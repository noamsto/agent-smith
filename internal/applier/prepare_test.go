package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareStatuses(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	existing := filepath.Join(root, "CLAUDE.md")
	missing := filepath.Join(root, "AGENTS.md") // in-repo but not present

	proposals := `[
	  {"id":"p-strengthen","implicated_artifact":"` + existing + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-add-missing","implicated_artifact":"` + missing + `","fix_type":"add",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-strengthen-missing","implicated_artifact":"` + missing + `","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-unresolved","implicated_artifact":"/nonexistent-xyz/C.md","fix_type":"add",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-skip","implicated_artifact":"` + existing + `#x","fix_type":"skip",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"","confidence":"high","reason_log":"skipped: already handled by harness"},
	  {"id":"p-low","implicated_artifact":"` + existing + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"low","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Prepare(pf, "", DedupConfig{}, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range plan {
		got[e.ProposalID] = e.Status
	}
	want := map[string]string{
		"p-add-missing":        StatusReady,
		"p-strengthen":         StatusReady,
		"p-strengthen-missing": StatusMissingFile,
		"p-unresolved":         StatusUnresolved,
		"p-skip":               StatusDeclined,
		"p-low":                StatusLowConfidence,
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: status = %q, want %q", id, got[id], w)
		}
	}

	// --include-low-confidence override keeps the low proposal actionable.
	planInc, err := Prepare(pf, "", DedupConfig{}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range planInc {
		if e.ProposalID == "p-low" && e.Status != StatusReady {
			t.Errorf("p-low with includeLowConfidence: status = %q, want %q", e.Status, StatusReady)
		}
	}
	// deterministic sort by ProposalID
	if plan[0].ProposalID != "p-add-missing" {
		t.Errorf("plan not sorted by id: first = %q", plan[0].ProposalID)
	}
}

func TestPrepareGroupsByArtifact(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	claude := filepath.Join(root, "CLAUDE.md")
	other := filepath.Join(root, "AGENTS.md")

	proposals := `[
	  {"id":"p-claude-a","implicated_artifact":"` + claude + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-claude-b","implicated_artifact":"` + claude + `#y","fix_type":"add",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-other","implicated_artifact":"` + other + `","fix_type":"add",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(pf, "", DedupConfig{}, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]PlanEntry{}
	for _, e := range plan {
		byID[e.ProposalID] = e
	}
	// Same artifact → same group + branch; the branch is artifact-derived, not id-derived.
	a, b := byID["p-claude-a"], byID["p-claude-b"]
	if a.GroupID == "" || a.GroupID != b.GroupID {
		t.Errorf("same-artifact proposals not grouped: %q vs %q", a.GroupID, b.GroupID)
	}
	if a.BranchName != b.BranchName {
		t.Errorf("grouped proposals on different branches: %q vs %q", a.BranchName, b.BranchName)
	}
	if a.BranchName == "docs/agent-smith-p-claude-a" {
		t.Errorf("branch is still id-derived: %q", a.BranchName)
	}
	// Different artifact → distinct group.
	if byID["p-other"].GroupID == a.GroupID {
		t.Errorf("different-artifact proposal landed in the same group %q", a.GroupID)
	}
	// FindGroup returns both members in id order; ReadyGroupIDs lists each group once.
	group, err := FindGroup(plan, a.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	if len(group) != 2 || group[0].ProposalID != "p-claude-a" {
		t.Errorf("FindGroup = %+v", group)
	}
	if len(ReadyGroupIDs(plan)) != 2 {
		t.Errorf("ReadyGroupIDs = %v, want 2 groups", ReadyGroupIDs(plan))
	}
}

func TestPrepareEscalationRoutesToSettingsRepo(t *testing.T) {
	implicated := initRepo(t, "https://github.com/noamsto/some-app.git")
	settings := initRepo(t, "https://github.com/noamsto/nix-config.git")

	proposals := `[
	  {"id":"p-escalate","implicated_artifact":"` + filepath.Join(implicated, "CLAUDE.md") + `","fix_type":"escalate-out-of-instructions",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"add a hook","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Prepare(pf, settings, DedupConfig{}, false)
	if err != nil {
		t.Fatal(err)
	}
	e, err := FindEntry(plan, "p-escalate")
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != StatusReady {
		t.Fatalf("status = %q, want %q", e.Status, StatusReady)
	}
	wantRoot, err := filepath.EvalSymlinks(settings)
	if err != nil {
		t.Fatal(err)
	}
	if e.RepoRoot != wantRoot {
		t.Errorf("RepoRoot = %q, want settings repo %q", e.RepoRoot, wantRoot)
	}
	if e.Owner != "nix-config" {
		t.Errorf("Owner = %q, want nix-config", e.Owner)
	}
	if !strings.HasPrefix(e.BranchName, "chore/agent-smith-") {
		t.Errorf("BranchName = %q, want chore/agent-smith-<artifact-slug>", e.BranchName)
	}
	if e.BranchName == "chore/agent-smith-p-escalate" {
		t.Errorf("branch is id-derived, want artifact-derived: %q", e.BranchName)
	}
	if e.FilePath != filepath.Join(wantRoot, settingsFileRel) {
		t.Errorf("FilePath = %q, want %q", e.FilePath, filepath.Join(wantRoot, settingsFileRel))
	}
}

func TestPrepareEscalationNoSettingsRepoUnrouted(t *testing.T) {
	implicated := initRepo(t, "https://github.com/noamsto/some-app.git")
	proposals := `[
	  {"id":"p-escalate","implicated_artifact":"` + filepath.Join(implicated, "CLAUDE.md") + `","fix_type":"escalate-out-of-instructions",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"add a hook","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Prepare(pf, "", DedupConfig{}, false)
	if err != nil {
		t.Fatal(err)
	}
	e, err := FindEntry(plan, "p-escalate")
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != StatusUnrouted {
		t.Errorf("status = %q, want %q", e.Status, StatusUnrouted)
	}
	if e.Reason == "" {
		t.Error("expected a routing reason on an unrouted escalation")
	}
	if e.RepoRoot != "" {
		t.Errorf("RepoRoot = %q, want empty (not dispatched at the implicated repo)", e.RepoRoot)
	}
}

func TestPrepareEscalationSettingsRepoUnresolvableUnrouted(t *testing.T) {
	proposals := `[
	  {"id":"p-escalate","implicated_artifact":"/somewhere/CLAUDE.md","fix_type":"escalate-out-of-instructions",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"add a hook","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Prepare(pf, t.TempDir(), DedupConfig{}, false) // a dir that is not a git repo
	if err != nil {
		t.Fatal(err)
	}
	e, err := FindEntry(plan, "p-escalate")
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != StatusUnrouted {
		t.Errorf("status = %q, want %q", e.Status, StatusUnrouted)
	}
	if e.Reason == "" {
		t.Error("expected a reason when the settings repo is not a git repo")
	}
}

func TestPlanRoundTrip(t *testing.T) {
	plan := []PlanEntry{{ProposalID: "p1", RepoRoot: "/r", FilePath: "/r/C.md", Status: StatusReady}}
	path := filepath.Join(t.TempDir(), "apply-plan.json")
	if err := WritePlan(plan, path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadPlan(path)
	if err != nil {
		t.Fatalf("ReadPlan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].ProposalID != "p1" {
		t.Errorf("ProposalID = %q, want %q", got[0].ProposalID, "p1")
	}
	if got[0].Status != StatusReady {
		t.Errorf("Status = %q, want %q", got[0].Status, StatusReady)
	}
	if got[0].FilePath != "/r/C.md" {
		t.Errorf("FilePath = %q, want %q", got[0].FilePath, "/r/C.md")
	}
	if _, err := FindEntry(got, "p1"); err != nil {
		t.Errorf("FindEntry: %v", err)
	}
	if _, err := FindEntry(got, "nope"); err == nil {
		t.Error("FindEntry should error on unknown id")
	}
}
