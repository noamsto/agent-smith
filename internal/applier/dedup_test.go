package applier

import (
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
		content += outcomePlaceholder + "\n"
	} else {
		content += "Merged; behavior improved.\n"
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

	listPRs := func() ([]PullRequest, error) {
		return []PullRequest{{Number: 12, Title: "t", HeadRefName: branch, URL: "https://github.com/noamsto/nix-config/pull/12"}}, nil
	}
	plan, err := Prepare(pf, "", DedupConfig{ListOpenPRs: listPRs}, false)
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

	listPRs := func() ([]PullRequest, error) {
		return []PullRequest{{Number: 99, HeadRefName: "docs/agent-smith-unrelated", URL: "u"}}, nil
	}
	plan, err := Prepare(pf, "", DedupConfig{ListOpenPRs: listPRs, ReasonLogDir: rl}, false)
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
