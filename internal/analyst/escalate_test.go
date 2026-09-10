package analyst

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const unroutableProposal = `{"id":"glitch-extractor-fp","implicated_artifact":"/g/CLAUDE.md#reading-code",
  "signal_type":"false-positive","fix_type":"escalate-out-of-instructions",
  "evidence":["s1:1","s2:1","s3:1"],"diagnosis":"the extractor SQL matches skeleton reads",
  "proposed_change":"tighten the SQL predicate","confidence":"high",
  "reason_log":"expected the cluster to disappear"}`

func TestLoadProposalsIgnoresVerdicts(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "p-0.json"), unroutableProposal)
	writeJSON(t, filepath.Join(dir, "v-0.json"),
		`{"proposal_id":"glitch-extractor-fp","verdict":"unroutable","reason":"verifies on disk"}`)

	props, errs := LoadProposals(dir)
	if len(props) != 1 || len(errs) != 0 {
		t.Fatalf("got %d proposals, %d errors: %v", len(props), len(errs), errs)
	}
}

func TestSplitUnroutable(t *testing.T) {
	props := []Proposal{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	verdicts := map[string]Verdict{
		"a": {ProposalID: "a", Verdict: VerdictUpheld},
		"b": {ProposalID: "b", Verdict: VerdictUnroutable},
	}
	routable, unroutable := SplitUnroutable(props, verdicts)
	if len(routable) != 2 || routable[0].ID != "a" || routable[1].ID != "c" {
		t.Errorf("routable = %v", routable)
	}
	if len(unroutable) != 1 || unroutable[0].ID != "b" {
		t.Errorf("unroutable = %v", unroutable)
	}
}

// fakeFiler records the issues it was asked to file.
type fakeFiler struct {
	titles []string
	bodies []string
}

func (f *fakeFiler) file(title, body string) (string, error) {
	f.titles = append(f.titles, title)
	f.bodies = append(f.bodies, body)
	return "https://github.com/noamsto/agent-smith/issues/99", nil
}

func loadUnroutable(t *testing.T) []Proposal {
	t.Helper()
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "p-0.json"), unroutableProposal)
	writeJSON(t, filepath.Join(dir, "v-0.json"),
		`{"proposal_id":"glitch-extractor-fp","verdict":"unroutable","reason":"verifies on disk"}`)
	props, _ := LoadProposals(dir)
	_, unroutable := SplitUnroutable(props, LoadVerdicts(dir))
	if len(unroutable) != 1 {
		t.Fatalf("expected 1 unroutable proposal, got %d", len(unroutable))
	}
	return unroutable
}

func TestEscalateFilesIssueAndRecordsIt(t *testing.T) {
	props := loadUnroutable(t)
	logDir := t.TempDir()
	f := &fakeFiler{}

	escs, err := Escalate(props, map[string]Verdict{
		"glitch-extractor-fp": {Verdict: VerdictUnroutable, Reason: "verifies on disk"},
	}, logDir, "2026-09-10", f.file)
	if err != nil {
		t.Fatal(err)
	}
	if len(escs) != 1 || escs[0].IssueURL == "" {
		t.Fatalf("escalations = %+v", escs)
	}
	if len(f.titles) != 1 || !strings.Contains(f.titles[0], "glitch-extractor-fp") {
		t.Errorf("titles = %v", f.titles)
	}
	if !strings.Contains(f.bodies[0], "verifies on disk") {
		t.Errorf("body missing skeptic reason: %s", f.bodies[0])
	}

	entries, err := ReadEntries(logDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries = %v, err = %v", entries, err)
	}
	body, err := os.ReadFile(entries[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "**Issue:** "+escs[0].IssueURL) {
		t.Errorf("reason-log entry missing issue link:\n%s", body)
	}
}

func TestEscalateDedupsAgainstReasonLog(t *testing.T) {
	props := loadUnroutable(t)
	logDir := t.TempDir()
	f := &fakeFiler{}
	verdicts := map[string]Verdict{"glitch-extractor-fp": {Verdict: VerdictUnroutable}}

	if _, err := Escalate(props, verdicts, logDir, "2026-09-10", f.file); err != nil {
		t.Fatal(err)
	}
	// A later run re-derives the same finding under a different proposal id.
	props[0].ID = "glitch-extractor-fp-2"
	escs, err := Escalate(props, verdicts, logDir, "2026-09-11", f.file)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.titles) != 1 {
		t.Errorf("filed %d issues, want 1: %v", len(f.titles), f.titles)
	}
	if escs[0].Skipped == "" {
		t.Errorf("expected a skip reason, got %+v", escs[0])
	}
}

func TestEscalateWithoutFilerSkips(t *testing.T) {
	props := loadUnroutable(t)
	logDir := t.TempDir()
	escs, err := Escalate(props, map[string]Verdict{}, logDir, "2026-09-10", nil)
	if err != nil {
		t.Fatal(err)
	}
	if escs[0].Skipped != "issue filing disabled" {
		t.Errorf("escalation = %+v", escs[0])
	}
	if entries, _ := ReadEntries(logDir); len(entries) != 0 {
		t.Errorf("wrote %d reason-log entries with filing disabled", len(entries))
	}
}
