# Retry Detector Noise Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the extractor's `retry` signal fire only when an earlier identical call errored (not on intentional successful re-runs), and bump the Oracle cap default 24 → 50.

**Architecture:** `retry` is emitted by the `tool_error` SQL template via a self-join on `tool_uses` (same tool+input within N turns). Add one join to the earlier call's `tool_results` requiring `is_error = true`. Detector logic stays in DuckDB SQL; Go is unchanged except a one-line CLI default.

**Tech Stack:** Go (stdlib), DuckDB CLI, text/template SQL. Spec: `docs/superpowers/specs/2026-06-04-retry-detector-noise-design.md`.

---

## File Structure

- `internal/extractor/testdata/retry/s.jsonl` — NEW fixture: a verify-loop session (identical successful call → not a retry) + a failed-then-retried session (→ exactly 1 retry).
- `internal/extractor/signals_test.go` — add `TestRetryRequiresPriorError`.
- `internal/extractor/sql/20_tool_error.sql.tmpl` — add the `is_error` precondition to the `retry` CTE.
- `cmd/analyst/main.go` — bump `--max-incidents-per-cluster` default 24 → 50.

The existing `TestToolErrorAndRetry` (fixture `testdata/tool_error/`) already covers the error-driven positive case and must stay green (its first call errors, so its repeat still counts).

---

### Task 1: Failing negative test + fixture

**Files:**
- Create: `internal/extractor/testdata/retry/s.jsonl`
- Modify: `internal/extractor/signals_test.go`

- [ ] **Step 1: Create the fixture**

Create `internal/extractor/testdata/retry/s.jsonl` with exactly these 8 lines (each a single JSON object; no trailing blank line needed). Session `r1` repeats an identical **successful** `Bash` call; session `r2` repeats an identical call whose **first attempt errored**:

```jsonl
{"type":"assistant","sessionId":"r1","uuid":"a1","timestamp":"2026-05-04T10:00:00Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"y1","name":"Bash","input":{"command":"echo hi"}}]}}
{"type":"user","sessionId":"r1","uuid":"u1","timestamp":"2026-05-04T10:00:01Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"y1","content":"hi"}]}}
{"type":"assistant","sessionId":"r1","uuid":"a2","timestamp":"2026-05-04T10:00:05Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"y2","name":"Bash","input":{"command":"echo hi"}}]}}
{"type":"user","sessionId":"r1","uuid":"u2","timestamp":"2026-05-04T10:00:06Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"y2","content":"hi"}]}}
{"type":"assistant","sessionId":"r2","uuid":"b1","timestamp":"2026-05-04T11:00:00Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"z1","name":"Bash","input":{"command":"cat /nope"}}]}}
{"type":"user","sessionId":"r2","uuid":"v1","timestamp":"2026-05-04T11:00:01Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"z1","is_error":true,"content":"No such file"}]}}
{"type":"assistant","sessionId":"r2","uuid":"b2","timestamp":"2026-05-04T11:00:05Z","cwd":"/home/noams/p","message":{"role":"assistant","content":[{"type":"tool_use","id":"z2","name":"Bash","input":{"command":"cat /nope"}}]}}
{"type":"user","sessionId":"r2","uuid":"v2","timestamp":"2026-05-04T11:00:06Z","cwd":"/home/noams/p","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"z2","is_error":true,"content":"No such file"}]}}
```

Note: a `tool_result` with no `is_error` field parses as `is_error = false` (the base SQL computes `(c.value->>'is_error') = 'true'`), so `r1`'s results are successes.

- [ ] **Step 2: Add the test**

In `internal/extractor/signals_test.go`, add this function (it reuses the existing `countBySignal` helper and `context`/`testing` imports already in the file):

```go
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
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `nix develop -c go test ./internal/extractor/ -run TestRetryRequiresPriorError -v`
Expected: FAIL — `expected exactly 1 retry (only the error-driven repeat), got 2` (current code counts both repeats, including the successful verify-loop one).

- [ ] **Step 4: Commit**

```bash
git add internal/extractor/testdata/retry/s.jsonl internal/extractor/signals_test.go
git commit -m "test(extractor): failing test — retry must require a prior error"
```

---

### Task 2: Add the `is_error` precondition to the `retry` CTE

**Files:**
- Modify: `internal/extractor/sql/20_tool_error.sql.tmpl`

- [ ] **Step 1: Add the join**

In `internal/extractor/sql/20_tool_error.sql.tmpl`, in the `retry` CTE (the second `WITH events AS (...)` block), insert a join from the earlier call `a` to its result. Replace this exact text:

```sql
  FROM tool_uses a
  JOIN tool_uses b
    ON b.session_id = a.session_id
```

with:

```sql
  FROM tool_uses a
  JOIN tool_results ra
    ON ra.session_id = a.session_id AND ra.tuid = a.tuid AND ra.is_error
  JOIN tool_uses b
    ON b.session_id = a.session_id
```

(`tool_results` is keyed by `tuid`, one result per call → no fan-out. The existing `SELECT DISTINCT` means `b` counts if any prior identical call within the window errored. A call with no result row is excluded automatically.)

- [ ] **Step 2: Run the new test to verify it passes**

Run: `nix develop -c go test ./internal/extractor/ -run TestRetryRequiresPriorError -v`
Expected: PASS (`retry == 1` — only `r2`'s error-driven repeat counts; `r1`'s successful repeat is excluded).

- [ ] **Step 3: Run the existing retry/tool_error test to confirm no regression**

Run: `nix develop -c go test ./internal/extractor/ -run TestToolErrorAndRetry -v`
Expected: PASS — its fixture's first call (`x1`, `Bash ls /nope`) errors, so the repeat still counts → `tool_error == 2`, `retry == 1`.

- [ ] **Step 4: Commit**

```bash
git add internal/extractor/sql/20_tool_error.sql.tmpl
git commit -m "fix(extractor): retry fires only when the prior identical call errored"
```

---

### Task 3: Bump Oracle cap default 24 → 50

**Files:**
- Modify: `cmd/analyst/main.go`

- [ ] **Step 1: Change the default**

In `cmd/analyst/main.go` (in `runCluster`), change the flag default from `24` to `50`. Replace:

```go
	maxIncidents := fs.Int("max-incidents-per-cluster", 24, "cap incidents per cluster fed to the Oracle (session-stratified sample); 0 = uncapped")
```

with:

```go
	maxIncidents := fs.Int("max-incidents-per-cluster", 50, "cap incidents per cluster fed to the Oracle (session-stratified sample); 0 = uncapped")
```

- [ ] **Step 2: Verify it builds and analyst tests still pass**

Run: `nix develop -c go build ./... && nix develop -c go test ./internal/analyst/`
Expected: PASS (no test pins the default value; the cap logic is unchanged).

- [ ] **Step 3: Commit**

```bash
git add cmd/analyst/main.go
git commit -m "chore(analyst): bump --max-incidents-per-cluster default to 50"
```

---

### Task 4: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Build, vet, and run the entire suite**

Run: `nix develop -c go build ./... && nix develop -c go vet ./... && nix develop -c go test ./...`
Expected: PASS across extractor, analyst, and applier; no vet findings.

- [ ] **Step 2: Final commit if any verification fixups were needed**

```bash
git add -A
git commit -m "chore(extractor): verification fixups for retry detector fix"
```

(Skip if the working tree is clean.)
