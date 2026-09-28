package freshness

import (
	"errors"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// artifact is one audited instruction file. Path is nominal (as listed or as
// import-resolved); Real is symlink-resolved, and Root is found from RealDir.
type artifact struct {
	Path, Real, Dir, RealDir, Root string
}

func newArtifact(path, real string) artifact {
	realDir := filepath.Dir(real)
	return artifact{
		Path:    path,
		Real:    real,
		Dir:     filepath.Dir(path),
		RealDir: realDir,
		Root:    repoRoot(realDir),
	}
}

// repoRoot returns the nearest ancestor of dir (inclusive) holding a .git entry;
// a worktree's .git is a file, so any entry type counts.
func repoRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}

func canonical(p string) string {
	return strings.TrimSuffix(analyst.CanonicalizeRepoPrefix(p), "/")
}

type scanner struct {
	roots []string // with trailing "/"
	home  string
	index map[string]*repoIndex
	gitOK bool
}

func newScanner(home string, gitOK bool) *scanner {
	return &scanner{home: home, gitOK: gitOK, index: map[string]*repoIndex{}}
}

// underRoot returns the innermost known root containing p (without its
// trailing slash), or "" when p lies under none.
func (s *scanner) underRoot(p string) string {
	best := ""
	for _, r := range s.roots {
		if strings.HasPrefix(p+"/", r) && len(r)-1 > len(best) {
			best = strings.TrimSuffix(r, "/")
		}
	}
	return best
}

func hasDotPrefix(p string) bool {
	return strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../")
}

// bases lists the paths under which c is looked up, first base first. skip
// means c has nothing to resolve against and is never flagged.
func (s *scanner) bases(c Candidate, a artifact) (paths []string, skip bool) {
	p := c.Path
	expanded := false
	if strings.HasPrefix(p, "~/") {
		if s.home == "" {
			return nil, true
		}
		p = filepath.Join(s.home, p[2:])
		expanded = true
	}

	var raw []string
	switch c.Form {
	case FormImport:
		if filepath.IsAbs(p) {
			raw = []string{p}
		} else {
			raw = []string{filepath.Join(a.Dir, p), filepath.Join(a.RealDir, p)}
		}
	case FormLink:
		switch {
		case expanded:
			raw = []string{p}
		case filepath.IsAbs(p):
			if a.Root == "" {
				return nil, true
			}
			raw = []string{a.Root + p}
		default:
			raw = []string{filepath.Join(a.Dir, p), filepath.Join(a.RealDir, p)}
		}
	case FormBacktick:
		switch {
		case filepath.IsAbs(p):
			if s.underRoot(p) == "" && s.underRoot(canonical(p)) == "" {
				return nil, true
			}
			raw = []string{p, canonical(p)}
		case a.Root == "":
			return nil, true
		case hasDotPrefix(p):
			raw = []string{filepath.Join(a.RealDir, p), filepath.Join(a.Dir, p), filepath.Join(a.Root, p)}
		default:
			raw = []string{filepath.Join(a.Root, p), filepath.Join(a.RealDir, p)}
		}
	}
	return dedupClean(raw), false
}

func dedupClean(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		p = filepath.Clean(p)
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// knownExts are the extensions `.Ident` symbol trimming must not strip, so
// `docs/plan.md` is never kept alive by a `docs/plan/` directory.
var knownExts = func() map[string]bool {
	m := map[string]bool{}
	for _, e := range strings.Fields(`md go json yaml yml toml nix sh bash fish zsh py ts tsx js jsx mjs cjs rs
		txt sql lock mod sum cfg conf ini html css swift kt java rb lua tmpl tpl env example local bak
		xml csv db log png svg jpg jpeg gif pdf`) {
		m[e] = true
	}
	return m
}()

// Each symbol suffix must follow a non-"/" character, so a dotfile like
// `.envrc` is never trimmed down to its bare directory.
var (
	colonSymRe  = regexp.MustCompile(`[^/](:[A-Za-z_][A-Za-z0-9_]*)$`)
	anchorSymRe = regexp.MustCompile(`[^/](#[^/]+)$`)
	dotSymRe    = regexp.MustCompile(`[^/](\.([A-Za-z][A-Za-z0-9_]*))$`)
)

// symbolTrims returns every path reachable from p by stripping trailing
// symbol suffixes (`f.go:Foo`, `f.md#sec`, `pkg.Type.Method`), p excluded.
func symbolTrims(p string) []string {
	var out []string
	seen := map[string]bool{}
	work := []string{p}
	for len(work) > 0 {
		cur := work[0]
		work = work[1:]
		var next []string
		if m := colonSymRe.FindStringSubmatchIndex(cur); m != nil {
			next = append(next, cur[:m[2]])
		}
		if m := anchorSymRe.FindStringSubmatchIndex(cur); m != nil {
			next = append(next, cur[:m[2]])
		}
		if m := dotSymRe.FindStringSubmatchIndex(cur); m != nil && !knownExts[strings.ToLower(cur[m[4]:m[5]])] {
			next = append(next, cur[:m[2]])
		}
		for _, n := range next {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
				work = append(work, n)
			}
		}
	}
	return out
}

// live reports whether c names something that exists: directly under a base,
// after stripping a symbol suffix, or (relative backticks only) as the tail of
// some path in the repo.
func (s *scanner) live(c Candidate, a artifact, bases []string) bool {
	probe := append([]string(nil), bases...)
	if c.Form == FormLink {
		// Link targets are URLs, so `my%20file.md` names "my file.md".
		for _, b := range bases {
			if u, err := url.PathUnescape(b); err == nil && u != b {
				probe = append(probe, u)
			}
		}
	}
	for _, b := range probe {
		if exists(b) {
			return true
		}
	}
	for _, b := range probe {
		for _, t := range symbolTrims(b) {
			if exists(t) {
				return true
			}
		}
	}

	if c.Form != FormBacktick || a.Root == "" || filepath.IsAbs(c.Path) || strings.HasPrefix(c.Path, "~/") {
		return false
	}
	tok := strings.TrimSuffix(c.Path, "/")
	for strings.HasPrefix(tok, "./") {
		tok = tok[2:]
	}
	idx := s.repoIndex(a.Root)
	for _, t := range append([]string{tok}, symbolTrims(tok)...) {
		if idx.hasSuffix(t) {
			return true
		}
	}
	return false
}

// downgrade reports whether a missing Confident ref must be demoted to
// Ambiguous. Any doubt — no git, a git error — demotes, never promotes to dead.
func (s *scanner) downgrade(c Candidate, a artifact, bases []string) bool {
	if c.Form == FormBacktick && !hasDotPrefix(c.Path) && !s.firstSegmentExists(c, a, bases) {
		return true
	}

	root := s.underRoot(bases[0])
	if root == "" {
		return false
	}
	if !s.gitOK {
		return true
	}
	rel, err := filepath.Rel(root, bases[0])
	if err != nil {
		return true
	}
	err = exec.Command("git", "-C", root, "check-ignore", "-q", "--", rel).Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	return true
}

// firstSegmentExists reports whether any part of the tree c names is present:
// its first segment under Root or RealDir for a relative ref, or under the
// containing root for an absolute one.
func (s *scanner) firstSegmentExists(c Candidate, a artifact, bases []string) bool {
	if !filepath.IsAbs(c.Path) && !strings.HasPrefix(c.Path, "~/") {
		seg := firstSegment(c.Path)
		return exists(filepath.Join(a.Root, seg)) || exists(filepath.Join(a.RealDir, seg))
	}
	for _, b := range bases {
		root := s.underRoot(b)
		if root == "" {
			continue
		}
		rel, err := filepath.Rel(root, b)
		if err != nil {
			continue
		}
		if exists(filepath.Join(root, firstSegment(rel))) {
			return true
		}
	}
	return false
}

func firstSegment(p string) string {
	p = strings.TrimPrefix(filepath.ToSlash(p), "/")
	if i := strings.Index(p, "/"); i >= 0 {
		return p[:i]
	}
	return p
}

// repoIndex is every file and directory under a repo root, repo-relative.
type repoIndex struct {
	paths  []string
	byBase map[string][]string
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".worktrees": true,
	".direnv": true, "result": true, "target": true, "dist": true,
}

func (s *scanner) repoIndex(root string) *repoIndex {
	if idx, ok := s.index[root]; ok {
		return idx
	}
	idx := &repoIndex{byBase: map[string][]string{}}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil // an unreadable subtree is left out, not fatal
		}
		if d.IsDir() && skipDirs[d.Name()] {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		idx.paths = append(idx.paths, rel)
		idx.byBase[d.Name()] = append(idx.byBase[d.Name()], rel)
		return nil
	})
	s.index[root] = idx
	return idx
}

func (idx *repoIndex) hasSuffix(tok string) bool {
	if tok == "" {
		return false
	}
	for _, p := range idx.paths {
		if p == tok || strings.HasSuffix(p, "/"+tok) {
			return true
		}
	}
	return false
}

// sameName lists up to 5 repo-relative paths sharing path's basename — the
// likely new home of a moved file. path is absolute, under root.
func (s *scanner) sameName(root, path string) []string {
	out := []string{}
	if root == "" {
		return out
	}
	self, _ := filepath.Rel(root, path)
	self = filepath.ToSlash(self)
	hits := append([]string(nil), s.repoIndex(root).byBase[filepath.Base(path)]...)
	sort.Strings(hits)
	for _, h := range hits {
		if h == self {
			continue
		}
		out = append(out, h)
		if len(out) == 5 {
			break
		}
	}
	return out
}
