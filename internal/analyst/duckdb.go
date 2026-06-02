// Package analyst turns the extractor's incidents.db into actionable proposals.
package analyst

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

// queryJSON runs a query against db in -json mode and returns the raw JSON bytes
// (a JSON array of row objects, or empty for no rows).
func queryJSON(ctx context.Context, db, sql string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, duckDBBin(), "-json", db, "-c", sql)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("duckdb -json failed: %w\nstderr: %s", err, stderr.String())
	}
	return out, nil
}
