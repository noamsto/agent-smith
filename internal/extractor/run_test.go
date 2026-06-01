package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
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
	// Fixtures are tiny; the production memory_limit is sized for the 480MB real
	// corpus. Under parallel `go test` an explicit cap makes DuckDB eagerly
	// reserve against the shared cgroup and OOM, so let fixtures use the lazy
	// default. TestInvalidMemoryLimitRejected sets MemoryLimit back explicitly.
	cfg.MemoryLimit = ""
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

// jsonCount extracts an integer count from a decoded query row, tolerating the
// float64/string ambiguity in duckdb's -json output.
func jsonCount(t *testing.T, v any) int {
	t.Helper()
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, err := strconv.Atoi(n)
		if err != nil {
			t.Fatalf("count not an int: %q", n)
		}
		return i
	default:
		t.Fatalf("unexpected count type %T", v)
		return 0
	}
}

func TestIncidentIDIsIdempotent(t *testing.T) {
	db := filepath.Join(t.TempDir(), "incidents.db")
	ddl := `CREATE TABLE incidents (
	  incident_id VARCHAR PRIMARY KEY, session_id VARCHAR, project VARCHAR, ts VARCHAR,
	  signal_type VARCHAR, implicated_artifact VARCHAR, candidates JSON, "window" JSON,
	  confidence VARCHAR, detail JSON);`
	ins := `INSERT INTO incidents VALUES
	  (md5('s1:3:inefficiency'),'s1','/p','2026-05-01T10:00:00Z','inefficiency',
	   '/c.md', '["a"]'::JSON, '[]'::JSON, 'high', '{}'::JSON)
	  ON CONFLICT (incident_id) DO NOTHING;`
	ctx := context.Background()
	if _, err := runDuckDB(ctx, db, ddl+ins); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := runDuckDB(ctx, db, ins); err != nil { // re-run same insert
		t.Fatalf("second insert: %v", err)
	}
	rows := query(t, db, "SELECT count(*) AS n FROM incidents;")
	if n := jsonCount(t, rows[0]["n"]); n != 1 {
		t.Fatalf("expected 1 incident after duplicate insert, got %d", n)
	}
}

func TestInvalidMemoryLimitRejected(t *testing.T) {
	cfg := testConfig(t, "base", "inefficiency")
	cfg.MemoryLimit = "4GB'; DROP TABLE incidents; --"
	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("expected invalid memory_limit to be rejected, got nil error")
	}
}

// TestBasePipelineBuildsDerivedTables renders ONLY the base template (no detector
// files exist yet) and confirms it loads the corpus into the derived tables.
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
	// rec is a TEMP table that only lives within a single duckdb invocation, so
	// probe it in the same script that builds the pipeline (JSON output to stdout).
	buf.WriteString("\n.mode json\nSELECT count(*) AS n FROM rec;\n")
	out, err := runDuckDB(context.Background(), cfg.OutDB, buf.String())
	if err != nil {
		t.Fatalf("run base: %v", err)
	}
	var recRows []map[string]any
	if err := json.Unmarshal([]byte(out), &recRows); err != nil {
		t.Fatalf("decode rec count: %v\noutput: %s", err, out)
	}
	if len(recRows) != 1 {
		t.Fatalf("expected 1 rec count row, got %d", len(recRows))
	}
	if n := jsonCount(t, recRows[0]["n"]); n < 5 {
		t.Fatalf("expected rec count >= 5 (5 jsonl lines across fixtures), got %d", n)
	}

	// incidents is durable, so a separate process can read it; base emits none.
	incRows := query(t, cfg.OutDB, "SELECT count(*) AS n FROM incidents;")
	if len(incRows) != 1 {
		t.Fatalf("expected 1 incidents count row, got %d", len(incRows))
	}
	if n := jsonCount(t, incRows[0]["n"]); n != 0 {
		t.Fatalf("expected 0 incidents from base alone, got %d", n)
	}
}
