package applier

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// writeProposals marshals a proposals JSON file and returns its path.
func writeProposals(t *testing.T, json string) string {
	t.Helper()
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(json), 0o644); err != nil {
		t.Fatal(err)
	}
	return pf
}

// writeReasonLogEntry writes a minimal reason-log file mirroring the analyst's
// format: a heading, the **Artifact:** line dedup keys on, an optional **PR:**
// link, and the deja-vu outcome placeholder unless resolved.
func writeReasonLogEntry(t *testing.T, dir, id, artifact, prURL string, resolved bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "# " + id + "\n\n**Artifact:** " + artifact + "  \n\n## Diagnosis\n\nd\n\n"
	if prURL != "" {
		content += "**PR:** " + prURL + "\n\n"
	}
	if !resolved {
		content += "<!-- outcome: " + analyst.OutcomeOpen + " -->\n"
	} else {
		content += "<!-- outcome: " + analyst.OutcomeMerged + " -->\n"
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func statusByID(plan []PlanEntry) map[string]PlanEntry {
	m := make(map[string]PlanEntry, len(plan))
	for _, e := range plan {
		m[e.ProposalID] = e
	}
	return m
}

// TestDedupPriorReasonLog: a prior unresolved reason-log entry for the SAME
// artifact+behavior but a different proposal id (different slug/branch) is the
// 06-04 vs 06-07 skeleton-first collision. The fresh proposal must skip.
func TestDedupPriorReasonLog(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	artifact := filepath.Join(root, "CLAUDE.md") + "#reading-code-skeleton-first"

	rl := filepath.Join(t.TempDir(), "reason-log")
	writeReasonLogEntry(t, rl, "glitch-2026-06-04-skeleton-first-read-ignored",
		artifact, "https://github.com/noamsto/nix-config/pull/2", false)

	pf := writeProposals(t, `[
	  {"id":"glitch-2026-06-07-skeleton-first-large-reads-ignored","implicated_artifact":"`+artifact+`","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`)

	plan, err := Prepare(pf, "", DedupConfig{ReasonLogDir: rl}, false)
	if err != nil {
		t.Fatal(err)
	}
	e := plan[0]
	if e.Status != StatusDuplicate {
		t.Fatalf("status = %q, want %q", e.Status, StatusDuplicate)
	}
	if e.Supersedes == "" {
		t.Error("Supersedes should name the prior entry")
	}
}

// TestDedupResolvedReasonLogNotDeduped: once deja-vu has recorded an outcome
// (placeholder gone), the prior entry no longer blocks — re-proposing a resolved
// fix is issue #4's territory, not pending-work dedup.
func TestDedupResolvedReasonLogNotDeduped(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	artifact := filepath.Join(root, "CLAUDE.md") + "#reading-code-skeleton-first"

	rl := filepath.Join(t.TempDir(), "reason-log")
	writeReasonLogEntry(t, rl, "glitch-2026-06-04-skeleton-first-read-ignored",
		artifact, "https://github.com/noamsto/nix-config/pull/2", true)

	pf := writeProposals(t, `[
	  {"id":"glitch-2026-06-07-skeleton-first-large-reads-ignored","implicated_artifact":"`+artifact+`","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`)

	plan, err := Prepare(pf, "", DedupConfig{ReasonLogDir: rl}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan[0].Status != StatusReady {
		t.Errorf("status = %q, want %q (resolved entry must not block)", plan[0].Status, StatusReady)
	}
}

// TestDedupOpenPR: an open PR whose head branch is the branch this proposal would
// push to is a pending duplicate of the same work.
func TestDedupOpenPR(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	p := analyst.Proposal{
		ID:                 "glitch-2026-06-07-skeleton-first",
		ImplicatedArtifact: filepath.Join(root, "CLAUDE.md") + "#x",
		FixType:            "strengthen",
	}

	pf := writeProposals(t, `[
	  {"id":"`+p.ID+`","implicated_artifact":"`+p.ImplicatedArtifact+`","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`)

	// The proposal pushes to its group branch (artifact-derived); discover it from a
	// dedup-free run, then stand up an open PR on that branch.
	base, err := Prepare(pf, "", DedupConfig{}, false)
	if err != nil {
		t.Fatal(err)
	}
	branch := base[0].BranchName

	plan, err := Prepare(pf, "", DedupConfig{OpenPRsForRepo: func(_ string) ([]PullRequest, error) {
		return []PullRequest{{Number: 12, Title: "t", HeadRefName: branch, URL: "https://github.com/noamsto/nix-config/pull/12"}}, nil
	}}, false)
	if err != nil {
		t.Fatal(err)
	}
	e := plan[0]
	if e.Status != StatusDuplicate {
		t.Fatalf("status = %q, want %q", e.Status, StatusDuplicate)
	}
	if e.Supersedes == "" {
		t.Error("Supersedes should name the open PR")
	}
}

// TestDedupDistinctNotDeduped: a different artifact and a different behavior on
// the same file are both left ready — dedup must not over-match.
func TestDedupDistinctNotDeduped(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	skeleton := filepath.Join(root, "CLAUDE.md") + "#reading-code-skeleton-first"
	otherSection := filepath.Join(root, "CLAUDE.md") + "#git-worktrees"

	rl := filepath.Join(t.TempDir(), "reason-log")
	writeReasonLogEntry(t, rl, "glitch-2026-06-04-skeleton-first-read-ignored",
		skeleton, "https://github.com/noamsto/nix-config/pull/2", false)

	pf := writeProposals(t, `[
	  {"id":"p-other-section","implicated_artifact":"`+otherSection+`","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-no-prior","implicated_artifact":"`+skeleton+`x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`)

	plan, err := Prepare(pf, "", DedupConfig{
		OpenPRsForRepo: func(_ string) ([]PullRequest, error) {
			return []PullRequest{{Number: 99, HeadRefName: "docs/agent-smith-unrelated", URL: "u"}}, nil
		},
		ReasonLogDir: rl,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := statusByID(plan)
	if got["p-other-section"].Status != StatusReady {
		t.Errorf("different behavior on same file: status = %q, want ready", got["p-other-section"].Status)
	}
	if got["p-no-prior"].Status != StatusReady {
		t.Errorf("different artifact: status = %q, want ready", got["p-no-prior"].Status)
	}
}

func TestPreparePerRepoDedup(t *testing.T) {
	repoA := initRepo(t, "https://github.com/noamsto/app-a.git")
	repoB := initRepo(t, "https://github.com/noamsto/app-b.git")
	fileA := filepath.Join(repoA, "CLAUDE.md")
	fileB := filepath.Join(repoB, "CLAUDE.md")
	proposals := `[
	  {"id":"p-a","implicated_artifact":"` + fileA + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-b","implicated_artifact":"` + fileB + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}

	// Learn each entry's artifact-derived branch name with dedup off.
	base, err := Prepare(pf, "", DedupConfig{}, false)
	if err != nil {
		t.Fatal(err)
	}
	// branchByRepo and rootByID key on the resolved RepoRoot the closure receives,
	// which may differ from the raw initRepo path (symlink canonicalization).
	branchByRepo := map[string]string{}
	rootByID := map[string]string{}
	for _, e := range base {
		branchByRepo[e.RepoRoot] = e.BranchName
		rootByID[e.ProposalID] = e.RepoRoot
	}

	// Each repo reports an open PR on its OWN proposal's branch.
	cfg := DedupConfig{OpenPRsForRepo: func(root string) ([]PullRequest, error) {
		return []PullRequest{{Number: 1, HeadRefName: branchByRepo[root], URL: "u/" + root}}, nil
	}}
	plan, err := Prepare(pf, "", cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan {
		if e.Status != StatusDuplicate {
			t.Errorf("%s: status %q, want duplicate (its own target repo has the open PR)", e.ProposalID, e.Status)
		}
	}

	// Isolation: only repo A has an open PR. A bug that reused A's PR list for every
	// proposal would mark p-b duplicate too; per-repo dedup leaves p-b ready.
	rootA := rootByID["p-a"]
	cfg2 := DedupConfig{OpenPRsForRepo: func(root string) ([]PullRequest, error) {
		if root == rootA {
			return []PullRequest{{Number: 1, HeadRefName: branchByRepo[rootA], URL: "u"}}, nil
		}
		return nil, nil // repo B has no open PRs
	}}
	plan2, err := Prepare(pf, "", cfg2, false)
	if err != nil {
		t.Fatal(err)
	}
	got := statusByID(plan2)
	if got["p-a"].Status != StatusDuplicate {
		t.Errorf("p-a: status %q, want duplicate (repo A has the open PR)", got["p-a"].Status)
	}
	if got["p-b"].Status != StatusReady {
		t.Errorf("p-b: status %q, want ready (repo B has no open PR — A's list must not leak)", got["p-b"].Status)
	}
}

func TestPrepareDedupFailsOpen(t *testing.T) {
	repo := initRepo(t, "https://github.com/noamsto/app-a.git")
	file := filepath.Join(repo, "CLAUDE.md")
	proposals := `[
	  {"id":"p-a","implicated_artifact":"` + file + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DedupConfig{OpenPRsForRepo: func(root string) ([]PullRequest, error) {
		return nil, fmt.Errorf("no gh remote")
	}}
	plan, err := Prepare(pf, "", cfg, false)
	if err != nil {
		t.Fatalf("dedup must fail open, not error: %v", err)
	}
	if plan[0].Status != StatusReady {
		t.Errorf("status = %q, want ready (a repo we can't query never blocks)", plan[0].Status)
	}
}
