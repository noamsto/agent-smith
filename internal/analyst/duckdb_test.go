package analyst

import (
	"context"
	"strings"
	"testing"
)

func TestRunDuckDBSmoke(t *testing.T) {
	out, err := runDuckDB(context.Background(), ":memory:", "SELECT 7 * 6 AS answer;")
	if err != nil {
		t.Fatalf("runDuckDB: %v", err)
	}
	if !strings.Contains(out, "42") {
		t.Fatalf("expected 42, got %q", out)
	}
}
