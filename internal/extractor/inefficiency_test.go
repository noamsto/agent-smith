package extractor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestInefficiencyFlagsWholeFileLargeRead(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CorpusGlob = filepath.Join("..", "..", "fixtures", "skeleton-first", "*.jsonl")
	cfg.OutDB = filepath.Join(t.TempDir(), "incidents.db")
	cfg.Signals = []string{"inefficiency"}
	cfg.GlobalClaudeMd = "/home/noams/.claude/CLAUDE.md"

	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	rows := query(t, cfg.OutDB,
		"SELECT session_id, confidence, implicated_artifact, candidates::VARCHAR AS cands, detail::VARCHAR AS detail FROM incidents WHERE signal_type='inefficiency';")

	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 inefficiency incident (the violation), got %d: %v", len(rows), rows)
	}
	r := rows[0]
	if r["session_id"] != "viol-1" {
		t.Errorf("expected incident in session viol-1, got %v", r["session_id"])
	}
	if r["confidence"] != "high" { // totalLines 1200 >= HighLines 1000
		t.Errorf("expected high confidence, got %v", r["confidence"])
	}
	// Acceptance: the global CLAUDE.md must be among candidate artifacts so the
	// analyst can trace this to the existing skeleton-first rule.
	if !strings.Contains(r["cands"].(string), "/home/noams/.claude/CLAUDE.md") {
		t.Errorf("expected global CLAUDE.md in candidates, got %v", r["cands"])
	}
	if !strings.Contains(r["detail"].(string), "server.go") {
		t.Errorf("expected file_path in detail, got %v", r["detail"])
	}
}

func TestInefficiencyWindowIsPopulated(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CorpusGlob = filepath.Join("..", "..", "fixtures", "skeleton-first", "*.jsonl")
	cfg.OutDB = filepath.Join(t.TempDir(), "incidents.db")
	cfg.Signals = []string{"inefficiency"}
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := query(t, cfg.OutDB,
		`SELECT json_array_length("window") AS n FROM incidents WHERE signal_type='inefficiency';`)
	n := rows[0]["n"]
	if n == float64(0) || n == "0" {
		t.Fatalf("expected a non-empty window slice, got %v", n)
	}
}
