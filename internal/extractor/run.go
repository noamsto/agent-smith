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
	"regexp"
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

// Summary returns per-signal incident counts from a populated db.
func Summary(ctx context.Context, db string) (string, error) {
	out, err := runDuckDB(ctx, db,
		"SELECT signal_type, count(*) AS n FROM incidents GROUP BY signal_type ORDER BY signal_type;")
	if err != nil {
		return "", err
	}
	return out, nil
}
