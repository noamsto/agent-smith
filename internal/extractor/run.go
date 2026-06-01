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
