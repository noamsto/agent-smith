package freshness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/noamsto/agent-smith/internal/analyst"
)

const maxRuleExcerptBytes = 240

// Ref is one missing path reference, as written to freshness.json.
type Ref struct {
	ID          string   `json:"id"`
	Artifact    string   `json:"artifact"`
	Form        string   `json:"form"`
	Path        string   `json:"path"`
	Line        int      `json:"line"`
	RuleExcerpt string   `json:"rule_excerpt"`
	ResolvedTo  string   `json:"resolved_to"`
	SameName    []string `json:"same_name"`
}

// Skipped is an audit-set artifact that could not be scanned.
type Skipped struct {
	Artifact string `json:"artifact"`
	Reason   string `json:"reason"`
}

// Report is the scan output: every slice is non-nil so JSON carries arrays.
type Report struct {
	Scanned          []string  `json:"scanned"`
	Skipped          []Skipped `json:"skipped"`
	Dead             []Ref     `json:"dead"`
	AmbiguousMissing []Ref     `json:"ambiguous_missing"`
}

type Options struct {
	// Prefix, when set, keeps only artifacts under this repo root (worktree
	// roots are canonicalized to the main checkout, as in analyst cluster).
	Prefix string
}

// Scan audits paths and the files they transitively @import for references to
// paths that do not exist. A path that exists is never reported.
func Scan(paths []string, opts Options) Report {
	home, _ := os.UserHomeDir()
	_, err := exec.LookPath("git")
	return newScanner(home, err == nil).scan(paths, opts)
}

type audited struct {
	a     artifact
	cands []Candidate
}

func (s *scanner) scan(paths []string, opts Options) Report {
	r := Report{Scanned: []string{}, Skipped: []Skipped{}, Dead: []Ref{}, AmbiguousMissing: []Ref{}}

	queue := sortedAbs(paths)
	visited := map[string]bool{}
	var arts []audited
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		if _, err := os.Stat(p); err != nil {
			reason := "missing"
			if !errors.Is(err, fs.ErrNotExist) {
				reason = "unreadable: " + err.Error()
			}
			r.Skipped = append(r.Skipped, Skipped{Artifact: p, Reason: reason})
			continue
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			r.Skipped = append(r.Skipped, Skipped{Artifact: p, Reason: "unreadable: " + err.Error()})
			continue
		}
		if visited[real] {
			continue
		}
		visited[real] = true

		content, err := os.ReadFile(p)
		if err != nil {
			r.Skipped = append(r.Skipped, Skipped{Artifact: p, Reason: "unreadable: " + err.Error()})
			continue
		}
		au := audited{a: newArtifact(p, real), cands: Extract(string(content))}
		arts = append(arts, au)
		queue = append(queue, s.imports(au)...)
	}

	if opts.Prefix != "" {
		prefix := analyst.CanonicalizeRepoPrefix(opts.Prefix)
		arts = filterArts(arts, prefix)
		kept := r.Skipped[:0]
		for _, sk := range r.Skipped {
			if strings.HasPrefix(sk.Artifact, prefix) {
				kept = append(kept, sk)
			}
		}
		r.Skipped = kept
	}

	s.roots = nil
	seenRoot := map[string]bool{}
	for _, au := range arts {
		if au.a.Root != "" && !seenRoot[au.a.Root] {
			seenRoot[au.a.Root] = true
			s.roots = append(s.roots, strings.TrimSuffix(au.a.Root, "/")+"/")
		}
	}

	for _, au := range arts {
		r.Scanned = append(r.Scanned, au.a.Path)
		dead, amb := s.resolveArtifact(au)
		r.Dead = append(r.Dead, dead...)
		r.AmbiguousMissing = append(r.AmbiguousMissing, amb...)
	}

	sort.Strings(r.Scanned)
	sort.Slice(r.Skipped, func(i, j int) bool { return r.Skipped[i].Artifact < r.Skipped[j].Artifact })
	sortRefs(r.Dead)
	sortRefs(r.AmbiguousMissing)
	return r
}

func sortedAbs(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func filterArts(arts []audited, prefix string) []audited {
	var out []audited
	for _, au := range arts {
		if strings.HasPrefix(au.a.Path, prefix) {
			out = append(out, au)
		}
	}
	return out
}

// imports returns, sorted, the nominal path of every @import in au whose first
// existing base is a regular file.
func (s *scanner) imports(au audited) []string {
	var out []string
	for _, c := range au.cands {
		if c.Form != FormImport {
			continue
		}
		bs, skip := s.bases(c, au.a)
		if skip {
			continue
		}
		for _, b := range bs {
			info, err := os.Stat(b)
			if err != nil {
				continue
			}
			if info.Mode().IsRegular() {
				out = append(out, b)
			}
			break
		}
	}
	sort.Strings(out)
	return out
}

// resolveArtifact returns au's missing refs. Candidates sharing an ID (the same
// path on one line in two forms) collapse to one ref: if any form resolves live
// none is reported, and ambiguous wins over dead.
func (s *scanner) resolveArtifact(au audited) (dead, amb []Ref) {
	liveIDs := map[string]bool{}
	refs := map[string]Ref{}
	ambiguous := map[string]bool{}
	var order []string

	for _, c := range au.cands {
		cls := Classify(c)
		if cls == Skip {
			continue
		}
		bs, skip := s.bases(c, au.a)
		if skip || len(bs) == 0 {
			continue
		}
		id := refID(au.a.Path, c.Line, c.Path)
		if s.live(c, au.a, bs) {
			liveIDs[id] = true
			continue
		}
		isAmb := cls == Ambiguous || s.downgrade(c, au.a, bs)
		_, seen := refs[id]
		if !seen {
			order = append(order, id)
		}
		if !seen || (isAmb && !ambiguous[id]) {
			refs[id] = s.newRef(id, c, au.a, bs)
			ambiguous[id] = isAmb
		}
	}

	for _, id := range order {
		if liveIDs[id] {
			continue
		}
		if ambiguous[id] {
			amb = append(amb, refs[id])
		} else {
			dead = append(dead, refs[id])
		}
	}
	return dead, amb
}

func (s *scanner) newRef(id string, c Candidate, a artifact, bases []string) Ref {
	root := s.underRoot(bases[0])
	if root == "" {
		root = s.underRoot(canonical(bases[0]))
	}
	return Ref{
		ID:          id,
		Artifact:    a.Path,
		Form:        string(c.Form),
		Path:        c.Path,
		Line:        c.Line,
		RuleExcerpt: capBytes(c.Text, maxRuleExcerptBytes),
		ResolvedTo:  bases[0],
		SameName:    s.sameName(root, bases[0]),
	}
}

func refID(artifact string, line int, path string) string {
	sum := sha256.Sum256([]byte(artifact + "\n" + strconv.Itoa(line) + "\n" + path))
	return hex.EncodeToString(sum[:])[:12]
}

// capBytes truncates s to at most n bytes without splitting a UTF-8 rune.
func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func sortRefs(refs []Ref) {
	sort.Slice(refs, func(i, j int) bool {
		a, b := refs[i], refs[j]
		if a.Artifact != b.Artifact {
			return a.Artifact < b.Artifact
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Path < b.Path
	})
}

func WriteReport(r Report, path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func ReadReport(path string) (Report, error) {
	var r Report
	b, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	return r, err
}
