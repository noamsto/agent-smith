// Package extractor mines the Claude Code .jsonl session corpus for behavioral
// glitches and writes them to an incidents.db DuckDB database.
package extractor

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"text/template"
)

//go:embed sql/*.sql.tmpl
var sqlFS embed.FS

// memLimitRe guards the memory_limit pragma against SQL injection (it is
// interpolated into a SET statement, including from the --memory-limit flag).
var memLimitRe = regexp.MustCompile(`(?i)^[0-9]+\s?(b|kb|mb|gb|tb)$`)

// duckDBBin is the duckdb executable; overridable for tests/packaging.
func duckDBBin() string {
	if b := os.Getenv("AGENT_SMITH_DUCKDB"); b != "" {
		return b
	}
	return "duckdb"
}

// runDuckDB pipes a SQL script to the duckdb CLI over stdin and returns stdout.
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
	// Prepend DuckDB runtime pragmas when configured.
	if cfg.MemoryLimit != "" {
		if !memLimitRe.MatchString(cfg.MemoryLimit) {
			return "", fmt.Errorf("invalid memory_limit %q (want e.g. 4GB)", cfg.MemoryLimit)
		}
		fmt.Fprintf(&buf, "SET memory_limit='%s';\n", cfg.MemoryLimit)
	}
	if cfg.Threads > 0 {
		fmt.Fprintf(&buf, "SET threads=%d;\n", cfg.Threads)
	}
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

// Coverage is the time span and size of the accumulated incident history.
type Coverage struct {
	First     string // earliest incident date, YYYY-MM-DD; "" when the db is empty
	Last      string // latest incident date, YYYY-MM-DD; "" when the db is empty
	Incidents int
	Sessions  int
}

// String renders the one-line history banner.
func (c Coverage) String() string {
	if c.Incidents == 0 {
		return "history: empty"
	}
	return fmt.Sprintf("history: %s \u2192 %s (%d incidents, %d sessions)",
		c.First, c.Last, c.Incidents, c.Sessions)
}

// ReadCoverage reports what span of history the database holds. The corpus on
// disk is a rolling retention window, so the db is the only record of anything
// older; printing its span is what makes a truncated history visible.
func ReadCoverage(ctx context.Context, db string) (Coverage, error) {
	out, err := exec.CommandContext(ctx, duckDBBin(), "-json", db, "-c",
		`SELECT min(substr(ts,1,10)) AS first, max(substr(ts,1,10)) AS last,
		        count(*) AS incidents, count(DISTINCT session_id) AS sessions
		 FROM incidents;`).Output()
	if err != nil {
		return Coverage{}, fmt.Errorf("read coverage from %s: %w", db, err)
	}
	var rows []struct {
		First     *string     `json:"first"`
		Last      *string     `json:"last"`
		Incidents json.Number `json:"incidents"`
		Sessions  json.Number `json:"sessions"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return Coverage{}, fmt.Errorf("decode coverage %q: %w", out, err)
	}
	if len(rows) != 1 {
		return Coverage{}, fmt.Errorf("expected 1 coverage row, got %d", len(rows))
	}
	r := rows[0]
	incidents, err := strconv.Atoi(r.Incidents.String())
	if err != nil {
		return Coverage{}, fmt.Errorf("coverage incident count %q: %w", r.Incidents, err)
	}
	sessions, err := strconv.Atoi(r.Sessions.String())
	if err != nil {
		return Coverage{}, fmt.Errorf("coverage session count %q: %w", r.Sessions, err)
	}
	c := Coverage{Incidents: incidents, Sessions: sessions}
	if r.First != nil {
		c.First = *r.First
	}
	if r.Last != nil {
		c.Last = *r.Last
	}
	return c, nil
}

// Summary returns per-signal incident counts from a populated db.
func Summary(ctx context.Context, db string) (string, error) {
	out, err := runDuckDB(ctx, db,
		"SELECT signal_type, count(*) AS n FROM incidents GROUP BY signal_type ORDER BY signal_type;")
	if err != nil {
		return "", err
	}
	return out, nil
}
