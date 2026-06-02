package applier

import (
	"os"
	"path/filepath"
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
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := Prepare(pf)
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
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: status = %q, want %q", id, got[id], w)
		}
	}
	// deterministic sort by ProposalID
	if plan[0].ProposalID != "p-add-missing" {
		t.Errorf("plan not sorted by id: first = %q", plan[0].ProposalID)
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
