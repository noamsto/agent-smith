package extractor

import (
	"context"
	"strings"
	"testing"
)

func countBySignal(t *testing.T, db string) map[string]int {
	t.Helper()
	rows := query(t, db, "SELECT signal_type, count(*) AS n FROM incidents GROUP BY signal_type;")
	m := map[string]int{}
	for _, r := range rows {
		if v, ok := r["n"].(float64); ok {
			m[r["signal_type"].(string)] = int(v)
		}
	}
	return m
}

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

func TestToolErrorAndRetry(t *testing.T) {
	cfg := testConfig(t, "tool_error", "tool_error")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	if c["tool_error"] != 2 {
		t.Errorf("expected 2 tool_error incidents, got %d", c["tool_error"])
	}
	if c["retry"] != 1 {
		t.Errorf("expected exactly 1 retry incident, got %d", c["retry"])
	}
}
