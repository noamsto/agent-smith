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

## Last-run marker (incremental re-mining)

When `--since` is **omitted**, the extractor defaults its lower bound to the
timestamp of the previous run, persisted at `<OutDB>.last-run` (e.g.
`incidents.db.last-run`, an RFC3339 line written next to the db). This makes the
deja-vu loop incremental: each re-mine only processes sessions newer than last
time. The marker is written on every successful run, stamped at run start so
records arriving mid-run are picked up next time.

- Force a full re-mine: delete `<OutDB>.last-run`, or pass `--since ""`.
- An explicit `--since <ts>` always wins over the marker.

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

`repeated_guidance` is NOT produced here — the analyst emits it by clustering
corrections across >=3 sessions. `orchestrator_disagreement` is **deferred** — see
[Deferred signals](#deferred-signals) below.

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

(`orchestrator_disagreement` is not an extractor signal — see [Deferred signals](#deferred-signals).)

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

## Deferred signals

### `orchestrator_disagreement` (removed from Phase 1)

The spec lists an "orchestrator overrules its subagent" signal — the only one that
implicates a *subagent's* `.md`. It was prototyped and then **removed**, because it
has no honest home in a cheap, deterministic extractor:

- **It's a semantic judgment, not a structural fact.** "Did the orchestrator overrule
  the subagent" depends on intent, expressed in unbounded prose. Regex can't decide it
  (poor recall on paraphrase, poor precision on benign matches like "the subagent found
  where X is *wrong*").
- **No cheap structural proxy works on real usage.** Empirically, this corpus's `Agent`
  usage is overwhelmingly **async fan-out** (background/parallel teammate spawns; 637
  `Agent` uses, only ~8 synchronous result→reaction sequences, all coordination). The
  obvious structural tell — *re-delegation* (same `subagent_type` re-spawned within K
  turns) — is exactly what parallel fan-out does normally, so it floods with false
  positives. And a background agent's tool_result is just `"Spawned successfully"`; its
  real output arrives much later as a `<task-notification>` (a user message, far outside
  any turn window), so a window anchored on the spawn can't see the overrule at all.
- **Putting an LLM in the extractor is the wrong fix.** The extractor must stay
  deterministic and free so `deja-vu` can re-mine history cheaply; an LLM call per
  re-mine breaks that. LLM judgment belongs in the analyst.

**Where the value actually lives (Phase 2):**

1. **Subagent-quality via sidechain attribution** — a deficient subagent shows up as
   glitches *in its own session*: the extractor already detects `tool_error`/`retry`/
   `inefficiency`; the missing piece is resolving a sidechain session's
   `implicated_artifact` to the subagent's `.md` (the agent identity is in the
   sidechain's opening). This answers "which subagents are failing" structurally,
   without judging intent.
2. **True overrule detection** — correlate a `<task-notification>` back to its spawning
   `Agent` call by `agent_id`, then have the **analyst** judge the orchestrator's
   reaction to the *completion*. This is analyst + correlation work, and a strong
   candidate for **Phase-3 inline capture**: a runtime hook can record the
   spawn→completion→reaction triple cleanly, with the `agent_id` in hand, instead of
   reverse-engineering it from async jsonl.
