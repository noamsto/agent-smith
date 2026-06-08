package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func entryWithPR(id, prURL string) string {
	return "# " + id + "\n\n" +
		"**Artifact:** /repo/CLAUDE.md  \n" +
		"**Signal:** tool_error  \n" +
		"**Fix type:** add  **Confidence:** high  **Date:** 2026-06-07\n\n" +
		"## Diagnosis\n\nd\n\n**PR:** " + prURL + "\n\n<!-- outcome: open -->\n"
}

func TestReconcileUpdatesMatchingEntry(t *testing.T) {
	dir := t.TempDir()
	merged := filepath.Join(dir, "a.md")
	closed := filepath.Join(dir, "b.md")
	untouched := filepath.Join(dir, "c.md")
	if err := os.WriteFile(merged, []byte(entryWithPR("a", "https://github.com/x/y/pull/1")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(closed, []byte(entryWithPR("b", "https://github.com/x/y/pull/2")), 0o644); err != nil {
		t.Fatal(err)
	}
	// No matching status → stays open.
	if err := os.WriteFile(untouched, []byte(entryWithPR("c", "https://github.com/x/y/pull/3")), 0o644); err != nil {
		t.Fatal(err)
	}

	statuses := []PRStatus{
		{URL: "https://github.com/x/y/pull/1", State: "MERGED"},
		{URL: "https://github.com/x/y/pull/2", State: "CLOSED"},
	}
	n, err := Reconcile(dir, statuses)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 updates, got %d", n)
	}

	assertOutcome(t, merged, "merged")
	assertOutcome(t, closed, "closed")
	assertOutcome(t, untouched, "open")

	// Idempotent: a second pass changes nothing.
	if n, _ := Reconcile(dir, statuses); n != 0 {
		t.Errorf("expected idempotent reconcile, got %d updates", n)
	}
}

func assertOutcome(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<!-- outcome: "+want+" -->") {
		t.Errorf("%s: expected outcome %q in:\n%s", filepath.Base(path), want, data)
	}
}

func TestEntryReposDistinct(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte(entryWithPR("a", "https://github.com/noamsto/lazytmux/pull/1")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte(entryWithPR("b", "https://github.com/noamsto/lazytmux/pull/2")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c.md"), []byte(entryWithPR("c", "https://github.com/noamsto/agent-smith/pull/9")), 0o644); err != nil {
		t.Fatal(err)
	}
	// An entry with no PR yet must not contribute a repo.
	if err := os.WriteFile(filepath.Join(dir, "d.md"), []byte("# d\n\n<!-- outcome: open -->\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repos, err := EntryRepos(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 distinct repos, got %v", repos)
	}
}
