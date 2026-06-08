package extractor

import (
	"os"
	"strings"
	"time"
)

// MarkerPath is where the last-run timestamp is persisted: alongside the db, as
// "<OutDB>.last-run". Mining defaults its lower bound to this marker so a re-run
// (deja-vu) only processes sessions newer than the previous run.
func MarkerPath(outDB string) string {
	return outDB + ".last-run"
}

// ReadMarker returns the ISO8601 timestamp of the last run, or "" if no marker
// exists yet (a first run mines the whole corpus).
func ReadMarker(outDB string) string {
	data, err := os.ReadFile(MarkerPath(outDB))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteMarker records ts as the last-run marker for outDB.
func WriteMarker(outDB string, ts time.Time) error {
	return os.WriteFile(MarkerPath(outDB), []byte(ts.UTC().Format(time.RFC3339)+"\n"), 0o644)
}
