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
