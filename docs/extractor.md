# Extractor (Track A)

Mines `~/.claude/projects/**/*.jsonl` for behavioral-glitch incidents and writes
them to `incidents.db`.

## Run

```bash
nix develop                       # provides go + duckdb
go run ./cmd/extractor            # all signals, default corpus, ./incidents.db
go run ./cmd/extractor --signals inefficiency --since 2026-05-01 --out /tmp/x.db
go run ./cmd/extractor --memory-limit 8GB   # override the DuckDB memory cap
```

Or build and run the packaged binary (duckdb is wrapped onto PATH):

```bash
nix build .#default
./result/bin/extractor --out /tmp/incidents.db
```

## Signals

| Signal | Heuristic | Confidence |
|--------|-----------|------------|
| `inefficiency` | whole-file Read (no offset/limit) of a file >= `LargeFileLines` (300) | by line count |
| `tool_error` | a tool_result with `is_error=true` | medium |
| `retry` | identical tool+input within `RetryWindowTurns` (5) turns | low |
| `user_correction` | negation/interruption text within `CorrectionLookback` (2) turns after a tool_use | medium |
| `orchestrator_disagreement` | overrule/redo text within `DisagreeWindow` (4) turns after an `Agent`/`Task` subagent result (main sessions only) | low |

`repeated_guidance` is NOT produced here — the analyst emits it by clustering
corrections across >=3 sessions.

## Schema (`incidents` table)

`incident_id` (md5 of `session_id:turn:signal_type`, PK — re-runs are idempotent),
`session_id`, `project`, `ts`, `signal_type`, `implicated_artifact`, `candidates`
(JSON array of alternatives), `window` (JSON transcript slice), `confidence`,
`detail` (JSON, detector-specific).

## Thresholds

All thresholds live in `internal/extractor/config.go::DefaultConfig`. Tuning them
against real false-positive rates is tracked in the design spec §10.

## Memory / Performance

The default config sets `memory_limit='8GB'` and `threads=0` (0 = omit the pragma,
so DuckDB uses all cores). `memory_limit` is prepended as a DuckDB pragma before
the pipeline SQL and validated against `^[0-9]+(b|kb|mb|gb|tb)$` (it is interpolated
into a `SET` statement, including from the flag, so a bad value is rejected rather
than executed). Override via `--memory-limit` / `--threads`.

The base loader reads each line as a raw JSON value via
`read_ndjson_objects(..., ignore_errors=true)` — the purpose-built reader for
newline-delimited JSON, which performs no schema inference (so every field stays
accessible and timestamps aren't coerced). Unlike `read_csv`, it does not
pre-reserve a `maximum_line_size x threads` read buffer, so it streams the full
corpus at default memory without OOM (an earlier `read_csv`-based loader OOM'd
because a 128 MiB line-size reservation ballooned to 30-44 GB regardless of
`memory_limit`). Malformed/blank lines are skipped; per-object size is bounded by
DuckDB's `maximum_object_size` (16 MiB default), well above the ~0.5 MiB largest
observed line. The `memory_limit`/`threads` pragmas above are now optional safety
knobs rather than a required workaround.

## Last verification run

**Date:** 2026-06-01
**Corpus:** 1002 `.jsonl` files across 103 projects (~480 MB)
**Wall-clock time:** ~7 seconds
**Total incidents:** 5030

**Per-signal counts:**

| Signal | Count |
|--------|-------|
| `retry` | 2318 |
| `tool_error` | 1664 |
| `user_correction` | 902 |
| `inefficiency` | 146 |
| `orchestrator_disagreement` | 0 |

**`orchestrator_disagreement: 0` — diagnosed and recalibrated.** The original 0 was
a structural bug: the detector keyed on a tool named `Task`, but this environment
spawns subagents via `Agent` (the corpus contains **0** `Task` tool-uses and **637**
`Agent` uses carrying `subagent_type`). Fixed to match `Agent`/`Task`; the join +
window then correctly surface candidates (8 `Agent`-result → reaction sequences), and
the regex was retuned to overrule/redo phrasings. The count is still 0 — but now
*correctly*: this user's `Agent` usage is overwhelmingly **async fan-out** (background/
parallel teammate spawns whose result is just "Spawned successfully"), not synchronous
delegate-then-review, so genuine overrules are rare. **Remaining §10 item:** a
background agent reports completion via a later `<task-notification>` (a user message,
often beyond the 4-turn window), so detecting reactions to *async* subagent results
needs task-notification correlation, not a turn-window anchored on the spawn ack.

**Inefficiency deep-cut:** 146 incidents across 86 distinct sessions — whole-file
reads (≥300 lines, no offset/limit) are common in real sessions.

**Top implicated artifact:** `/home/noams/Data/git/factify/mono/CLAUDE.md` (2195
`retry`, 783 `tool_error`, 412 `user_correction`, 54 `inefficiency`). The `mono`
repo is by far the heaviest-use project.

**NULL implicated_artifact values:** 0 — artifact resolution is clean across all
71 distinct artifacts.

**Worktree path observations (§10 tuning input):** Several artifacts resolve to
`.worktrees/eng-*/CLAUDE.md` paths (e.g. 95 incidents pointing at
`eng-5582-doc-slice/CLAUDE.md`). These are valid — worktrees have their own CWD
and CLAUDE.md — but the file may no longer exist on disk after the branch is
deleted. The analyst should check for file existence before surfacing these as
actionable. This is tracked as a threshold-tuning concern for §10.
