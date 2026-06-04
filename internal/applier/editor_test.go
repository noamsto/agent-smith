package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorAgentContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "agents", "editor.md"))
	if err != nil {
		t.Fatalf("read agents/editor.md: %v", err)
	}
	p := string(b)
	for _, must := range []string{
		"applied", "files_changed", "summary", "reason", // output schema
		"escalate-out-of-instructions", // handles the hook case
		"settings.json",                // two-layer rule
		"/nix/store",                   // overlay rule
		"decline",                      // may refuse on drift
	} {
		if !strings.Contains(p, must) {
			t.Errorf("editor prompt missing %q", must)
		}
	}
}
