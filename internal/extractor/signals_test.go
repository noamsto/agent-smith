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

func TestUserCorrectionSkipsMetaTurns(t *testing.T) {
	cfg := testConfig(t, "user_meta", "user_correction")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	// Two isMeta records (string- and array-form) carry correction-like wording
	// but are harness-injected; only the human turn 6 counts.
	if c["user_correction"] != 1 {
		t.Fatalf("expected 1 user_correction incident, got %d", c["user_correction"])
	}
}

func TestRetryExcludesEditPreconditionRecovery(t *testing.T) {
	cfg := testConfig(t, "retry_edit_recovery", "tool_error")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	// e1 re-issues the Edit after a successful Read of the same file -> recovery.
	// e2 re-issues it blind -> exactly 1 retry.
	if c["retry"] != 1 {
		t.Fatalf("expected exactly 1 retry (only the blind repeat), got %d", c["retry"])
	}
}

func TestRetryRequiresPriorError(t *testing.T) {
	cfg := testConfig(t, "retry", "tool_error")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	// r1 repeats an identical SUCCESSFUL Bash call (verify-loop) -> NOT a retry.
	// r2 repeats an identical call whose first attempt errored -> exactly 1 retry.
	if c["retry"] != 1 {
		t.Errorf("expected exactly 1 retry (only the error-driven repeat), got %d", c["retry"])
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

// A --since run used to kill the whole pipeline: two `->>` extractions ANDed in
// the WHERE clause mis-resolve on DuckDB 1.5.3 and fail to cast column j. The
// fixture also carries a timestamp-less {"type":"permission-mode",...} line, the
// record type the original error message named.
func TestSinceToleratesTimestamplessRecords(t *testing.T) {
	cfg := testConfig(t, "since_no_timestamp", "user_correction")
	cfg.Since = "2026-08-01"
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run with --since: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	// Only the September correction is in window; the May one is filtered out.
	if c["user_correction"] != 1 {
		t.Fatalf("expected 1 in-window user_correction incident, got %d", c["user_correction"])
	}
}

func TestFullMineKeepsBothTurnsDespiteTimestamplessRecord(t *testing.T) {
	cfg := testConfig(t, "since_no_timestamp", "user_correction")
	if err := Run(context.Background(), cfg); err != nil {
		t.Fatalf("Run: %v", err)
	}
	c := countBySignal(t, cfg.OutDB)
	if c["user_correction"] != 2 {
		t.Fatalf("expected 2 user_correction incidents without --since, got %d", c["user_correction"])
	}
}
