package analyst

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEntry(t *testing.T, dir, name, artifact, signal, outcome string) {
	t.Helper()
	content := "# " + name + "\n\n" +
		"**Artifact:** " + artifact + "  \n" +
		"**Signal:** " + signal + "  \n" +
		"**Fix type:** add  **Confidence:** high  **Date:** 2026-06-07\n\n" +
		"## Diagnosis\n\nd\n\n**PR:** https://github.com/x/y/pull/1\n\n" +
		"<!-- outcome: " + outcome + " -->\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFilterRejectedSkipsClosedClusters(t *testing.T) {
	dir := t.TempDir()
	writeEntry(t, dir, "closed-one", "/repo/CLAUDE.md#x", "tool_error", OutcomeClosed)
	writeEntry(t, dir, "open-one", "/repo/OTHER.md", "inefficiency", OutcomeOpen)

	entries, err := ReadEntries(dir)
	if err != nil {
		t.Fatal(err)
	}

	clusters := []Cluster{
		{ClusterID: "tool_error::/repo/CLAUDE.md", SignalType: "tool_error", Artifact: "/repo/CLAUDE.md"},
		{ClusterID: "inefficiency::/repo/OTHER.md", SignalType: "inefficiency", Artifact: "/repo/OTHER.md"},
		{ClusterID: "user_correction::/repo/CLAUDE.md", SignalType: "user_correction", Artifact: "/repo/CLAUDE.md"},
	}
	kept, skipped := FilterRejected(clusters, entries)

	if len(skipped) != 1 || skipped[0].ClusterID != "tool_error::/repo/CLAUDE.md" {
		t.Fatalf("expected the closed tool_error cluster skipped, got %+v", skipped)
	}
	if len(kept) != 2 {
		t.Fatalf("expected 2 kept clusters, got %d", len(kept))
	}
	// The open entry must not skip its cluster; a different signal on the same
	// artifact must not be skipped either (key is artifact+signal, not artifact).
	for _, c := range kept {
		if c.ClusterID == "tool_error::/repo/CLAUDE.md" {
			t.Error("closed cluster leaked into kept")
		}
	}
}

func TestFilterRejectedMatchesAcrossSectionSuffix(t *testing.T) {
	dir := t.TempDir()
	// Reason-log records "path#section"; the cluster artifact is the bare path.
	writeEntry(t, dir, "closed", "/repo/CLAUDE.md#reading-code", "inefficiency", OutcomeRejected)
	entries, err := ReadEntries(dir)
	if err != nil {
		t.Fatal(err)
	}
	clusters := []Cluster{{ClusterID: "inefficiency::/repo/CLAUDE.md", SignalType: "inefficiency", Artifact: "/repo/CLAUDE.md"}}
	kept, skipped := FilterRejected(clusters, entries)
	if len(kept) != 0 || len(skipped) != 1 {
		t.Fatalf("section suffix should still match: kept=%d skipped=%d", len(kept), len(skipped))
	}
}

func TestReadEntriesNoDir(t *testing.T) {
	entries, err := ReadEntries(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no entries, got %d", len(entries))
	}
}

func TestSetOutcome(t *testing.T) {
	content := "# x\n\n<!-- outcome: open -->\n"
	got, changed := SetOutcome(content, OutcomeClosed)
	if !changed {
		t.Fatal("expected a change")
	}
	if got != "# x\n\n<!-- outcome: closed -->\n" {
		t.Fatalf("unexpected rewrite: %q", got)
	}
	if _, changed := SetOutcome("# x\n\nno marker\n", OutcomeMerged); changed {
		t.Error("expected no change when marker absent")
	}
}

// writeRawEntry writes an entry body verbatim, so a test can reproduce a legacy
// on-disk shape the current writer no longer emits.
func writeRawEntry(t *testing.T, dir, name, tail string) string {
	t.Helper()
	path := filepath.Join(dir, name+".md")
	content := "# " + name + "\n\n" +
		"**Artifact:** /repo/CLAUDE.md  \n" +
		"**Signal:** tool_error  \n\n" +
		"## Diagnosis\n\nd\n\n" + tail
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOutcomeReadsLegacyMarkerAsOpen(t *testing.T) {
	if got := Outcome("body\n\n" + legacyOutcomeMarker + "\n"); got != OutcomeOpen {
		t.Fatalf("legacy marker: got %q, want %q", got, OutcomeOpen)
	}
	if got := Outcome("body\n\n" + PRPlaceholder + "\n"); got != "" {
		t.Fatalf("PR placeholder must not read as an outcome, got %q", got)
	}
}

func TestSetOutcomeNormalizesLegacyMarker(t *testing.T) {
	got, changed := SetOutcome("body\n\n"+legacyOutcomeMarker+"\n", OutcomeMerged)
	if !changed {
		t.Fatal("expected the legacy marker to be treated as settable")
	}
	if want := "body\n\n<!-- outcome: merged -->\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMigrateOutcomeMarkers(t *testing.T) {
	dir := t.TempDir()
	legacy := writeRawEntry(t, dir, "legacy", "**PR:** https://github.com/x/y/pull/1\n\n"+legacyOutcomeMarker+"\n")
	unapplied := writeRawEntry(t, dir, "unapplied", PRPlaceholder+"\n")
	writeEntry(t, dir, "current", "/repo/CLAUDE.md", "tool_error", OutcomeMerged)
	currentBefore, err := os.ReadFile(filepath.Join(dir, "current.md"))
	if err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateOutcomeMarkers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if migrated != 2 {
		t.Fatalf("migrated %d entries, want 2", migrated)
	}

	for _, tc := range []struct{ name, path string }{{"legacy", legacy}, {"unapplied", unapplied}} {
		data, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := Outcome(string(data)); got != OutcomeOpen {
			t.Fatalf("%s: outcome %q, want %q", tc.name, got, OutcomeOpen)
		}
		if strings.Contains(string(data), legacyOutcomeMarker) {
			t.Fatalf("%s: legacy marker survived migration", tc.name)
		}
	}

	// The un-applied entry still needs its PR slot for a later applier run.
	data, err := os.ReadFile(unapplied)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), PRPlaceholder) {
		t.Fatal("unapplied: PR placeholder must survive migration")
	}

	currentAfter, err := os.ReadFile(filepath.Join(dir, "current.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(currentAfter) != string(currentBefore) {
		t.Fatal("an entry with a canonical marker must be left untouched")
	}

	// Migration is idempotent: a second pass rewrites nothing.
	again, err := MigrateOutcomeMarkers(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("second pass migrated %d entries, want 0", again)
	}
}

func TestMigrateOutcomeMarkersMissingDir(t *testing.T) {
	migrated, err := MigrateOutcomeMarkers(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatal(err)
	}
	if migrated != 0 {
		t.Fatalf("migrated %d entries from a missing dir, want 0", migrated)
	}
}
