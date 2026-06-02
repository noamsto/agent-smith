package applier

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const prPlaceholder = "<!-- PR link appended by the applier; outcome appended by deja-vu -->"

// AppendPRLink fills the applier placeholder in the reason-log entry whose first
// heading is "# <id>" with the PR URL, leaving a deja-vu outcome marker. It scans
// by heading (not filename) so it is decoupled from the analyst's slug logic, and
// is idempotent — a second call with a PR line already present is a no-op.
func AppendPRLink(dir, id, prURL string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return fmt.Errorf("glob %s: %w", dir, err)
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.HasPrefix(content, "# "+id+"\n") {
			continue
		}
		if strings.Contains(content, "**PR:** ") {
			return nil // already linked
		}
		if !strings.Contains(content, prPlaceholder) {
			return fmt.Errorf("reason-log entry %q has no applier placeholder to fill", id)
		}
		repl := fmt.Sprintf("**PR:** %s\n\n<!-- outcome appended by deja-vu -->", prURL)
		content = strings.Replace(content, prPlaceholder, repl, 1)
		return os.WriteFile(path, []byte(content), 0o644)
	}
	return fmt.Errorf("no reason-log entry with heading %q in %s", id, dir)
}
