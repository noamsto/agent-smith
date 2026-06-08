package extractor

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMarkerRoundTrip(t *testing.T) {
	db := filepath.Join(t.TempDir(), "incidents.db")

	// No marker yet → empty (a first run mines everything).
	if got := ReadMarker(db); got != "" {
		t.Fatalf("expected empty marker, got %q", got)
	}

	ts := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	if err := WriteMarker(db, ts); err != nil {
		t.Fatal(err)
	}
	got := ReadMarker(db)
	want := ts.Format(time.RFC3339)
	if got != want {
		t.Fatalf("marker round-trip: got %q want %q", got, want)
	}
	if MarkerPath(db) != db+".last-run" {
		t.Errorf("marker path = %q", MarkerPath(db))
	}
}
