# Extractor (Track A)

Mines `~/.claude/projects/**/*.jsonl` for behavioral-glitch incidents and writes
them to `incidents.db`.

## Run

```bash
nix develop                       # provides go + duckdb
go run ./cmd/extractor            # all signals, default corpus, ./incidents.db
go run ./cmd/extractor --signals inefficiency --since 2026-05-01 --out /tmp/x.db
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

The default config sets `memory_limit='30GB'` and `threads=4` to handle large
corpora (480 MB+, 1000+ files) without OOM. These are prepended as DuckDB pragmas
before the pipeline SQL. Override via `Config.MemoryLimit` / `Config.Threads`.

The base loader uses `ignore_errors=true` and a 128 MiB `maximum_line_size`, so
malformed or oversized lines are skipped rather than fatal.

## Last verification run

**Date:** 2026-06-01
**Corpus:** 1002 `.jsonl` files across 103 projects (~480 MB)
**Wall-clock time:** ~8 seconds
**Total incidents:** 5025

**Per-signal counts:**

| Signal | Count |
|--------|-------|
| `retry` | 2318 |
| `tool_error` | 1661 |
| `user_correction` | 900 |
| `inefficiency` | 146 |

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
