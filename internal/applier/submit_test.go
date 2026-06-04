package applier

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// fakeRunner records calls and returns canned output keyed by the command verb.
type fakeRunner struct {
	calls        [][]string
	status       string // git status --porcelain stdout
	prURL        string // gh pr create stdout
	failVerb     string // "add"|"status"|"commit"|"push"|"gh" → that command returns an error
	revListCount string // git rev-list --count stdout ("" = "1")
	diffNames    string // git diff --name-only stdout ("" = no files to verify)
}

func (f *fakeRunner) run(dir, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	verb := name
	if name == "git" && len(args) > 0 {
		verb = args[0]
	}
	if f.failVerb != "" && verb == f.failVerb {
		return []byte("boom"), fmt.Errorf("simulated %s failure", verb)
	}
	if name == "git" && len(args) > 0 && args[0] == "status" {
		return []byte(f.status), nil
	}
	if name == "git" && len(args) > 0 && args[0] == "rev-list" {
		n := f.revListCount
		if n == "" {
			n = "1"
		}
		return []byte(n + "\n"), nil
	}
	if name == "git" && len(args) > 0 && args[0] == "diff" {
		return []byte(f.diffNames), nil
	}
	if name == "gh" {
		return []byte("Creating pull request...\n" + f.prURL + "\n"), nil
	}
	return nil, nil
}

func sampleProposal() analyst.Proposal {
	return analyst.Proposal{
		ID: "glitch-skeleton", ImplicatedArtifact: "/g/CLAUDE.md#reading-code",
		FixType: "strengthen", Evidence: []string{"s1:1", "≥3 sessions"},
		Diagnosis: "rule ignored", ProposedChange: "make imperative",
		Confidence: "high", ReasonLog: "fewer whole-file reads",
	}
}

func TestCommitMessageNoDoublePrefix(t *testing.T) {
	// An editor summary that is already a conventional-commit subject must be
	// used as-is — not given a second type prefix ("chore: feat(...): ...",
	// the malformed title nix-config#2 shipped with).
	ed := EditorResult{Applied: true, Summary: "feat(claude-code): enforce skeleton-first reads"}
	title, _ := commitMessage(sampleProposal(), ed)
	if title != "feat(claude-code): enforce skeleton-first reads" {
		t.Errorf("title = %q; want the editor subject unchanged", title)
	}
	// A plain imperative summary still gets the fix-type prefix.
	ed.Summary = "raise skeleton-first rule"
	title, _ = commitMessage(sampleProposal(), ed)
	if title != "docs: raise skeleton-first rule" {
		t.Errorf("title = %q; want a docs: prefix", title)
	}
}

func TestSubmitPreflightRejectsExtraCommits(t *testing.T) {
	// More than one commit over origin/<base> means unpushed local commits are
	// leaking into the PR (how nix-config#2 picked up an unrelated commit).
	// Preflight must abort BEFORE anything is pushed.
	f := &fakeRunner{status: " M CLAUDE.md", revListCount: "3"}
	tg := Target{BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
	_, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(),
		EditorResult{Applied: true, Summary: "s"}, t.TempDir(), false)
	if err == nil || skipped {
		t.Fatalf("expected preflight error, got skipped=%v err=%v", skipped, err)
	}
	if !strings.Contains(err.Error(), "preflight") {
		t.Errorf("error %q should mention preflight", err)
	}
	for _, c := range f.calls {
		if (c[0] == "git" && c[1] == "push") || c[0] == "gh" {
			t.Errorf("nothing must be pushed after a preflight failure; saw %v", c)
		}
	}
}

func TestSubmitPreflightRejectsSurpriseFiles(t *testing.T) {
	// The branch diff must not touch files the editor didn't report.
	f := &fakeRunner{status: " M CLAUDE.md", diffNames: "CLAUDE.md\nUNRELATED.txt\n"}
	tg := Target{BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
	_, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(),
		EditorResult{Applied: true, Summary: "s", FilesChanged: []string{"CLAUDE.md"}}, t.TempDir(), false)
	if err == nil || skipped {
		t.Fatalf("expected preflight error, got skipped=%v err=%v", skipped, err)
	}
	if !strings.Contains(err.Error(), "UNRELATED.txt") {
		t.Errorf("error %q should name the surprise file", err)
	}
	for _, c := range f.calls {
		if (c[0] == "git" && c[1] == "push") || c[0] == "gh" {
			t.Errorf("nothing must be pushed after a preflight failure; saw %v", c)
		}
	}
}

func TestDoublePrefixLint(t *testing.T) {
	// The preflight title lint is the backstop should commitMessage regress.
	for title, double := range map[string]bool{
		"chore: feat(claude-code): enforce skeleton-first reads": true,
		"docs: fix: something":                       true,
		"feat(claude-code): enforce skeleton-first":  false,
		"docs: raise skeleton-first rule":            false,
		"docs: update the chore: of the day section": false, // prefix-like text mid-sentence is fine
	} {
		if got := doublePrefixRe.MatchString(title); got != double {
			t.Errorf("doublePrefixRe(%q) = %v, want %v", title, got, double)
		}
	}
}

func TestSubmitCreatesPR(t *testing.T) {
	dir := t.TempDir()
	rlPath := filepath.Join(dir, "2026-06-01-glitch-skeleton.md")
	if err := os.WriteFile(rlPath, []byte(sampleEntry), 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{status: " M CLAUDE.md", prURL: "https://github.com/x/y/pull/9", diffNames: "CLAUDE.md\n"}
	tg := Target{RepoRoot: "/r", BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
	ed := EditorResult{Applied: true, FilesChanged: []string{"CLAUDE.md"}, Summary: "raise skeleton-first rule"}

	url, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(), ed, dir, false)
	if err != nil || skipped {
		t.Fatalf("Submit: url=%q skipped=%v err=%v", url, skipped, err)
	}
	if url != "https://github.com/x/y/pull/9" {
		t.Errorf("PR URL = %q", url)
	}
	// Verb sequence: add, status, commit, preflight (rev-parse/rev-list/diff),
	// push, then gh pr create.
	verbs := []string{}
	for _, c := range f.calls {
		verbs = append(verbs, c[0]+" "+c[1])
	}
	want := []string{"git add", "git status", "git commit",
		"git rev-parse", "git rev-list", "git diff", "git push", "gh pr"}
	if !slices.Equal(verbs, want) {
		t.Errorf("call sequence = %v, want %v", verbs, want)
	}
	// gh pr create got the right assignee + head.
	last := f.calls[len(f.calls)-1]
	joined := strings.Join(last, " ")
	if !strings.Contains(joined, "--assignee @me") || !strings.Contains(joined, "--head docs/agent-smith-glitch-skeleton") {
		t.Errorf("gh args = %q", joined)
	}
	// reason-log got the PR link.
	got, err := os.ReadFile(rlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "**PR:** https://github.com/x/y/pull/9") {
		t.Errorf("reason-log not updated:\n%s", got)
	}
}

func TestSubmitSkipsWhenEditorDeclined(t *testing.T) {
	f := &fakeRunner{}
	_, skipped, err := Submit(f.run, Target{}, "/wt", sampleProposal(),
		EditorResult{Applied: false, Reason: "content drifted"}, t.TempDir(), false)
	if err != nil || !skipped {
		t.Fatalf("expected skip, got skipped=%v err=%v", skipped, err)
	}
	if len(f.calls) != 0 {
		t.Errorf("no git/gh calls expected, got %v", f.calls)
	}
}

func TestSubmitSkipsWhenNoDiff(t *testing.T) {
	f := &fakeRunner{status: ""} // editor applied but produced no change
	_, skipped, err := Submit(f.run, Target{BranchName: "b", Base: "main"}, "/wt",
		sampleProposal(), EditorResult{Applied: true}, t.TempDir(), false)
	if err != nil || !skipped {
		t.Fatalf("expected skip on empty diff, got skipped=%v err=%v", skipped, err)
	}
	// add + status ran, but no commit.
	for _, c := range f.calls {
		if c[0] == "git" && c[1] == "commit" {
			t.Error("commit should not run on empty diff")
		}
	}
}

func TestSubmitErrorPaths(t *testing.T) {
	for _, verb := range []string{"add", "status", "commit", "push", "gh"} {
		t.Run(verb, func(t *testing.T) {
			f := &fakeRunner{status: " M f", prURL: "https://github.com/x/y/pull/9", failVerb: verb}
			tg := Target{BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
			_, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(),
				EditorResult{Applied: true, Summary: "s"}, t.TempDir(), false)
			if err == nil {
				t.Fatalf("expected error when %q fails", verb)
			}
			if skipped {
				t.Errorf("skipped should be false on error")
			}
			step := verb
			if verb == "gh" {
				step = "gh pr create"
			}
			if !strings.Contains(err.Error(), step) {
				t.Errorf("error %q should mention step %q", err, step)
			}
		})
	}
}

func TestSubmitAppendPRLinkFailure(t *testing.T) {
	// Successful run, but the reason-log dir has no matching entry → AppendPRLink errors.
	f := &fakeRunner{status: " M f", prURL: "https://github.com/x/y/pull/9"}
	tg := Target{BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
	url, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(),
		EditorResult{Applied: true, Summary: "s"}, t.TempDir(), false)
	if err == nil {
		t.Fatal("expected error when reason-log entry is missing")
	}
	if skipped || url != "" {
		t.Errorf("on AppendPRLink failure: url=%q skipped=%v", url, skipped)
	}
	if !strings.Contains(err.Error(), "append PR link") {
		t.Errorf("error %q should mention append PR link", err)
	}
}

func TestSubmitNonURLGhOutput(t *testing.T) {
	// gh stdout ends with the "Creating pull request..." line (empty prURL) → not a URL.
	f := &fakeRunner{status: " M f", prURL: ""}
	tg := Target{BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
	url, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(),
		EditorResult{Applied: true, Summary: "s"}, t.TempDir(), false)
	if err == nil {
		t.Fatal("expected error when gh output is not a URL")
	}
	if skipped || url != "" {
		t.Errorf("on non-URL gh output: url=%q skipped=%v", url, skipped)
	}
	if !strings.Contains(err.Error(), "gh pr create") {
		t.Errorf("error %q should mention gh pr create", err)
	}
}

func TestCommitMessageFallback(t *testing.T) {
	p := sampleProposal()
	p.Diagnosis = "first line of diagnosis\nsecond line"
	title, body := commitMessage(p, EditorResult{}) // empty Summary
	if title != "docs: first line of diagnosis" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(body, "Proposed by agent-smith (proposal glitch-skeleton, fix_type=strengthen)") {
		t.Errorf("body missing provenance:\n%s", body)
	}
}

func TestParseEditorResultToleratesFences(t *testing.T) {
	fenced := "```json\n{\"applied\":true,\"files_changed\":[\"CLAUDE.md\"],\"summary\":\"s\",\"reason\":\"\"}\n```"
	ed, err := ParseEditorResult([]byte(fenced))
	if err != nil {
		t.Fatalf("ParseEditorResult: %v", err)
	}
	if !ed.Applied || ed.Summary != "s" {
		t.Errorf("got %+v", ed)
	}
}

func TestSubmitDraftFlag(t *testing.T) {
	// draft=true adds --draft to gh pr create; draft=false omits it.
	for _, draft := range []bool{true, false} {
		dir := t.TempDir()
		rlPath := filepath.Join(dir, "2026-06-01-glitch-skeleton.md")
		if err := os.WriteFile(rlPath, []byte(sampleEntry), 0o644); err != nil {
			t.Fatal(err)
		}
		f := &fakeRunner{status: " M CLAUDE.md", prURL: "https://github.com/x/y/pull/9"}
		tg := Target{RepoRoot: "/r", BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
		ed := EditorResult{Applied: true, FilesChanged: []string{"CLAUDE.md"}, Summary: "x"}

		if _, _, err := Submit(f.run, tg, "/wt", sampleProposal(), ed, dir, draft); err != nil {
			t.Fatalf("draft=%v: Submit: %v", draft, err)
		}
		last := f.calls[len(f.calls)-1]
		joined := strings.Join(last, " ")
		if got := strings.Contains(joined, "--draft"); got != draft {
			t.Errorf("draft=%v: --draft present=%v; gh args=%q", draft, got, joined)
		}
	}
}
