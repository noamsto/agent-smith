package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"text/template"
)

func TestRunDuckDBSmoke(t *testing.T) {
	out, err := runDuckDB(context.Background(), ":memory:", "SELECT 41 + 1 AS answer;")
	if err != nil {
		t.Fatalf("runDuckDB: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("expected 42 in output, got: %q", out)
	}
}

// testConfig returns a Config pointing at a testdata fixture dir and a temp db.
func testConfig(t *testing.T, fixtureDir string, signals ...string) Config {
	t.Helper()
	cfg := DefaultConfig()
	cfg.CorpusGlob = filepath.Join("testdata", fixtureDir, "*.jsonl")
	cfg.OutDB = filepath.Join(t.TempDir(), "incidents.db")
	cfg.Signals = signals
	cfg.GlobalClaudeMd = "/home/noams/.claude/CLAUDE.md"
	cfg.AgentsDir = "/agents"
	return cfg
}

// query runs a SQL query against db with -json and decodes the result rows.
func query(t *testing.T, db, sql string) []map[string]any {
	t.Helper()
	out, err := exec.Command(duckDBBin(), "-json", db, "-c", sql).Output()
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	if len(out) == 0 {
		return nil
	}
	var rows []map[string]any
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("decode %q: %v\noutput: %s", sql, err, out)
	}
	return rows
}

// TestBasePipelineBuildsDerivedTables renders ONLY the base template (no detector
// files exist yet) and confirms it loads and creates the incidents table.
func TestBasePipelineBuildsDerivedTables(t *testing.T) {
	cfg := testConfig(t, "base")
	tmpl, err := template.New("sql").ParseFS(sqlFS, "sql/*.sql.tmpl")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "00_base.sql.tmpl", cfg); err != nil {
		t.Fatalf("render base: %v", err)
	}
	if _, err := runDuckDB(context.Background(), cfg.OutDB, buf.String()); err != nil {
		t.Fatalf("run base: %v", err)
	}
	rows := query(t, cfg.OutDB, "SELECT count(*) AS n FROM incidents;")
	if len(rows) != 1 {
		t.Fatalf("expected 1 count row, got %d", len(rows))
	}
}
