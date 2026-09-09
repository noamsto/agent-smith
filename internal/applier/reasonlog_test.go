package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleEntry = `# glitch-skeleton

**Artifact:** /g/CLAUDE.md#reading-code
**Signal:** inefficiency
**Fix type:** strengthen  **Confidence:** high  **Date:** 2026-06-01

## Diagnosis

rule ignored

## Proposed change

` + "```" + `
make imperative
` + "```" + `

## Expected effect

fewer whole-file reads

<!-- PR link appended by the applier; outcome appended by deja-vu -->

<!-- outcome: open -->
`

func TestAppendPRLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-06-01-glitch-skeleton.md")
	if err := os.WriteFile(path, []byte(sampleEntry), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AppendPRLink(dir, "glitch-skeleton", "https://github.com/x/y/pull/7"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(got), "**PR:** https://github.com/x/y/pull/7") {
		t.Errorf("PR link not written:\n%s", got)
	}
	if !strings.Contains(string(got), "<!-- outcome: open -->") {
		t.Error("outcome marker not left behind")
	}
	if strings.Contains(string(got), "appended by the applier") {
		t.Error("applier placeholder should be consumed")
	}

	// Idempotent: a second call must not double-append.
	if err := AppendPRLink(dir, "glitch-skeleton", "https://github.com/x/y/pull/7"); err != nil {
		t.Fatal(err)
	}
	got2, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Count(string(got2), "**PR:**") != 1 {
		t.Errorf("PR link appended twice:\n%s", got2)
	}

	// Unknown id → error.
	if err := AppendPRLink(dir, "no-such-id", "url"); err == nil {
		t.Error("expected error for unknown proposal id")
	}

	// Heading present but placeholder already consumed/removed → error, not silent success.
	noPlaceholder := "# orphan\n\nsome body, no placeholder\n"
	p2 := filepath.Join(dir, "2026-06-01-orphan.md")
	if err := os.WriteFile(p2, []byte(noPlaceholder), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AppendPRLink(dir, "orphan", "https://github.com/x/y/pull/8"); err == nil {
		t.Error("expected error when heading matches but placeholder is absent")
	}
}

// A legacy entry carries the PR placeholder but no machine-readable outcome
// marker. Filling the PR link must leave one behind, or Reconcile can never
// stamp the outcome.
func TestAppendPRLinkAddsMissingOutcomeMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-06-01-glitch-legacy.md")
	legacy := "# glitch-legacy\n\n**Artifact:** /g/CLAUDE.md\n\n## Diagnosis\n\nd\n\n" + prPlaceholder + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AppendPRLink(dir, "glitch-legacy", "https://github.com/x/y/pull/9"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "**PR:** https://github.com/x/y/pull/9") {
		t.Errorf("PR link not written:\n%s", got)
	}
	if !strings.Contains(string(got), "<!-- outcome: open -->") {
		t.Errorf("outcome marker not added:\n%s", got)
	}

	// The reconcile path must now be able to stamp it.
	n, err := Reconcile(dir, []PRStatus{{URL: "https://github.com/x/y/pull/9", State: "MERGED"}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled %d entries, want 1", n)
	}
	got, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "<!-- outcome: merged -->") {
		t.Errorf("outcome not reconciled:\n%s", got)
	}
}

// Reconcile must also stamp an entry still carrying only the legacy prose marker,
// so a ledger that skipped the migration step still heals.
func TestReconcileStampsLegacyMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "2026-06-01-glitch-prose.md")
	entry := "# glitch-prose\n\n**PR:** https://github.com/x/y/pull/3\n\n" +
		"<!-- outcome appended by deja-vu -->\n"
	if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := Reconcile(dir, []PRStatus{{URL: "https://github.com/x/y/pull/3", State: "CLOSED"}})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled %d entries, want 1", n)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "<!-- outcome: closed -->") {
		t.Errorf("legacy marker not stamped:\n%s", got)
	}
}
