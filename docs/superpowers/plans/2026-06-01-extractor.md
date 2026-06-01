# Extractor (Track A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the Track A corpus-mining extractor — a Go orchestrator that runs SQL/duckdb detectors over the `~/.claude/projects/**/*.jsonl` session corpus and writes behavioral-glitch `incidents` to `incidents.db`, with the skeleton-first whole-file-read fixture passing end-to-end as the acceptance bar.

**Architecture:** Detector logic lives in `.sql.tmpl` files (rendered with Go `text/template`, embedded via `//go:embed`) and is executed by shelling out to the `duckdb` CLI — **no CGO duckdb driver**. Go is a thin orchestrator: parse flags → build `Config` → render the SQL pipeline → pipe it to `duckdb incidents.db` over stdin. Each signal detector is one SQL file producing a uniform `events` CTE that a shared `windowed_insert` partial turns into rows in the `incidents` table. Re-runs are idempotent via a deterministic `incident_id` primary key + `ON CONFLICT DO NOTHING`, so `deja-vu` (a later plan) can accumulate history.

**Tech Stack:** Go (stdlib only — `embed`, `text/template`, `os/exec`, `flag`, `encoding/json`), DuckDB 1.5.x via its CLI, Nix flake (`buildGoModule`, `vendorHash = null`) for devshell + packaging.

---

## Scope

This plan covers **only the Track A extractor** — the foundation every later unit reads. Out of scope (separate plans): Track B freshness audit, the analyst subagent, the applier/PR path, and `deja-vu`. The extractor emits four of the five spec signals; `repeated_guidance` is intentionally **not** an extractor signal (the spec assigns it to the analyst, which clusters across ≥3 sessions).

## File Structure

```
agent-smith/
├── flake.nix                              # devshell (go, duckdb, gopls) + packages.default
├── go.mod                                 # module github.com/noamsto/agent-smith, no external deps
├── cmd/extractor/main.go                  # CLI: flags → Config → extractor.Run
├── internal/extractor/
│   ├── config.go                          # Config struct + DefaultConfig()
│   ├── run.go                             # Run(): render templates, exec duckdb; runDuckDB(); Summary()
│   ├── run_test.go                        # white-box helpers + base-pipeline test
│   ├── inefficiency_test.go               # acceptance-adjacent unit test
│   ├── signals_test.go                    # tool_error/retry/correction/disagreement tests
│   ├── cli_test.go                        # end-to-end over fixtures/
│   ├── sql/
│   │   ├── _partials.sql.tmpl             # {{define "windowed_insert"}}
│   │   ├── 00_base.sql.tmpl               # incidents DDL + corpus load + derived TEMP tables
│   │   ├── 10_inefficiency.sql.tmpl
│   │   ├── 20_tool_error.sql.tmpl         # tool_error + retry (two statements)
│   │   ├── 30_user_correction.sql.tmpl
│   │   └── 40_orchestrator_disagreement.sql.tmpl
│   └── testdata/                          # tiny per-detector fixtures
│       ├── base/{s1.jsonl,s2.jsonl}
│       ├── tool_error/s.jsonl
│       ├── user_correction/s.jsonl
│       └── orchestrator/s.jsonl
├── fixtures/skeleton-first/               # canonical acceptance fixture (spec §9)
│   ├── violation.jsonl                    # whole-file Read of a large file (should flag)
│   └── compliant.jsonl                    # partial/offset Read + small file (should NOT flag)
└── docs/extractor.md                      # usage + real-corpus run notes (Task 10)
```

**Naming contract (used across tasks — keep these exact):**

- Go module: `github.com/noamsto/agent-smith`; binary package `cmd/extractor`; logic package `internal/extractor`.
- Every detector's `events` CTE projects exactly these columns, in this order:
  `incident_id, session_id, project, ts, signal_type, implicated_artifact, candidates, confidence, detail, turn`.
- TEMP tables created by `00_base`: `raw`, `rec`, `tool_uses`, `tool_results`, `user_text`, `rec_text`, `session_meta`, `artifact_main`.
- Signal names (CLI + file mapping): `inefficiency`→`10_inefficiency`, `tool_error`→`20_tool_error` (also emits `retry`), `user_correction`→`30_user_correction`, `orchestrator_disagreement`→`40_orchestrator_disagreement`.

---

## Task 1: Repo scaffold + devshell + duckdb wiring smoke test

**Files:**
- Create: `go.mod`
- Create: `flake.nix`
- Create: `cmd/extractor/main.go` (placeholder)
- Create: `internal/extractor/run.go` (just `runDuckDB` for now)
- Test: `internal/extractor/run_test.go`

- [ ] **Step 1: Create `go.mod`**

```
module github.com/noamsto/agent-smith

go 1.23
```

- [ ] **Step 2: Create `flake.nix`**

```nix
{
  description = "agent-smith — Track A corpus-mining extractor";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let pkgs = import nixpkgs { inherit system; };
      in {
        packages.default = pkgs.buildGoModule {
          pname = "agent-smith";
          version = "0.1.0";
          src = ./.;
          vendorHash = null; # stdlib only
          subPackages = [ "cmd/extractor" ];
          nativeBuildInputs = [ pkgs.makeWrapper ];
          nativeCheckInputs = [ pkgs.duckdb ]; # tests shell out to duckdb
          postInstall = ''
            wrapProgram $out/bin/extractor \
              --prefix PATH : ${pkgs.duckdb}/bin
          '';
        };

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.gopls pkgs.go-tools pkgs.duckdb pkgs.jq ];
        };
      });
}
```

- [ ] **Step 3: Create placeholder `cmd/extractor/main.go`**

```go
package main

import "fmt"

func main() {
	fmt.Println("agent-smith extractor")
}
```

- [ ] **Step 4: Create `internal/extractor/run.go` with just the duckdb exec wrapper**

```go
// Package extractor mines the Claude Code .jsonl session corpus for behavioral
// glitches and writes them to an incidents.db DuckDB database.
package extractor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// duckDBBin is the duckdb executable; overridable for tests/packaging.
func duckDBBin() string {
	if b := os.Getenv("AGENT_SMITH_DUCKDB"); b != "" {
		return b
	}
	return "duckdb"
}

// runDuckDB pipes a SQL script to `duckdb <db>` over stdin and returns stdout.
func runDuckDB(ctx context.Context, db, script string) (string, error) {
	cmd := exec.CommandContext(ctx, duckDBBin(), db)
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("duckdb failed: %w\nstderr: %s", err, stderr.String())
	}
	return string(out), nil
}
```

- [ ] **Step 5: Write the failing wiring test**

```go
package extractor

import (
	"context"
	"strings"
	"testing"
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
```

- [ ] **Step 6: Run the test (inside the devshell so duckdb is present)**

Run: `nix develop -c go test ./internal/extractor/ -run TestRunDuckDBSmoke -v`
Expected: PASS (proves Go ↔ duckdb CLI wiring works).

- [ ] **Step 7: Verify the binary builds**

Run: `nix develop -c go build ./...`
Expected: no output, exit 0.

- [ ] **Step 8: Commit**

```bash
git add go.mod flake.nix cmd/ internal/
git commit -m "feat(extractor): scaffold repo, devshell, and duckdb exec wiring"
```

---

## Task 2: Config + base SQL pipeline (corpus load → derived tables)

**Files:**
- Create: `internal/extractor/config.go`
- Create: `internal/extractor/sql/00_base.sql.tmpl`
- Modify: `internal/extractor/run.go` (add embed, template rendering, `Run`, `RenderBase` helper)
- Test: `internal/extractor/run_test.go` (add base-pipeline test + shared helpers)

- [ ] **Step 1: Create `internal/extractor/config.go`**

```go
package extractor

import (
	"os"
	"path/filepath"
)

// Config drives both the Go orchestration and the SQL templates.
type Config struct {
	CorpusGlob string // glob of .jsonl files to mine
	OutDB      string // output DuckDB file
	Since      string // optional ISO8601 lower bound on record timestamp; "" = all
	Signals    []string // which detectors to run; empty = all

	// DuckDB runtime (optional safety; loader uses read_ndjson_objects which streams without OOM)
	MemoryLimit string // memory_limit pragma, e.g. "8GB"; "" = duckdb default. Validated before interpolation.
	Threads     int    // threads pragma; 0 = omit (duckdb default / all cores)

	// Window (transcript slice stored per incident)
	WindowBefore int
	WindowAfter  int
	ExcerptChars int

	// inefficiency thresholds (line counts)
	LargeFileLines int
	MediumLines    int
	HighLines      int

	// retry / correction / disagreement windows
	RetryWindowTurns   int
	CorrectionLookback int
	DisagreeWindow     int

	// regexes (RE2; no single quotes — they break SQL string literals)
	CorrectionRegex string
	DisagreeRegex   string

	// artifact resolution
	GlobalClaudeMd string // path to global CLAUDE.md
	AgentsDir      string // dir holding subagent .md files
}

// AllSignals is the canonical ordered detector list.
var AllSignals = []string{
	"inefficiency",
	"tool_error",
	"user_correction",
	"orchestrator_disagreement",
}

// signalFile maps a signal name to its SQL template filename.
var signalFile = map[string]string{
	"inefficiency":              "10_inefficiency.sql.tmpl",
	"tool_error":                "20_tool_error.sql.tmpl",
	"user_correction":           "30_user_correction.sql.tmpl",
	"orchestrator_disagreement": "40_orchestrator_disagreement.sql.tmpl",
}

// DefaultConfig returns production defaults, resolving paths under $HOME.
func DefaultConfig() Config {
	home, _ := os.UserHomeDir()
	return Config{
		CorpusGlob:         filepath.Join(home, ".claude", "projects", "**", "*.jsonl"),
		OutDB:              "incidents.db",
		Since:              "",
		Signals:            nil,
		MemoryLimit:        "8GB",     // optional safety cap; duckdb spills above it
		Threads:            0,         // duckdb default (all cores)
		WindowBefore:       3,
		WindowAfter:        4,
		ExcerptChars:       300,
		LargeFileLines:     300,
		MediumLines:        500,
		HighLines:          1000,
		RetryWindowTurns:   5,
		CorrectionLookback: 2,
		DisagreeWindow:     4,
		CorrectionRegex:    `(\bno\b|\bdon.?t\b|\bactually\b|\brevert\b|that.?s wrong|\bwrong\b|\bundo\b|\bnope\b|incorrect|\bstop\b)`,
		DisagreeRegex:      `(\bdisagree|not what i asked|that.?s not right|that.?s not correct|that.?s wrong|that.?s incorrect|let me redo|let me just redo|i.?ll redo|i.?ll do this myself|i.?ll do it myself|redo this myself|the subagent is wrong|the subagent was wrong|subagent got it wrong|subagent is incorrect|ignore the subagent|\boverrule)`,  // overrule-specific; omits bare wrong/incorrect
		GlobalClaudeMd:     filepath.Join(home, ".claude", "CLAUDE.md"),
		AgentsDir:          filepath.Join(home, "nix-config", "home", "ai", "claude-code", "agents"),
	}
}
```

- [ ] **Step 2: Create `internal/extractor/sql/00_base.sql.tmpl`**

> Loads each jsonl line as a raw JSON value via `read_ndjson_objects` (no struct schema inference, which would otherwise silently drop fields like `isSidechain` and coerce timestamps; it also streams without the `read_csv` `max_line_size × threads` OOM). All later tables read from `rec`. Scalar fields detectors filter on (`file_path`, `rd_offset`, `rd_limit`, `subagent_type`) are projected here as columns — never filtered via JSON operators inside a join (that triggers a duckdb cast error).

```sql
-- Durable output table (append across runs; idempotent via PK + ON CONFLICT).
CREATE TABLE IF NOT EXISTS incidents (
  incident_id         VARCHAR PRIMARY KEY,
  session_id          VARCHAR,
  project             VARCHAR,
  ts                  VARCHAR,
  signal_type         VARCHAR,
  implicated_artifact VARCHAR,
  candidates          JSON,
  "window"            JSON,                  -- quoted: window is a duckdb reserved keyword
  confidence          VARCHAR,
  detail              JSON
);

-- 1) Raw load: one row per jsonl line as a raw JSON value (no schema inference).
-- read_ndjson_objects is the purpose-built reader; unlike read_csv it doesn't
-- pre-reserve a maximum_line_size x threads buffer, so it streams the corpus
-- without OOM. Malformed/blank lines skipped; downstream sessionId filter drops the rest.
CREATE OR REPLACE TEMP TABLE raw AS
SELECT json AS j, filename
FROM read_ndjson_objects('{{.CorpusGlob}}', ignore_errors=true, filename=true);

-- 2) Normalized records with a per-session turn ordering (by timestamp).
CREATE OR REPLACE TEMP TABLE rec AS
SELECT j->>'type'                      AS type,
       j->>'sessionId'                 AS session_id,
       j->>'uuid'                      AS uuid,
       j->>'timestamp'                 AS ts,
       j->>'cwd'                       AS cwd,
       COALESCE((j->>'isSidechain') = 'true', false) AS is_sidechain,  -- COALESCE: absent field means "not a sidechain", keeps is_subagent a definite boolean (NOT NULL filters work)
       j->'message'                    AS message,
       j->'toolUseResult'              AS tur,
       filename,
       row_number() OVER (PARTITION BY j->>'sessionId'
                          ORDER BY j->>'timestamp', j->>'uuid') AS turn
FROM raw
WHERE j->>'sessionId' IS NOT NULL
{{if .Since}}  AND j->>'timestamp' >= '{{.Since}}'{{end}};

-- 3) Assistant tool_use blocks (scalar fields pre-extracted as columns).
CREATE OR REPLACE TEMP TABLE tool_uses AS
WITH g AS (
  SELECT session_id, turn, ts,
         CASE WHEN json_type(message->'content') = 'ARRAY'
              THEN CAST(message->'content' AS JSON[])
              ELSE CAST('[]' AS JSON[]) END AS blocks
  FROM rec WHERE type = 'assistant'
)
SELECT g.session_id, g.turn, g.ts,
       c.value->>'id'                                    AS tuid,
       c.value->>'name'                                  AS tool,
       json_extract_string(c.value->'input', 'file_path')     AS file_path,
       json_extract_string(c.value->'input', 'offset')        AS rd_offset,
       json_extract_string(c.value->'input', 'limit')         AS rd_limit,
       json_extract_string(c.value->'input', 'subagent_type') AS subagent_type,
       CAST(c.value->'input' AS VARCHAR)                 AS input_str
FROM g, unnest(g.blocks) AS c(value)
WHERE c.value->>'type' = 'tool_use';

-- 4) Tool results (is_error flag + read result size for inefficiency).
CREATE OR REPLACE TEMP TABLE tool_results AS
WITH g AS (
  SELECT session_id, turn, ts, tur,
         CASE WHEN json_type(message->'content') = 'ARRAY'
              THEN CAST(message->'content' AS JSON[])
              ELSE CAST('[]' AS JSON[]) END AS blocks
  FROM rec WHERE type = 'user'
)
SELECT g.session_id, g.turn, g.ts,
       c.value->>'tool_use_id'              AS tuid,
       (c.value->>'is_error') = 'true'      AS is_error,
       COALESCE((g.tur->'file'->>'totalLines')::BIGINT,
                (g.tur->'file'->>'numLines')::BIGINT, 0) AS total_lines
FROM g, unnest(g.blocks) AS c(value)
WHERE c.value->>'type' = 'tool_result';

-- 5) Human user text (handles both string- and array-form content).
CREATE OR REPLACE TEMP TABLE user_text AS
WITH g AS (
  SELECT session_id, turn, ts,
         CASE WHEN json_type(message->'content') = 'ARRAY'
              THEN CAST(message->'content' AS JSON[])
              ELSE CAST('[]' AS JSON[]) END AS blocks
  FROM rec WHERE type = 'user'
),
arr AS (
  SELECT g.session_id, g.turn, any_value(g.ts) AS ts,
         string_agg(c.value->>'text', ' ') AS text
  FROM g, unnest(g.blocks) AS c(value)
  WHERE c.value->>'type' = 'text'
  GROUP BY g.session_id, g.turn
),
str AS (
  SELECT session_id, turn, ts, message->>'content' AS text
  FROM rec
  WHERE type = 'user' AND json_type(message->'content') = 'VARCHAR'
)
SELECT session_id, turn, ts, text FROM arr
UNION ALL
SELECT session_id, turn, ts, text FROM str;

-- 6) Compact per-turn excerpt for window slices.
CREATE OR REPLACE TEMP TABLE rec_text AS
SELECT session_id, turn, ts, type,
       substr(CAST(message AS VARCHAR), 1, {{.ExcerptChars}}) AS excerpt
FROM rec;

-- 7) Session-level metadata.
CREATE OR REPLACE TEMP TABLE session_meta AS
SELECT session_id, any_value(cwd) AS cwd, bool_or(is_sidechain) AS is_subagent
FROM rec GROUP BY session_id;

-- 8) Main-session artifact candidates (global + project CLAUDE.md).
CREATE OR REPLACE TEMP TABLE artifact_main AS
SELECT session_id,
       COALESCE(cwd || '/CLAUDE.md', '{{.GlobalClaudeMd}}') AS implicated_artifact,
       json_array('{{.GlobalClaudeMd}}',
                  COALESCE(cwd || '/CLAUDE.md', '{{.GlobalClaudeMd}}')) AS candidates
FROM session_meta;
```

- [ ] **Step 3: Replace `internal/extractor/run.go` with embed + rendering + `Run`**

```go
// Package extractor mines the Claude Code .jsonl session corpus for behavioral
// glitches and writes them to an incidents.db DuckDB database.
package extractor

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/template"
)

//go:embed sql/*.sql.tmpl
var sqlFS embed.FS

func duckDBBin() string {
	if b := os.Getenv("AGENT_SMITH_DUCKDB"); b != "" {
		return b
	}
	return "duckdb"
}

func runDuckDB(ctx context.Context, db, script string) (string, error) {
	cmd := exec.CommandContext(ctx, duckDBBin(), db)
	cmd.Stdin = strings.NewReader(script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("duckdb failed: %w\nstderr: %s", err, stderr.String())
	}
	return string(out), nil
}

// renderScript renders the base pipeline plus the selected detector templates,
// concatenated in order, into one SQL script.
func renderScript(cfg Config) (string, error) {
	tmpl, err := template.New("sql").ParseFS(sqlFS, "sql/*.sql.tmpl")
	if err != nil {
		return "", fmt.Errorf("parse templates: %w", err)
	}
	files := []string{"00_base.sql.tmpl"}
	signals := cfg.Signals
	if len(signals) == 0 {
		signals = AllSignals
	}
	for _, s := range signals {
		if s == "" {
			continue
		}
		f, ok := signalFile[s]
		if !ok {
			return "", fmt.Errorf("unknown signal %q", s)
		}
		files = append(files, f)
	}
	var buf bytes.Buffer
	for _, f := range files {
		if err := tmpl.ExecuteTemplate(&buf, f, cfg); err != nil {
			return "", fmt.Errorf("render %s: %w", f, err)
		}
		buf.WriteString("\n")
	}
	return buf.String(), nil
}

// Run renders the pipeline and executes it against cfg.OutDB.
func Run(ctx context.Context, cfg Config) error {
	script, err := renderScript(cfg)
	if err != nil {
		return err
	}
	if _, err := runDuckDB(ctx, cfg.OutDB, script); err != nil {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Create the base fixture files**

`internal/extractor/testdata/base/s1.jsonl` (one whole-file large Read in a main session):

```
{"type":"assistant","sessionId":"s1","uuid":"a1","timestamp":"2026-05-01T10:00:00Z","cwd":"/home/noams/proj","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/home/noams/proj/big.go"}}]}}
{"type":"user","sessionId":"s1","uuid":"u1","timestamp":"2026-05-01T10:00:02Z","cwd":"/home/noams/proj","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":false,"content":"file text"}]},"toolUseResult":{"type":"text","file":{"filePath":"/home/noams/proj/big.go","totalLines":500}}}
{"type":"user","sessionId":"s1","uuid":"u2","timestamp":"2026-05-01T10:00:05Z","cwd":"/home/noams/proj","message":{"role":"user","content":"no, that is wrong, revert that"}}
```

`internal/extractor/testdata/base/s2.jsonl` (a subagent session with a tool error):

```
{"type":"assistant","sessionId":"s2","uuid":"b1","timestamp":"2026-05-02T09:00:00Z","cwd":"/home/noams/projB","isSidechain":true,"message":{"role":"assistant","content":[{"type":"tool_use","id":"k1","name":"Bash","input":{"command":"ls /nope"}}]}}
{"type":"user","sessionId":"s2","uuid":"b2","timestamp":"2026-05-02T09:00:30Z","cwd":"/home/noams/projB","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"k1","is_error":true,"content":"No such file"}]}}
```

- [ ] **Step 5: Create `internal/extractor/run_test.go` with shared helpers + base test**

```go
package extractor

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
)

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

// renderBaseOnly renders just the base pipeline plus a debug SELECT and runs it,
// returning the row count of a derived table.
func TestBasePipelineBuildsDerivedTables(t *testing.T) {
	cfg := testConfig(t, "base")
	cfg.Signals = []string{} // base only: render base + all detectors is fine, but
	// to isolate the base layer we render base and append a probe query.
	script, err := renderScript(Config{ // base + no detectors
		CorpusGlob: cfg.CorpusGlob,
		ExcerptChars: cfg.ExcerptChars, GlobalClaudeMd: cfg.GlobalClaudeMd,
		Signals: []string{"inefficiency"}, // include one so file list is valid
		LargeFileLines: 999999, MediumLines: 500, HighLines: 1000,
		WindowBefore: cfg.WindowBefore, WindowAfter: cfg.WindowAfter,
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if _, err := runDuckDB(context.Background(), cfg.OutDB, script); err != nil {
		t.Fatalf("run base: %v", err)
	}
	// LargeFileLines is absurdly high, so inefficiency produces 0 rows; the point
	// is that the base derived tables loaded without error and incidents exists.
	rows := query(t, cfg.OutDB, "SELECT count(*) AS n FROM incidents;")
	if len(rows) != 1 {
		t.Fatalf("expected 1 count row, got %d", len(rows))
	}
}
```

> Note: the base TEMP tables only exist within a single duckdb invocation, so we assert against the durable `incidents` table (which is created and persisted). A high `LargeFileLines` guarantees zero inserts, proving the base + a real detector parse and execute cleanly end-to-end. Per-table row counts get exercised by the detector tasks that follow.

- [ ] **Step 6: Run the base test**

Run: `nix develop -c go test ./internal/extractor/ -run TestBasePipeline -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/extractor/config.go internal/extractor/run.go internal/extractor/sql/00_base.sql.tmpl internal/extractor/testdata/base internal/extractor/run_test.go
git commit -m "feat(extractor): config + base SQL pipeline (corpus load to derived tables)"
```

---

## Task 3: Incidents schema + windowed_insert partial

**Files:**
- Create: `internal/extractor/sql/_partials.sql.tmpl`
- Test: `internal/extractor/run_test.go` (add idempotency test)

> The incidents DDL already lives at the top of `00_base`. This task adds the shared `windowed_insert` partial that every detector ends with, and proves the deterministic-id + `ON CONFLICT DO NOTHING` idempotency that `deja-vu` will depend on.

- [ ] **Step 1: Create `internal/extractor/sql/_partials.sql.tmpl`**

> This template defines a named block only — executing the file directly renders nothing. Detectors call it as `{{` `template "windowed_insert" .` `}}` immediately after a `WITH events AS (...)` clause. It continues the CTE chain with `, win AS (...)` and inserts.

```sql
{{define "windowed_insert"}}
, win AS (
  SELECT e.incident_id,
         to_json(list(json_object('turn', rt.turn, 'type', rt.type, 'excerpt', rt.excerpt)
                      ORDER BY rt.turn)) AS "window"  -- list() is a native aggregate; honors ORDER BY (json_group_array is a macro and does not)
  FROM events e
  JOIN rec_text rt
    ON rt.session_id = e.session_id
   AND rt.turn BETWEEN e.turn - {{.WindowBefore}} AND e.turn + {{.WindowAfter}}
  GROUP BY e.incident_id
)
INSERT INTO incidents
SELECT e.incident_id, e.session_id, e.project, e.ts, e.signal_type,
       e.implicated_artifact, e.candidates, w."window", e.confidence, e.detail
FROM events e
LEFT JOIN win w USING (incident_id)
ON CONFLICT (incident_id) DO NOTHING;
{{end}}
```

- [ ] **Step 2: Write the failing idempotency test (white-box, uses runDuckDB directly)**

Add to `internal/extractor/run_test.go`:

```go
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
	if got := rows[0]["n"]; got != float64(1) && got != "1" {
		t.Fatalf("expected 1 incident after duplicate insert, got %v", got)
	}
}
```

- [ ] **Step 3: Run the test**

Run: `nix develop -c go test ./internal/extractor/ -run TestIncidentID -v`
Expected: PASS (duplicate insert is a no-op).

- [ ] **Step 4: Commit**

```bash
git add internal/extractor/sql/_partials.sql.tmpl internal/extractor/run_test.go
git commit -m "feat(extractor): windowed_insert partial + idempotent incident writes"
```

---

## Task 4: Inefficiency detector + skeleton-first fixture (ACCEPTANCE BAR)

**Files:**
- Create: `internal/extractor/sql/10_inefficiency.sql.tmpl`
- Create: `fixtures/skeleton-first/violation.jsonl`
- Create: `fixtures/skeleton-first/compliant.jsonl`
- Test: `internal/extractor/inefficiency_test.go`

> This is the spec §9 acceptance case for Track A: a whole-file Read of a large file must surface as an `inefficiency` incident whose candidate artifacts include the **global CLAUDE.md** (so the analyst, in a later plan, can trace it to the existing skeleton-first rule and choose `strengthen` rather than a duplicate `add`). A partial read, or a small file, must NOT flag.

- [ ] **Step 1: Create `internal/extractor/sql/10_inefficiency.sql.tmpl`**

```sql
-- inefficiency: whole-file Read (no offset, no limit) of a large file.
WITH events AS (
  SELECT
    md5(tu.session_id || ':' || CAST(tu.turn AS VARCHAR) || ':inefficiency') AS incident_id,
    tu.session_id,
    sm.cwd AS project,
    tu.ts,
    'inefficiency' AS signal_type,
    am.implicated_artifact,
    am.candidates,
    CASE WHEN tr.total_lines >= {{.HighLines}}   THEN 'high'
         WHEN tr.total_lines >= {{.MediumLines}} THEN 'medium'
         ELSE 'low' END AS confidence,
    json_object('tool', 'Read', 'file_path', tu.file_path, 'total_lines', tr.total_lines) AS detail,
    tu.turn
  FROM tool_uses tu
  JOIN tool_results tr ON tr.session_id = tu.session_id AND tr.tuid = tu.tuid
  JOIN session_meta  sm ON sm.session_id = tu.session_id
  JOIN artifact_main am ON am.session_id = tu.session_id
  WHERE tu.tool = 'Read'
    AND tu.rd_offset IS NULL
    AND tu.rd_limit  IS NULL
    AND tr.total_lines >= {{.LargeFileLines}}
)
{{template "windowed_insert" .}}
```

- [ ] **Step 2: Create `fixtures/skeleton-first/violation.jsonl`**

```
{"type":"assistant","sessionId":"viol-1","uuid":"a1","timestamp":"2026-05-10T08:00:00Z","cwd":"/home/noams/work","message":{"role":"assistant","content":[{"type":"text","text":"Let me read the file."},{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"/home/noams/work/server.go"}}]}}
{"type":"user","sessionId":"viol-1","uuid":"u1","timestamp":"2026-05-10T08:00:01Z","cwd":"/home/noams/work","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r1","is_error":false,"content":"...big file..."}]},"toolUseResult":{"type":"text","file":{"filePath":"/home/noams/work/server.go","totalLines":1200}}}
{"type":"assistant","sessionId":"viol-1","uuid":"a2","timestamp":"2026-05-10T08:00:05Z","cwd":"/home/noams/work","message":{"role":"assistant","content":[{"type":"text","text":"Now I understand the structure."}]}}
```

- [ ] **Step 3: Create `fixtures/skeleton-first/compliant.jsonl`**

```
{"type":"assistant","sessionId":"ok-1","uuid":"c1","timestamp":"2026-05-10T09:00:00Z","cwd":"/home/noams/work","message":{"role":"assistant","content":[{"type":"tool_use","id":"r2","name":"Read","input":{"file_path":"/home/noams/work/server.go","offset":100,"limit":50}}]}}
{"type":"user","sessionId":"ok-1","uuid":"d1","timestamp":"2026-05-10T09:00:01Z","cwd":"/home/noams/work","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r2","is_error":false,"content":"...50 lines..."}]},"toolUseResult":{"type":"text","file":{"filePath":"/home/noams/work/server.go","totalLines":1200}}}
{"type":"assistant","sessionId":"ok-2","uuid":"c2","timestamp":"2026-05-10T09:10:00Z","cwd":"/home/noams/work","message":{"role":"assistant","content":[{"type":"tool_use","id":"r3","name":"Read","input":{"file_path":"/home/noams/work/tiny.go"}}]}}
{"type":"user","sessionId":"ok-2","uuid":"d2","timestamp":"2026-05-10T09:10:01Z","cwd":"/home/noams/work","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"r3","is_error":false,"content":"...short..."}]},"toolUseResult":{"type":"text","file":{"filePath":"/home/noams/work/tiny.go","totalLines":40}}}
```

- [ ] **Step 4: Create `internal/extractor/inefficiency_test.go`**

```go
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
```

- [ ] **Step 5: Run the tests**

Run: `nix develop -c go test ./internal/extractor/ -run TestInefficiency -v`
Expected: PASS. The `compliant.jsonl` sessions (partial read; 40-line file) produce zero incidents, so the count is exactly 1.

- [ ] **Step 6: Commit**

```bash
git add internal/extractor/sql/10_inefficiency.sql.tmpl fixtures/skeleton-first internal/extractor/inefficiency_test.go
git commit -m "feat(extractor): inefficiency detector + skeleton-first acceptance fixture"
```

---

## Task 5: tool_error + retry detectors

**Files:**
- Create: `internal/extractor/sql/20_tool_error.sql.tmpl`
- Create: `internal/extractor/testdata/tool_error/s.jsonl`
- Test: `internal/extractor/signals_test.go` (create; add tool_error + retry cases)

- [ ] **Step 1: Create `internal/extractor/sql/20_tool_error.sql.tmpl`**

> Two statements: `tool_error` (a tool_result with `is_error`) and `retry` (the same tool+input repeated within `RetryWindowTurns`). Each ends with the shared partial.

```sql
-- tool_error: a tool_result flagged is_error.
WITH events AS (
  SELECT
    md5(tr.session_id || ':' || CAST(tr.turn AS VARCHAR) || ':tool_error') AS incident_id,
    tr.session_id,
    sm.cwd AS project,
    tr.ts,
    'tool_error' AS signal_type,
    COALESCE(am.implicated_artifact, '{{.GlobalClaudeMd}}') AS implicated_artifact,
    COALESCE(am.candidates, json_array('{{.GlobalClaudeMd}}')) AS candidates,
    'medium' AS confidence,
    json_object('tool', tu.tool, 'tuid', tr.tuid) AS detail,
    tr.turn
  FROM tool_results tr
  JOIN tool_uses    tu ON tu.session_id = tr.session_id AND tu.tuid = tr.tuid
  JOIN session_meta sm ON sm.session_id = tr.session_id
  LEFT JOIN artifact_main am ON am.session_id = tr.session_id
  WHERE tr.is_error
)
{{template "windowed_insert" .}}

-- retry: identical tool+input reappears within RetryWindowTurns turns.
WITH events AS (
  SELECT DISTINCT
    md5(b.session_id || ':' || CAST(b.turn AS VARCHAR) || ':retry') AS incident_id,
    b.session_id,
    sm.cwd AS project,
    b.ts,
    'retry' AS signal_type,
    am.implicated_artifact,
    am.candidates,
    'low' AS confidence,
    json_object('tool', b.tool, 'input', b.input_str) AS detail,
    b.turn
  FROM tool_uses a
  JOIN tool_uses b
    ON b.session_id = a.session_id
   AND b.tool = a.tool
   AND b.input_str = a.input_str
   AND b.turn > a.turn
   AND b.turn - a.turn <= {{.RetryWindowTurns}}
  JOIN session_meta  sm ON sm.session_id = b.session_id
  JOIN artifact_main am ON am.session_id = b.session_id
)
{{template "windowed_insert" .}}
```

- [ ] **Step 2: Create `internal/extractor/testdata/tool_error/s.jsonl`**

> One errored Bash result, plus the same `ls /nope` command run twice (turns 1 and 3) to trigger retry.

```
{"type":"assistant","sessionId":"e1","uuid":"a1","timestamp":"2026-05-03T10:00:00Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"x1","name":"Bash","input":{"command":"ls /nope"}}]}}
{"type":"user","sessionId":"e1","uuid":"u1","timestamp":"2026-05-03T10:00:01Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x1","is_error":true,"content":"No such file"}]}}
{"type":"assistant","sessionId":"e1","uuid":"a2","timestamp":"2026-05-03T10:00:05Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"x2","name":"Bash","input":{"command":"ls /nope"}}]}}
{"type":"user","sessionId":"e1","uuid":"u2","timestamp":"2026-05-03T10:00:06Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"x2","is_error":true,"content":"No such file"}]}}
```

- [ ] **Step 3: Create `internal/extractor/signals_test.go` with tool_error + retry tests**

```go
package extractor

import (
	"context"
	"testing"
)

func countBySignal(t *testing.T, db string) map[string]int {
	t.Helper()
	rows := query(t, db, "SELECT signal_type, count(*) AS n FROM incidents GROUP BY signal_type;")
	m := map[string]int{}
	for _, r := range rows {
		switch v := r["n"].(type) {
		case float64:
			m[r["signal_type"].(string)] = int(v)
		case string:
			// duckdb -json emits numbers as JSON numbers; string fallback unused.
		}
	}
	return m
}

func TestToolErrorAndRetry(t *testing.T) {
	cfg := testConfig(t, "tool_error", "tool_error")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	if c["tool_error"] != 2 {
		t.Errorf("expected 2 tool_error incidents, got %d", c["tool_error"])
	}
	if c["retry"] < 1 {
		t.Errorf("expected at least 1 retry incident, got %d", c["retry"])
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `nix develop -c go test ./internal/extractor/ -run TestToolErrorAndRetry -v`
Expected: PASS (2 errors, ≥1 retry).

- [ ] **Step 5: Commit**

```bash
git add internal/extractor/sql/20_tool_error.sql.tmpl internal/extractor/testdata/tool_error internal/extractor/signals_test.go
git commit -m "feat(extractor): tool_error and retry detectors"
```

---

## Task 6: user_correction detector

**Files:**
- Create: `internal/extractor/sql/30_user_correction.sql.tmpl`
- Create: `internal/extractor/testdata/user_correction/s.jsonl`
- Test: `internal/extractor/signals_test.go` (add correction case)

- [ ] **Step 1: Create `internal/extractor/sql/30_user_correction.sql.tmpl`**

> A human user message matching the correction regex (or an interruption marker), occurring within `CorrectionLookback` turns after an assistant tool_use.

```sql
-- user_correction: negation/interruption text shortly after an assistant tool_use.
WITH events AS (
  SELECT
    md5(ut.session_id || ':' || CAST(ut.turn AS VARCHAR) || ':user_correction') AS incident_id,
    ut.session_id,
    sm.cwd AS project,
    ut.ts,
    'user_correction' AS signal_type,
    am.implicated_artifact,
    am.candidates,
    'medium' AS confidence,
    json_object('text', substr(ut.text, 1, 200)) AS detail,
    ut.turn
  FROM user_text ut
  JOIN session_meta  sm ON sm.session_id = ut.session_id
  JOIN artifact_main am ON am.session_id = ut.session_id
  WHERE ut.text IS NOT NULL
    AND ( regexp_matches(lower(ut.text), '{{.CorrectionRegex}}')
          OR lower(ut.text) LIKE '%request interrupted%' )
    AND EXISTS (
      SELECT 1 FROM tool_uses tx
      WHERE tx.session_id = ut.session_id
        AND tx.turn < ut.turn
        AND ut.turn - tx.turn <= {{.CorrectionLookback}}
    )
)
{{template "windowed_insert" .}}
```

- [ ] **Step 2: Create `internal/extractor/testdata/user_correction/s.jsonl`**

> Turn 1 = assistant tool_use; turn 2 = a correction ("no, that's wrong"); also an interruption marker; plus a benign user message that must NOT flag.

```
{"type":"assistant","sessionId":"c1","uuid":"a1","timestamp":"2026-05-04T10:00:00Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"y1","name":"Edit","input":{"file_path":"/home/noams/p/x.go"}}]}}
{"type":"user","sessionId":"c1","uuid":"u1","timestamp":"2026-05-04T10:00:02Z","cwd":"/home/noams/p","message":{"role":"user","content":"no, that's wrong, revert that"}}
{"type":"assistant","sessionId":"c1","uuid":"a2","timestamp":"2026-05-04T10:00:10Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"y2","name":"Bash","input":{"command":"go build"}}]}}
{"type":"user","sessionId":"c1","uuid":"u2","timestamp":"2026-05-04T10:00:12Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user]"}]}}
{"type":"assistant","sessionId":"c2","uuid":"a3","timestamp":"2026-05-04T11:00:00Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"y3","name":"Read","input":{"file_path":"/home/noams/p/y.go"}}]}}
{"type":"user","sessionId":"c2","uuid":"u3","timestamp":"2026-05-04T11:00:02Z","cwd":"/home/noams/p","message":{"role":"user","content":"thanks, that looks great"}}
```

- [ ] **Step 3: Add the test to `internal/extractor/signals_test.go`**

```go
func TestUserCorrection(t *testing.T) {
	cfg := testConfig(t, "user_correction", "user_correction")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	// "no, that's wrong" (turn 2) + interruption marker (turn 4) = 2.
	// "thanks, that looks great" (session c2) must NOT flag.
	if c["user_correction"] != 2 {
		t.Fatalf("expected 2 user_correction incidents, got %d", c["user_correction"])
	}
}
```

- [ ] **Step 4: Run the test**

Run: `nix develop -c go test ./internal/extractor/ -run TestUserCorrection -v`
Expected: PASS (exactly 2; the benign message does not flag).

- [ ] **Step 5: Commit**

```bash
git add internal/extractor/sql/30_user_correction.sql.tmpl internal/extractor/testdata/user_correction internal/extractor/signals_test.go
git commit -m "feat(extractor): user_correction detector"
```

---

## Task 7: orchestrator_disagreement detector

**Files:**
- Create: `internal/extractor/sql/40_orchestrator_disagreement.sql.tmpl`
- Create: `internal/extractor/testdata/orchestrator/s.jsonl`
- Test: `internal/extractor/signals_test.go` (add disagreement case)

- [ ] **Step 1: Create `internal/extractor/sql/40_orchestrator_disagreement.sql.tmpl`**

> A `Task` tool_use (subagent), then within `DisagreeWindow` turns an assistant message whose excerpt matches the disagreement regex. Implicates the named subagent's `.md` (under `AgentsDir`).

```sql
-- orchestrator_disagreement: orchestrator text disputes a subagent's result.
WITH events AS (
  SELECT DISTINCT
    md5(d.session_id || ':' || CAST(d.turn AS VARCHAR) || ':orchestrator_disagreement') AS incident_id,
    d.session_id,
    sm.cwd AS project,
    d.ts,
    'orchestrator_disagreement' AS signal_type,
    '{{.AgentsDir}}/' || task.subagent_type || '.md' AS implicated_artifact,
    json_array('{{.AgentsDir}}/' || task.subagent_type || '.md') AS candidates,
    'low' AS confidence,
    json_object('subagent_type', task.subagent_type) AS detail,
    d.turn
  FROM tool_uses task
  JOIN tool_results tres ON tres.session_id = task.session_id AND tres.tuid = task.tuid
  JOIN rec_text d
    ON d.session_id = task.session_id
   AND d.type = 'assistant'
   AND d.turn > tres.turn
   AND d.turn - tres.turn <= {{.DisagreeWindow}}
  JOIN session_meta sm ON sm.session_id = task.session_id
  WHERE task.tool IN ('Agent', 'Task')  -- subagents spawn via Agent (this env) or Task (other CC setups)
    AND task.subagent_type IS NOT NULL
    AND NOT sm.is_subagent  -- only main-session orchestrators overrule a subagent
    -- Overrule/redo language in the assistant reaction, matched against the stringified-JSON
    -- excerpt (text + thinking) — an over-collecting heuristic, hence confidence='low'.
    AND regexp_matches(lower(d.excerpt), '{{.DisagreeRegex}}')
)
{{template "windowed_insert" .}}
```

- [ ] **Step 2: Create `internal/extractor/testdata/orchestrator/s.jsonl`**

> Turn 1 = Task(go-reviewer); turn 2 = its result; turn 3 = orchestrator disagrees ("the subagent is wrong, let me redo this"). A second Task whose follow-up is agreement must NOT flag.

```
{"type":"assistant","sessionId":"o1","uuid":"a1","timestamp":"2026-05-05T10:00:00Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"z1","name":"Task","input":{"subagent_type":"go-reviewer","description":"review diff"}}]}}
{"type":"user","sessionId":"o1","uuid":"u1","timestamp":"2026-05-05T10:00:30Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"z1","is_error":false,"content":"LGTM, no issues"}]}}
{"type":"assistant","sessionId":"o1","uuid":"a2","timestamp":"2026-05-05T10:00:35Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"text","text":"The subagent is wrong here, let me redo this myself."}]}}
{"type":"assistant","sessionId":"o2","uuid":"a3","timestamp":"2026-05-05T11:00:00Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"z2","name":"Task","input":{"subagent_type":"go-reviewer","description":"review diff"}}]}}
{"type":"user","sessionId":"o2","uuid":"u2","timestamp":"2026-05-05T11:00:30Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"z2","is_error":false,"content":"found a bug"}]}}
{"type":"assistant","sessionId":"o2","uuid":"a4","timestamp":"2026-05-05T11:00:35Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"text","text":"Great catch, applying the fix."}]}}
```

- [ ] **Step 3: Add the test to `internal/extractor/signals_test.go`**

```go
import "strings" // add to existing imports if not present

func TestOrchestratorDisagreement(t *testing.T) {
	cfg := testConfig(t, "orchestrator", "orchestrator_disagreement")
	cfg.AgentsDir = "/agents"
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := query(t, cfg.OutDB,
		"SELECT implicated_artifact FROM incidents WHERE signal_type='orchestrator_disagreement';")
	if len(rows) != 1 {
		t.Fatalf("expected 1 disagreement incident (o1 only), got %d: %v", len(rows), rows)
	}
	if got := rows[0]["implicated_artifact"].(string); !strings.Contains(got, "/agents/go-reviewer.md") {
		t.Errorf("expected implicated go-reviewer.md, got %v", got)
	}
}
```

- [ ] **Step 4: Run the test**

Run: `nix develop -c go test ./internal/extractor/ -run TestOrchestratorDisagreement -v`
Expected: PASS (only session o1 flags; o2's agreement does not).

- [ ] **Step 5: Commit**

```bash
git add internal/extractor/sql/40_orchestrator_disagreement.sql.tmpl internal/extractor/testdata/orchestrator internal/extractor/signals_test.go
git commit -m "feat(extractor): orchestrator_disagreement detector"
```

---

## Task 8: CLI wiring + summary + end-to-end test

**Files:**
- Modify: `cmd/extractor/main.go`
- Modify: `internal/extractor/run.go` (add `Summary`)
- Test: `internal/extractor/cli_test.go`

- [ ] **Step 1: Add `Summary` to `internal/extractor/run.go`**

```go
// Summary returns per-signal incident counts from a populated db.
func Summary(ctx context.Context, db string) (string, error) {
	out, err := runDuckDB(ctx, db,
		"SELECT signal_type, count(*) AS n FROM incidents GROUP BY signal_type ORDER BY signal_type;")
	if err != nil {
		return "", err
	}
	return out, nil
}
```

- [ ] **Step 2: Replace `cmd/extractor/main.go` with the real CLI**

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/noamsto/agent-smith/internal/extractor"
)

func main() {
	cfg := extractor.DefaultConfig()
	var signals string

	flag.StringVar(&cfg.CorpusGlob, "corpus", cfg.CorpusGlob, "glob of .jsonl files to mine")
	flag.StringVar(&cfg.OutDB, "out", cfg.OutDB, "output DuckDB file")
	flag.StringVar(&cfg.Since, "since", cfg.Since, "ISO8601 lower bound on record timestamp")
	flag.StringVar(&signals, "signals", "", "comma-separated signals (default: all)")
	flag.StringVar(&cfg.MemoryLimit, "memory-limit", cfg.MemoryLimit, "DuckDB memory_limit pragma (e.g. 4GB)")
	flag.IntVar(&cfg.Threads, "threads", cfg.Threads, "DuckDB threads pragma (0 = duckdb default / all cores)")
	flag.Parse()

	for _, s := range strings.Split(signals, ",") {
		if s = strings.TrimSpace(s); s != "" { // tolerate "a, b" and trailing commas
			cfg.Signals = append(cfg.Signals, s)
		}
	}

	ctx := context.Background()
	if err := extractor.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "extractor:", err)
		os.Exit(1)
	}
	summary, err := extractor.Summary(ctx, cfg.OutDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extractor: summary:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote incidents to %s\n%s", cfg.OutDB, summary)
}
```

- [ ] **Step 3: Create `internal/extractor/cli_test.go` (end-to-end over all fixtures)**

```go
package extractor

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestEndToEndAllSignals runs every detector over a combined fixture corpus and
// asserts incidents appear across multiple signal types.
func TestEndToEndAllSignals(t *testing.T) {
	// Combine all testdata fixtures + the canonical skeleton-first fixture.
	tmp := t.TempDir()
	corpus := filepath.Join(tmp, "corpus")
	if err := exec.Command("mkdir", "-p", corpus).Run(); err != nil {
		t.Fatal(err)
	}
	srcs := []string{
		filepath.Join("testdata", "base"),
		filepath.Join("testdata", "tool_error"),
		filepath.Join("testdata", "user_correction"),
		filepath.Join("testdata", "orchestrator"),
		filepath.Join("..", "..", "fixtures", "skeleton-first"),
	}
	for i, dir := range srcs {
		matches, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for j, m := range matches {
			dst := filepath.Join(corpus, filepath.Base(dir)+"-"+itoa(i)+"-"+itoa(j)+".jsonl")
			if out, err := exec.Command("cp", m, dst).CombinedOutput(); err != nil {
				t.Fatalf("cp %s: %v: %s", m, err, out)
			}
		}
	}

	cfg := DefaultConfig()
	cfg.CorpusGlob = filepath.Join(corpus, "*.jsonl")
	cfg.OutDB = filepath.Join(tmp, "incidents.db")
	cfg.Signals = nil // all
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	c := countBySignal(t, cfg.OutDB)
	for _, want := range []string{"inefficiency", "tool_error", "user_correction", "orchestrator_disagreement"} {
		if c[want] < 1 {
			t.Errorf("expected >=1 %s incident across the combined corpus, got %d", want, c[want])
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestSignalsFilter runs only one detector and confirms no others fire.
func TestSignalsFilter(t *testing.T) {
	cfg := testConfig(t, "tool_error", "inefficiency") // ask for inefficiency only
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	for sig := range c {
		if sig != "inefficiency" {
			t.Errorf("filter leaked: expected only inefficiency signals, got %q in %v", sig, c)
		}
	}
}
```

- [ ] **Step 4: Run the full test suite**

Run: `nix develop -c go test ./... -v`
Expected: all PASS.

- [ ] **Step 5: Smoke-run the CLI against the fixtures**

Run: `nix develop -c go run ./cmd/extractor --corpus 'fixtures/skeleton-first/*.jsonl' --out /tmp/smoke.db --signals inefficiency`
Expected: prints `wrote incidents to /tmp/smoke.db` followed by a one-row `inefficiency` count.
Cleanup: `gtrash put /tmp/smoke.db`

- [ ] **Step 6: Commit**

```bash
git add cmd/extractor/main.go internal/extractor/run.go internal/extractor/cli_test.go
git commit -m "feat(extractor): CLI flags, summary output, end-to-end fixture test"
```

---

## Task 9: Nix package (buildGoModule + duckdb-wrapped binary)

**Files:**
- Verify: `flake.nix` (already written in Task 1; confirm it builds and the wrapped binary finds duckdb)

- [ ] **Step 1: Build the package**

Run: `nix build .#default`
Expected: builds; `result/bin/extractor` exists. (`vendorHash = null` is correct because the module has no external deps; if Go ever adds one, the build will fail and print the expected hash to paste in.)

- [ ] **Step 2: Confirm the wrapped binary runs without duckdb on PATH**

Run: `env PATH=/usr/bin:/bin ./result/bin/extractor --corpus 'fixtures/skeleton-first/*.jsonl' --out /tmp/pkg.db --signals inefficiency`
Expected: succeeds (the `wrapProgram --prefix PATH` injects duckdb), prints the inefficiency count.
Cleanup: `gtrash put /tmp/pkg.db`

- [ ] **Step 3: Run flake checks (compiles tests with duckdb as a check input)**

Run: `nix flake check`
Expected: passes. If the Go tests run in the sandbox and need duckdb, `nativeCheckInputs = [ pkgs.duckdb ]` (set in Task 1) provides it.

- [ ] **Step 4: Commit any flake adjustments**

```bash
git add flake.nix
git commit -m "build(extractor): package binary with wrapped duckdb on PATH"
```

---

## Task 10: Real-corpus verification run + usage doc

> Verification task (not TDD). Confirms the detectors survive the real, messy corpus and records counts for the §10 threshold-tuning work. Read-only on the corpus.

**Files:**
- Create: `docs/extractor.md`

- [ ] **Step 1: Run the extractor over the real corpus (writes to a scratch db, not committed)**

Run: `nix develop -c go run ./cmd/extractor --out /tmp/real-incidents.db`
Expected: completes without a duckdb error; prints per-signal counts. Note the wall-clock time and any `ignore_errors`-skipped lines.

- [ ] **Step 2: Sanity-check the skeleton-first signal exists in the real corpus**

Run: `nix develop -c duckdb -json /tmp/real-incidents.db -c "SELECT count(*) AS n, count(DISTINCT session_id) AS sessions FROM incidents WHERE signal_type='inefficiency';"`
Expected: a non-zero count (you read whole large files in real sessions). Record the number.

- [ ] **Step 3: Spot-check implicated artifacts and candidate resolution**

Run: `nix develop -c duckdb -json /tmp/real-incidents.db -c "SELECT signal_type, implicated_artifact, count(*) AS n FROM incidents GROUP BY 1,2 ORDER BY n DESC LIMIT 20;"`
Expected: paths resolve to real CLAUDE.md / agent files. Note any that are NULL or obviously wrong (input for §10 tuning).

- [ ] **Step 4: Write `docs/extractor.md`**

````markdown
# Extractor (Track A)

Mines `~/.claude/projects/**/*.jsonl` for behavioral-glitch incidents and writes
them to `incidents.db`.

## Run

```bash
nix develop                       # provides go + duckdb
go run ./cmd/extractor            # all signals, default corpus, ./incidents.db
go run ./cmd/extractor --signals inefficiency --since 2026-05-01 --out /tmp/x.db
```

Or build and run the packaged binary (duckdb is wrapped onto PATH):

```bash
nix build .#default
./result/bin/extractor --out /tmp/incidents.db
```

## Signals

| Signal | Heuristic | Confidence |
|--------|-----------|------------|
| `inefficiency` | whole-file Read (no offset/limit) of a file ≥ `LargeFileLines` (300) | by line count |
| `tool_error` | a tool_result with `is_error=true` | medium |
| `retry` | identical tool+input within `RetryWindowTurns` (5) turns | low |
| `user_correction` | negation/interruption text within `CorrectionLookback` (2) turns after a tool_use | medium |
| `orchestrator_disagreement` | disagreement text within `DisagreeWindow` (4) turns after a `Task` result | low |

`repeated_guidance` is NOT produced here — the analyst emits it by clustering
corrections across ≥3 sessions.

## Schema (`incidents` table)

`incident_id` (md5 of `session_id:turn:signal_type`, PK — re-runs are idempotent),
`session_id`, `project`, `ts`, `signal_type`, `implicated_artifact`, `candidates`
(JSON array of alternatives), `window` (JSON transcript slice), `confidence`,
`detail` (JSON, detector-specific).

## Thresholds

All thresholds live in `internal/extractor/config.go::DefaultConfig`. Tuning them
against real false-positive rates is tracked in the design spec §10.

## Last verification run

<!-- fill in from Task 10: date, total incidents, per-signal counts, time taken -->
````

- [ ] **Step 5: Fill in the "Last verification run" section** with the counts from Steps 1–3.

- [ ] **Step 6: Clean up the scratch db and commit the doc**

```bash
gtrash put /tmp/real-incidents.db
git add docs/extractor.md
git commit -m "docs(extractor): usage guide + real-corpus verification notes"
```

---

## Self-Review (completed during planning)

**Spec coverage (§4, §8, §9):**
- §4 incident schema → Task 2 `incidents` DDL (all fields; `candidates` + `detail` added for over-collection, which §4 explicitly calls for).
- §4 signal detectors → `tool_error/retry` (Task 5), `user_correction` (Task 6), `inefficiency` (Task 4), `orchestrator_disagreement` (Task 7). `repeated_guidance` correctly excluded (analyst-side per §4/§6).
- §4 `implicated_artifact` resolution → `artifact_main` (main sessions → global + project CLAUDE.md candidates), `subagent_type` mapping (orchestrator_disagreement → agent `.md`), `session_meta.is_subagent`. Ambiguity handled by `candidates`.
- §8 repo layout → `extractor/` realized as `cmd/extractor` + `internal/extractor` with per-signal SQL modules; `fixtures/` (skeleton-first); `flake.nix`. (`signals/` "one module per detector" is honored as one SQL file per detector.)
- §9 canonical fixture → Task 4 (`fixtures/skeleton-first`), with the acceptance assertion that the global CLAUDE.md is in candidates so the later analyst can `strengthen` rather than `add`.

**Known limitations (documented, deferred — not gaps):**
- Deep attribution of signals *inside* subagent sessions (sidechain files) is best-effort; `session_meta.is_subagent` flags them but exact agent identity for non-`Task`-attributed signals is left for a later refinement. Main-session + `Task`-based attribution covers the acceptance bar.
- Threshold tuning (spec §10) is explicitly out of scope; Task 10 gathers the data for it.

**Placeholder scan:** none — every code/SQL step has complete content.

**Type consistency:** the `events` CTE column contract (10 columns, fixed order) is identical across Tasks 4–7 and matches the `windowed_insert` INSERT in Task 3; `Config` field names used in templates all exist in `config.go` (Task 2); table/view names match the base pipeline.

---

## Post-implementation deviations (recorded during execution)

These emerged during subagent-driven execution and reviews; the plan above was updated to match the shipped code:

- **`"window"` is a DuckDB reserved keyword** — quoted everywhere (incidents DDL, `windowed_insert`, test queries).
- **`json_group_array` is a macro and rejects `ORDER BY`** — the window slice uses `to_json(list(json_object(...) ORDER BY rt.turn))` (the native `list` aggregate honors `ORDER BY`), guaranteeing turn-ordering.
- **Three-valued logic on `isSidechain`** — `is_sidechain` uses `COALESCE((j->>'isSidechain')='true', false)` so absent fields read as `false` and `NOT is_subagent` filters work.
- **`orchestrator_disagreement` scoped to main sessions** — added `AND NOT sm.is_subagent`.
- **`--signals` token hygiene** — trimmed + empty-skipped in `main.go`; `renderScript` also skips empty tokens.
- **OOM on the real corpus (Task 10), and the loader rewrite that resolved it** — the original `read_csv`-based loader (lines as VARCHAR → `CAST AS JSON`) OOM'd because `read_csv` eagerly reserves a `maximum_line_size × threads` buffer (128 MiB × 16 cores ≈ 30-44 GB, OOMing *regardless of* `memory_limit`). An interim fix dropped `MaxLineSize` to 8 MiB. The **final** fix (post-review, using the duckdb-skills docs) replaced the whole hack with **`read_ndjson_objects(..., ignore_errors=true, filename=true)`** — DuckDB's purpose-built raw-NDJSON reader, which performs no schema inference *and* doesn't pre-reserve line buffers, so it streams the full corpus at default memory. This removed the `MaxLineSize` knob entirely (per-object size now bounded by duckdb's `maximum_object_size`, 16 MiB default). Verified parity: identical detection (inefficiency 146/86 sessions, retry 2318; high-volume signals drift only with live-corpus growth), ~7s. `MemoryLimit`/`Threads` (default `"8GB"`/0, validated via `(?i)^[0-9]+\s?(b|kb|mb|gb|tb)$`, exposed as `--memory-limit`/`--threads`) are retained as optional safety knobs. **`orchestrator_disagreement` calibration (resolved):** the 0-count was a structural bug — the detector keyed on a tool named `Task`, but this environment spawns subagents via **`Agent`** (no `Task` tool exists in the corpus). Fixed to `tool IN ('Agent','Task')`. With that, the join+window correctly surface candidates (verified: 8 Agent-result→reaction sequences), and the regex was retuned to overrule/redo phrasings (dropping bare `wrong`/`incorrect`/`the subagent`, which match agreement). Real count is still 0 — but now *correctly*: this user's `Agent` usage is overwhelmingly **async fan-out** (background/parallel teammate spawns whose result is "Spawned successfully"), not synchronous delegate-then-review, so genuine overrules are rare. **Remaining §10 item:** background agents report completion via a later `<task-notification>` (often >`DisagreeWindow` turns out, as a user message, not a tool_result), so the sync-anchored window can't see the orchestrator's reaction to async results — proper detection needs task-notification correlation.
