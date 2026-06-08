package analyst

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadProposalsValidatesDedupsAndRejects(t *testing.T) {
	dir := t.TempDir()
	good := `{"id":"glitch-skeleton","implicated_artifact":"/g/CLAUDE.md#reading-code",
	  "fix_type":"strengthen","evidence":["s1:1","s2:1","s3:1","≥3 sessions"],
	  "diagnosis":"rule ignored","proposed_change":"make imperative","confidence":"high",
	  "reason_log":"expected fewer whole-file reads"}`
	writeJSON(t, filepath.Join(dir, "a.json"), good)
	writeJSON(t, filepath.Join(dir, "b.json"), good)         // duplicate id → deduped
	writeJSON(t, filepath.Join(dir, "c.json"), `{"id":"x"}`) // invalid → rejected
	writeJSON(t, filepath.Join(dir, "d.json"), `{not json`)  // malformed → rejected

	props, errs := LoadProposals(dir)
	if len(props) != 1 {
		t.Fatalf("expected 1 deduped valid proposal, got %d", len(props))
	}
	if props[0].FixType != "strengthen" {
		t.Errorf("fix_type = %q", props[0].FixType)
	}
	if len(errs) != 2 { // c.json (invalid) + d.json (malformed)
		t.Errorf("expected 2 rejected inputs, got %d: %v", len(errs), errs)
	}
}

func TestWriteProposalsAndReasonLogs(t *testing.T) {
	props := []Proposal{{
		ID: "glitch-skeleton", ImplicatedArtifact: "/g/CLAUDE.md#reading-code",
		FixType: "strengthen", Evidence: []string{"s1:1", "≥3 sessions"},
		Diagnosis: "rule ignored", ProposedChange: "make imperative",
		Confidence: "high", ReasonLog: "fewer whole-file reads",
	}}
	dir := t.TempDir()
	propsPath := filepath.Join(dir, "proposals.json")
	if err := WriteProposals(props, propsPath); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(propsPath)
	var round []Proposal
	if err := json.Unmarshal(data, &round); err != nil || len(round) != 1 {
		t.Fatalf("proposals.json round-trip failed: %v rows=%d", err, len(round))
	}

	rlDir := filepath.Join(dir, "reason-log")
	n, err := WriteReasonLogs(props, rlDir, "2026-06-01")
	if err != nil || n != 1 {
		t.Fatalf("WriteReasonLogs: n=%d err=%v", n, err)
	}
	entry, _ := os.ReadFile(filepath.Join(rlDir, "2026-06-01-glitch-skeleton.md"))
	if !strings.Contains(string(entry), "make imperative") ||
		!strings.Contains(string(entry), "## Diagnosis") {
		t.Errorf("reason-log missing content:\n%s", entry)
	}
	// Append-only: a second run must NOT overwrite an existing entry, even if the
	// proposal content changed.
	mutated := []Proposal{props[0]}
	mutated[0].ProposedChange = "SHOULD NOT APPEAR"
	n2, _ := WriteReasonLogs(mutated, rlDir, "2026-06-01")
	if n2 != 0 {
		t.Errorf("expected append-only (0 new), got %d", n2)
	}
	again, _ := os.ReadFile(filepath.Join(rlDir, "2026-06-01-glitch-skeleton.md"))
	if strings.Contains(string(again), "SHOULD NOT APPEAR") {
		t.Errorf("append-only violated: existing reason-log was overwritten")
	}
}

func TestSkipProposalValidatesWithoutProposedChange(t *testing.T) {
	dir := t.TempDir()
	skip := `{"id":"glitch-confirm-irreversible","implicated_artifact":"/g/CLAUDE.md#git",
	  "fix_type":"skip","evidence":["s1:1","s2:1","s3:1","≥3 sessions"],
	  "diagnosis":"harness already confirms destructive git","proposed_change":"",
	  "confidence":"high","reason_log":"skipped: already handled by harness — system prompt confirms irreversible actions"}`
	writeJSON(t, filepath.Join(dir, "p.json"), skip)
	props, errs := LoadProposals(dir)
	if len(errs) != 0 {
		t.Fatalf("expected skip proposal to validate, got errors: %v", errs)
	}
	if len(props) != 1 || props[0].FixType != "skip" {
		t.Fatalf("expected 1 skip proposal, got %+v", props)
	}
}

func TestLoadProposalsToleratesCodeFences(t *testing.T) {
	dir := t.TempDir()
	fenced := "```json\n" + `{"id":"glitch-fenced","implicated_artifact":"/g/CLAUDE.md#x",
	  "fix_type":"strengthen","evidence":["s1:1","s2:1","s3:1"],"diagnosis":"d",
	  "proposed_change":"c","confidence":"high","reason_log":"r"}` + "\n```\n"
	writeJSON(t, filepath.Join(dir, "p.json"), fenced)
	props, errs := LoadProposals(dir)
	if len(errs) != 0 {
		t.Fatalf("expected fenced JSON to parse, got errors: %v", errs)
	}
	if len(props) != 1 || props[0].ID != "glitch-fenced" {
		t.Fatalf("expected 1 proposal glitch-fenced, got %+v", props)
	}
}
