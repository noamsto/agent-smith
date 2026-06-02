package applier

import (
	"strings"
	"testing"
)

func TestEditorPromptContract(t *testing.T) {
	p := EditorPrompt()
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
