package applier

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
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

// doublePrefixRe matches a title that starts with TWO conventional-commit
// prefixes (e.g. "chore: feat(scope): ..."), the malformed shape nix-config#2
// shipped with. Preflight's backstop should commitMessage ever regress.
var doublePrefixRe = regexp.MustCompile(`^[a-z]+(\([^)]*\))?!?: [a-z]+(\([^)]*\))?!?: `)

// GroupItem pairs a proposal with the editor's result for it. A grouped submit
// carries one item per proposal that targets the same artifact, applied into the
// same worktree in plan order (issue #9).
type GroupItem struct {
	Proposal analyst.Proposal
	Editor   EditorResult
}

// preflight validates the assembled PR before anything is pushed: a sane title,
// exactly one commit over the (remote-tracking) base, and no files beyond what
// the editors reported (the union across the group). Failing any check aborts
// submit instead of opening a malformed PR.
func preflight(run runner, t Target, wt, title string, allowedFiles []string) error {
	if doublePrefixRe.MatchString(title) {
		return fmt.Errorf("preflight: doubled conventional-commit prefix in title %q", title)
	}
	baseRef := t.Base
	if _, err := run(wt, "git", "rev-parse", "--verify", "--quiet",
		"refs/remotes/origin/"+t.Base); err == nil {
		baseRef = "refs/remotes/origin/" + t.Base
	}
	out, err := run(wt, "git", "rev-list", "--count", baseRef+"..HEAD")
	if err != nil {
		return fmt.Errorf("preflight: rev-list: %w", err)
	}
	if n := strings.TrimSpace(string(out)); n != "1" {
		return fmt.Errorf("preflight: branch has %s commits over %s, want exactly 1 (unpushed local commits leaking in?)", n, baseRef)
	}
	if len(allowedFiles) == 0 {
		return nil // no editor reported a file list; nothing to verify against
	}
	out, err = run(wt, "git", "diff", "--name-only", baseRef+"..HEAD")
	if err != nil {
		return fmt.Errorf("preflight: diff: %w", err)
	}
	allowed := make(map[string]bool, len(allowedFiles))
	for _, f := range allowedFiles {
		allowed[f] = true
	}
	for _, f := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if f != "" && !allowed[f] {
			return fmt.Errorf("preflight: diff touches %q, which no editor reported in files_changed %v", f, allowedFiles)
		}
	}
	return nil
}

// proposalBody renders one proposal's diagnosis, evidence, and reason-log into the
// shared PR body — the per-proposal block enumerated under a grouped PR.
func proposalBody(b *strings.Builder, p analyst.Proposal) {
	fmt.Fprintf(b, "%s\n\nEvidence:\n", p.Diagnosis)
	for _, e := range p.Evidence {
		fmt.Fprintf(b, "- %s\n", e)
	}
	fmt.Fprintf(b, "\n%s\n\nProposed by agent-smith (proposal %s, fix_type=%s)\n",
		p.ReasonLog, p.ID, p.FixType)
}

// singleMessage is the title/body for a group of one — unchanged from the original
// one-PR-per-proposal shape so the common case stays identical.
func singleMessage(p analyst.Proposal, ed EditorResult) (title, body string) {
	summary := ed.Summary
	if summary == "" {
		summary = firstLine(p.Diagnosis)
	}
	title = fmt.Sprintf("%s: %s", commitType(p.FixType), summary)
	if ccSubjectRe.MatchString(summary) {
		title = summary // already conventional — don't double the prefix
	}
	var b strings.Builder
	proposalBody(&b, p)
	return title, b.String()
}

// groupMessage builds the conventional-commit subject and body for a group of
// applied items. A group of one delegates to singleMessage; a larger group gets a
// title naming the artifact and a body enumerating each carried proposal (id +
// summary) so the reviewer sees one coherent diff backed by every proposal it
// folds in. The branch's own prefix already encodes the group's type.
func groupMessage(t Target, items []GroupItem) (title, body string) {
	if len(items) == 1 {
		return singleMessage(items[0].Proposal, items[0].Editor)
	}
	prefix := strings.SplitN(t.BranchName, "/", 2)[0]
	artifact := filepath.Base(t.FilePath)
	title = fmt.Sprintf("%s: apply %d agent-smith proposals to %s", prefix, len(items), artifact)

	var b strings.Builder
	fmt.Fprintf(&b, "Grouped %d agent-smith proposals targeting %s:\n\n", len(items), artifact)
	for _, it := range items {
		summary := it.Editor.Summary
		if summary == "" {
			summary = firstLine(it.Proposal.Diagnosis)
		}
		fmt.Fprintf(&b, "- %s: %s\n", it.Proposal.ID, summary)
	}
	for _, it := range items {
		fmt.Fprintf(&b, "\n---\n\n## %s\n\n", it.Proposal.ID)
		proposalBody(&b, it.Proposal)
	}
	return title, b.String()
}

// Submit commits a group's worktree edits, pushes the shared branch, opens one
// human-gated PR, and records the PR link in every carried proposal's reason-log
// entry. The items have already been applied sequentially into wt by the editor (so
// each edit saw the prior one); Submit only assembles the single coherent diff into
// one PR. It returns skipped=true (no error) when no item applied or the combined
// diff is empty. Never commits to the default branch. wt is the worktree checkout
// (distinct from t.RepoRoot).
func Submit(run runner, t Target, wt string, items []GroupItem, reasonLogDir string, draft bool) (prURL string, skipped bool, err error) {
	applied := items[:0:0]
	for _, it := range items {
		if it.Editor.Applied {
			applied = append(applied, it)
		}
	}
	if len(applied) == 0 {
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
		return "", true, nil // every editor was a no-op
	}
	title, body := groupMessage(t, applied)
	if _, err := run(wt, "git", "commit", "-m", title, "-m", body); err != nil {
		return "", false, fmt.Errorf("git commit: %w", err)
	}
	var allowed []string
	for _, it := range applied {
		allowed = append(allowed, it.Editor.FilesChanged...)
	}
	if err := preflight(run, t, wt, title, allowed); err != nil {
		return "", false, err
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
	for _, it := range applied {
		if err := AppendPRLink(reasonLogDir, it.Proposal.ID, prURL); err != nil {
			return "", false, fmt.Errorf("append PR link for %s (PR already created at %s): %w", it.Proposal.ID, prURL, err)
		}
	}
	return prURL, false, nil
}
