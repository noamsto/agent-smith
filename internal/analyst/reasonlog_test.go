package analyst

import (
	"os"
	"path/filepath"
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
