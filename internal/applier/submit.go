package applier

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// runner runs a command in dir and returns combined output. Injected so tests run
// offline against a fake.
type runner func(dir, name string, args ...string) ([]byte, error)

// execRunner is the production runner.
func execRunner(dir, name string, args ...string) ([]byte, error) {
	c := exec.Command(name, args...)
	c.Dir = dir
	return c.CombinedOutput()
}

// Run is the exported production runner (exec in dir), for the CLI to pass to Submit.
// Pass to Submit as the runner in production.
func Run(dir, name string, args ...string) ([]byte, error) {
	return execRunner(dir, name, args...)
}

// EditorResult is what the editor subagent returns after editing the worktree.
type EditorResult struct {
	Applied      bool     `json:"applied"`
	FilesChanged []string `json:"files_changed"`
	Summary      string   `json:"summary"`
	Reason       string   `json:"reason"`
}

// ParseEditorResult decodes an editor subagent's JSON result, tolerating a
// surrounding markdown code fence (the same leniency the analyst applies to the
// Oracle's output).
func ParseEditorResult(data []byte) (EditorResult, error) {
	var ed EditorResult
	if err := json.Unmarshal(analyst.StripCodeFence(data), &ed); err != nil {
		return EditorResult{}, err
	}
	return ed, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// ccSubjectRe matches a summary that is already a conventional-commit subject
// (e.g. "feat(scope): ..."), so commitMessage must not prepend a second type.
var ccSubjectRe = regexp.MustCompile(`^[a-z]+(\([^)]*\))?!?: `)

// commitMessage builds the conventional-commit subject and body for a proposal.
func commitMessage(p analyst.Proposal, ed EditorResult) (title, body string) {
	summary := ed.Summary
	if summary == "" {
		summary = firstLine(p.Diagnosis)
	}
	title = fmt.Sprintf("%s: %s", commitType(p.FixType), summary)
	if ccSubjectRe.MatchString(summary) {
		title = summary // already conventional — don't double the prefix
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nEvidence:\n", p.Diagnosis)
	for _, e := range p.Evidence {
		fmt.Fprintf(&b, "- %s\n", e)
	}
	fmt.Fprintf(&b, "\n%s\n\nProposed by agent-smith (proposal %s, fix_type=%s)\n",
		p.ReasonLog, p.ID, p.FixType)
	return title, b.String()
}

// Submit commits the worktree edit, pushes the branch, opens a human-gated PR, and
// records the PR link in the reason-log. It returns skipped=true (no error) when
// the editor declined or produced no diff. Never commits to the default branch.
// wt is the path to the git worktree checkout (distinct from t.RepoRoot).
func Submit(run runner, t Target, wt string, p analyst.Proposal, ed EditorResult, reasonLogDir string, draft bool) (prURL string, skipped bool, err error) {
	if !ed.Applied {
		return "", true, nil
	}
	if _, err := run(wt, "git", "add", "-A"); err != nil {
		return "", false, fmt.Errorf("git add: %w", err)
	}
	status, err := run(wt, "git", "status", "--porcelain")
	if err != nil {
		return "", false, fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(string(status)) == "" {
		return "", true, nil // editor was a no-op
	}
	title, body := commitMessage(p, ed)
	if _, err := run(wt, "git", "commit", "-m", title, "-m", body); err != nil {
		return "", false, fmt.Errorf("git commit: %w", err)
	}
	if _, err := run(wt, "git", "push", "-u", "origin", t.BranchName); err != nil {
		return "", false, fmt.Errorf("git push: %w", err)
	}
	ghArgs := []string{"pr", "create", "--assignee", "@me",
		"--title", title, "--body", body, "--head", t.BranchName, "--base", t.Base}
	if draft {
		ghArgs = append(ghArgs, "--draft")
	}
	out, err := run(wt, "gh", ghArgs...)
	if err != nil {
		return "", false, fmt.Errorf("gh pr create: %w", err)
	}
	prURL = lastLine(string(out))
	if !strings.HasPrefix(prURL, "https://") {
		return "", false, fmt.Errorf("gh pr create: unexpected output (last line %q)", prURL)
	}
	if err := AppendPRLink(reasonLogDir, p.ID, prURL); err != nil {
		return "", false, fmt.Errorf("append PR link (PR already created at %s): %w", prURL, err)
	}
	return prURL, false, nil
}
