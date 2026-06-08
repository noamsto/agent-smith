package analyst

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Proposal is one improvement the Oracle proposes for one cluster.
type Proposal struct {
	ID                 string   `json:"id"`
	ImplicatedArtifact string   `json:"implicated_artifact"`
	FixType            string   `json:"fix_type"`
	Evidence           []string `json:"evidence"`
	Diagnosis          string   `json:"diagnosis"`
	ProposedChange     string   `json:"proposed_change"`
	Confidence         string   `json:"confidence"`
	ReasonLog          string   `json:"reason_log"`
}

var validFixTypes = map[string]bool{
	"add": true, "strengthen": true, "fix-stale": true,
	"remove": true, "escalate-out-of-instructions": true, "skip": true,
}
var validConfidence = map[string]bool{"high": true, "medium": true, "low": true}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = slugRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

// StripCodeFence removes an optional surrounding markdown code fence
// (```json … ``` or ``` … ```) that LLMs commonly wrap JSON output in.
func StripCodeFence(b []byte) []byte {
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "```") {
		return b
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 { // drop the opening ``` / ```json line
		s = s[i+1:]
	} else {
		s = ""
	}
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
	return []byte(s)
}

func fnv32a(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

// Validate checks a proposal has all required fields and valid enums.
func (p Proposal) Validate() error {
	switch {
	case p.ID == "":
		return fmt.Errorf("missing id")
	case p.ImplicatedArtifact == "":
		return fmt.Errorf("%s: missing implicated_artifact", p.ID)
	case !validFixTypes[p.FixType]:
		return fmt.Errorf("%s: invalid fix_type %q", p.ID, p.FixType)
	case !validConfidence[p.Confidence]:
		return fmt.Errorf("%s: invalid confidence %q", p.ID, p.Confidence)
	case len(p.Evidence) == 0:
		return fmt.Errorf("%s: missing evidence", p.ID)
	case p.Diagnosis == "":
		return fmt.Errorf("%s: missing diagnosis", p.ID)
	case p.ProposedChange == "" && p.FixType != "skip": // a skip declines, so it has nothing to change
		return fmt.Errorf("%s: missing proposed_change", p.ID)
	case p.ReasonLog == "":
		return fmt.Errorf("%s: missing reason_log", p.ID)
	}
	return nil
}

// LoadProposals reads every *.json in dir, validates each, and dedups by ID
// (first occurrence wins). Returns a sorted-by-ID slice and a slice of errors
// for files that failed to parse or validate.
func LoadProposals(dir string) ([]Proposal, []error) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(paths)
	var out []Proposal
	var errs []error
	seen := map[string]bool{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		var prop Proposal
		if err := json.Unmarshal(StripCodeFence(data), &prop); err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid JSON: %w", p, err))
			continue
		}
		if err := prop.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		if seen[prop.ID] {
			continue
		}
		seen[prop.ID] = true
		out = append(out, prop)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, errs
}

// WriteProposals writes the validated proposals as an indented JSON array.
func WriteProposals(props []Proposal, outPath string) error {
	data, err := json.MarshalIndent(props, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, append(data, '\n'), 0o644)
}

// WriteReasonLogs writes one append-only markdown file per proposal under dir,
// named <date>-<slug>.md. Existing files are left untouched (the ledger is
// append-only across runs). Returns how many new files were written.
func WriteReasonLogs(props []Proposal, dir, date string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	written := 0
	for _, p := range props {
		slug := slugify(p.ID)
		if slug == "" { // non-ASCII/empty id → deterministic distinct fallback
			slug = fmt.Sprintf("%08x", fnv32a(p.ID))
		}
		path := filepath.Join(dir, date+"-"+slug+".md")

		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", p.ID)
		fmt.Fprintf(&b, "**Artifact:** %s  \n", p.ImplicatedArtifact)
		fmt.Fprintf(&b, "**Fix type:** %s  **Confidence:** %s  **Date:** %s\n\n", p.FixType, p.Confidence, date)
		fmt.Fprintf(&b, "## Diagnosis\n\n%s\n\n", p.Diagnosis)
		fmt.Fprintf(&b, "## Evidence\n\n")
		for _, e := range p.Evidence {
			fmt.Fprintf(&b, "- %s\n", e)
		}
		fmt.Fprintf(&b, "\n## Proposed change\n\n```\n%s\n```\n\n", p.ProposedChange)
		fmt.Fprintf(&b, "## Expected effect\n\n%s\n\n", p.ReasonLog)
		fmt.Fprintf(&b, "<!-- PR link appended by the applier; outcome appended by deja-vu -->\n")

		// O_EXCL makes append-only atomic: refuse to clobber an existing entry.
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return written, err
		}
		if _, err := f.WriteString(b.String()); err != nil {
			f.Close()
			return written, err
		}
		if err := f.Close(); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
