package analyst

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Skeptic verdicts. Unroutable is the finding-loss guard: the diagnosis verified
// on disk but no instruction-file edit can carry the fix (it lands in
// agent-smith's own code), so the proposal is escalated as an issue instead of
// being dropped.
const (
	VerdictUpheld     = "upheld"
	VerdictRefuted    = "refuted"
	VerdictUnroutable = "unroutable"
)

// Verdict is one Skeptic verdict on one Oracle proposal.
type Verdict struct {
	ProposalID string `json:"proposal_id"`
	Verdict    string `json:"verdict"`
	Reason     string `json:"reason"`
	Caveats    string `json:"caveats"`
}

// parseVerdict decodes a verdict file; ok is false for JSON that is not one.
func parseVerdict(data []byte) (Verdict, bool) {
	var v Verdict
	if err := json.Unmarshal(StripCodeFence(data), &v); err != nil {
		return Verdict{}, false
	}
	return v, v.ProposalID != "" && v.Verdict != ""
}

// LoadVerdicts reads every Skeptic verdict in dir, keyed by proposal id. Files
// that are not verdicts (the proposals themselves) are ignored.
func LoadVerdicts(dir string) map[string]Verdict {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	sort.Strings(paths)
	out := map[string]Verdict{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if v, ok := parseVerdict(data); ok {
			out[v.ProposalID] = v
		}
	}
	return out
}

// SplitUnroutable separates proposals the Skeptic marked unroutable from the ones
// assembly can hand to the applier.
func SplitUnroutable(props []Proposal, verdicts map[string]Verdict) (routable, unroutable []Proposal) {
	for _, p := range props {
		if verdicts[p.ID].Verdict == VerdictUnroutable {
			unroutable = append(unroutable, p)
			continue
		}
		routable = append(routable, p)
	}
	return routable, unroutable
}

// IssueFiler files one GitHub issue and returns its URL.
type IssueFiler func(title, body string) (string, error)

// GhIssueFiler files issues against repo with the gh CLI.
func GhIssueFiler(repo string) IssueFiler {
	return func(title, body string) (string, error) {
		out, err := exec.Command("gh", "issue", "create", "--repo", repo,
			"--title", title, "--body", body).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("gh issue create: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return lastLine(string(out)), nil
	}
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// Escalation is the outcome of one unroutable proposal: an issue URL, or the
// reason no issue was filed.
type Escalation struct {
	ID       string
	IssueURL string
	Skipped  string
}

func issueBody(p Proposal, v Verdict) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Filed by agent-smith: the Skeptic upheld this diagnosis but it cannot be routed to an instruction file.\n\n")
	fmt.Fprintf(&b, "**Artifact:** %s  \n", p.ImplicatedArtifact)
	if p.SignalType != "" {
		fmt.Fprintf(&b, "**Signal:** %s  \n", p.SignalType)
	}
	fmt.Fprintf(&b, "**Confidence:** %s\n\n", p.Confidence)
	fmt.Fprintf(&b, "## Diagnosis\n\n%s\n\n", p.Diagnosis)
	fmt.Fprintf(&b, "## Evidence\n\n")
	for _, e := range p.Evidence {
		fmt.Fprintf(&b, "- %s\n", e)
	}
	fmt.Fprintf(&b, "\n## Proposed change\n\n```\n%s\n```\n\n", p.ProposedChange)
	fmt.Fprintf(&b, "## Skeptic\n\n%s\n", v.Reason)
	return b.String()
}

// Escalate files one GitHub issue per unroutable proposal and records it in the
// reason-log so later runs recognize the finding. A proposal whose
// (artifact, signal) already has a reason-log entry is skipped — that is the same
// key the cluster filter dedups on, so a finding is filed once, not once per run.
// A nil file skips filing entirely (issue creation is off by default).
func Escalate(props []Proposal, verdicts map[string]Verdict, logDir, date string, file IssueFiler) ([]Escalation, error) {
	entries, err := ReadEntries(logDir)
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[artifactPath(e.Artifact)+"\x00"+e.Signal] = true
	}

	var out []Escalation
	for _, p := range props {
		esc := Escalation{ID: p.ID}
		switch {
		case known[artifactPath(p.ImplicatedArtifact)+"\x00"+p.SignalType]:
			esc.Skipped = "already in the reason-log"
		case file == nil:
			esc.Skipped = "issue filing disabled"
		default:
			url, err := file(fmt.Sprintf("agent-smith self-fix: %s", p.ID), issueBody(p, verdicts[p.ID]))
			if err != nil {
				esc.Skipped = err.Error()
				break
			}
			if _, err := WriteReasonLogs([]Proposal{p}, logDir, date); err != nil {
				return out, err
			}
			if err := LinkReasonLog(logDir, p.ID, "Issue", url); err != nil {
				return out, err
			}
			esc.IssueURL = url
		}
		out = append(out, esc)
	}
	return out, nil
}
