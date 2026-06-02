package analyst

import "testing"

func TestOraclePromptEmbedsTheGuard(t *testing.T) {
	p := OraclePrompt()
	if len(p) < 500 {
		t.Fatalf("oracle prompt looks too short (%d bytes) — embed failed?", len(p))
	}
	for _, must := range []string{
		"MUST NOT choose `add`", // the acceptance-bar guard
		"escalate-out-of-instructions",
		"Output valid JSON only",
		"artifact_content",
	} {
		if !contains(p, must) {
			t.Errorf("oracle prompt missing required text: %q", must)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
