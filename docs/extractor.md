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
| `orchestrator_disagreement` | disagreement text within `DisagreeWindow` (4) turns after a `Task` result (main sessions only) | low |

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

The base loader uses `ignore_errors=true` and an 8 MiB `maximum_line_size`, so
malformed lines are skipped rather than fatal. 8 MiB was chosen deliberately: the
longest real transcript line in the corpus is ~0.5 MiB, and DuckDB sizes its CSV
read buffer eagerly at roughly `maximum_line_size x threads`. The previous 128 MiB
value made that reservation balloon to 30-44 GB and OOM on the full corpus
*regardless of* `memory_limit` — shrinking the line size is what actually fixed the
OOM. 8 MiB keeps a ~15x margin over the largest observed line.

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

**`orchestrator_disagreement: 0` (§10 tuning candidate):** No disagreement
incidents fired across the whole corpus. Zero hits suggests the detector is too
narrow — the regex or the 4-turn `DisagreeWindow` likely misses how disagreement
actually reads in main sessions (or `Task` results are rarer than assumed). Worth
revisiting the regex/window in §10.

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
