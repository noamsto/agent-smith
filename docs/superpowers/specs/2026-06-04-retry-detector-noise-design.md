# Retry detector noise fix + Oracle cap default bump

> Design spec. Status: approved 2026-06-04. Units: extractor `retry` detector, analyst CLI default.

## Problem

A live golden run (extractor → `analyst cluster` capped → Oracle on the `retry` /
global `CLAUDE.md` cluster) showed the `retry` signal is **inflated with intentional
re-run noise**. Two independent Oracle passes (24- and 50-incident samples) agreed
that ~half the `retry` incidents are duplicate *successful* calls — verify loops
(`go vet ./...` run twice), duplicate `Read`s — not glitches. `retry` reported 2,344
incidents corpus-wide; roughly half aren't problems, which pollutes the cluster the
Oracle reasons over and risks driving a CLAUDE.md change off noise.

### Root cause (verified in code)

`internal/extractor/sql/20_tool_error.sql.tmpl:22-45` defines `retry` purely
structurally: the same `tool` + `input_str` reappearing within `RetryWindowTurns`
(5) turns. **There is no requirement that the earlier call failed**, so any identical
re-issue — including intentional, successful re-runs — is counted.

## Goal

`retry` should mean what it was designed to mean: *the agent re-issued a call because
the previous attempt did not succeed.* A repeated identical call counts only if an
earlier identical call within the window **errored**.

## Decision: strict `is_error` precondition

A repeat `b` is a `retry` iff there exists an identical earlier call `a`
(same `tool` + `input_str`, `a.turn < b.turn ≤ a.turn + RetryWindowTurns`) whose
tool result has `is_error = true`.

- **Captures real failures**, including cancellations and harness rejections
  (*"File has not been read yet"*, *"Cancelled: parallel tool call … errored"*) —
  these surface AS error results, so `is_error = true` already catches them.
- **Excludes successful re-runs** (verify loops, duplicate Reads) — the noise.
- Rejected the looser "errored OR no result row" variant: for an earlier call that
  has a later identical twin, a missing result row is a rare transcript edge-artifact;
  including it trades determinism for a handful of ambiguous cases (YAGNI).

`confidence` stays `'low'` (scope discipline — revisit separately). The
incident-ID scheme (`session:turn:retry`), `detail`, `window`, and candidate
resolution are unchanged, so the detector stays idempotent.

## Implementation

### Unit 1 — `retry` CTE (`internal/extractor/sql/20_tool_error.sql.tmpl`)

Add a join from the earlier call `a` to its result, requiring an error:

```sql
FROM tool_uses a
JOIN tool_results ra
  ON ra.session_id = a.session_id AND ra.tuid = a.tuid AND ra.is_error
JOIN tool_uses b
  ON b.session_id = a.session_id
 AND b.tool = a.tool
 AND b.input_str = a.input_str
 AND b.turn > a.turn
 AND b.turn - a.turn <= {{.RetryWindowTurns}}
JOIN session_meta  sm ON sm.session_id = b.session_id
JOIN artifact_main am ON am.session_id = b.session_id
```

- `tool_results` is keyed by `tuid` (one result per call) → no fan-out.
- The existing `SELECT DISTINCT` (on `b`'s `incident_id`) means `b` counts if **any**
  prior identical call within the window errored.
- A call with no result row is naturally excluded (no matching `ra`) — exactly the
  strict semantics chosen.
- The `tool_error` half of the template (lines 1-20) is untouched.

### Unit 2 — Oracle cap default (`cmd/analyst/main.go`)

Change the `--max-incidents-per-cluster` default from `24` to `50`. One line.
Informed by the golden run: more headroom to gauge heterogeneous clusters; worst-case
~50k tokens, well within budget. Flag wiring and the uncapped sentinel are unchanged.
No test pins this default (verified), so nothing else changes.

## Testing (TDD)

- **Existing `TestToolErrorAndRetry` stays green.** Its fixture's first call (`x1`,
  `Bash ls /nope`) errors, so the repeat (`x2`) still counts → `retry == 1`,
  `tool_error == 2`. This is the *positive* (error-driven) case.
- **New `testdata/retry/s.jsonl` fixture + `TestRetryRequiresPriorError`** covering
  the *negative* case the fix introduces. The fixture has two sessions:
  - a **verify-loop** session — identical `Bash` call twice, both results
    successful (`is_error` absent/false) → contributes **0** `retry`;
  - a **failed-then-retried** session — identical call twice, first result
    `is_error: true` → contributes **1** `retry`.
  Run with signal `tool_error` (the template emits both). Assert `retry == 1`
  (proving the successful repeat was excluded while the failed one was kept).
- Full `go test ./...` stays green.

## Acceptance bar

On a corpus, `retry` no longer fires on repeated *successful* identical calls; it
fires only when an earlier identical call within the window errored. The existing
error-driven retry test passes unchanged; the new test proves successful re-runs
yield zero retries.

## Out of scope

- Bumping retry `confidence`.
- A separate "duplicate successful call" signal.
- The inefficiency-cluster PR (separate operational step).
