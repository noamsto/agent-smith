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
