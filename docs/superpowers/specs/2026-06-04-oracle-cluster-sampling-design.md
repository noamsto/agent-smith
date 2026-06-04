# Oracle big-cluster ingestion — session-stratified incident sampling

> Design spec. Status: approved 2026-06-04. Unit: analyst `cluster`.

## Problem

The deterministic extractor → `analyst cluster` chain runs on the live corpus, but
the Oracle (a `prompt → JSON` subagent that diagnoses one cluster's fix) **cannot
ingest the high-signal clusters**. A full run produced clusters up to **8 MB**
(global `CLAUDE.md` `retry` = 8 MB, `tool_error` = 6.7 MB). `fixtures/analyst/RUNBOOK.md`
assumes a cluster object is inlined whole into the Oracle subagent — impossible at
that size. This is the gate before a real end-to-end PR on the high-signal artifacts.

### Root cause (verified, not assumed)

A single incident `window` is small: `WindowBefore=3 + WindowAfter=4 + 1 ≈ 8` turns,
each excerpt capped at `ExcerptChars=300` → ~3 KB per incident. **The size comes
from incident *count*, not window size.** An 8 MB cluster bundles ~2,000 incidents,
each carrying a window. `artifact_content` (the implicated file's text) is bounded —
a global `CLAUDE.md` is ~6 KB of tokens — and is *not* the pressure source.

## Goal

A high-signal cluster's Oracle input stays under a comfortable token budget
(~24 incidents ≈ ~18k tokens + artifact_content) while:
- preserving **≥1 window per distinct session** up to the cap (the ≥3-session
  diversity signal is what the Oracle reasons from),
- exposing **accurate totals** (`distinct_sessions`, `total_incidents`) so the
  Oracle knows it is reading a sample of a larger recurrence,
- **never truncating `artifact_content`** — it is the basis for the decisive
  add-vs-strengthen branch in `oracle.md`.

## Decision: sample in `analyst cluster`

The sampling lives in the cluster step; `clusters.json` becomes the Oracle-ready
sample. `incidents.db` remains the idempotent, fully-queryable archival record of
the complete evidence — there is no need to duplicate the full incident set into
`clusters.json`. Rejected alternatives:

- **Separate `condense` step** — produces a multi-MB intermediate that nothing
  consumes whole; extra plumbing for no benefit.
- **Keep all incidents inline + a `sampled` view field** — leaves `clusters.json`
  unwieldy and invites inlining the wrong field.

This *pulls complexity downward*: one output, already Oracle-ready, full record in
the DB. Sampling is done in SQL, consistent with the locked decision that
detector/SQL logic runs via the `duckdb` CLI and Go stays a thin orchestrator.

## Sampling algorithm (in `clusterSQL`)

Between the existing `gated` CTE and the final aggregation:

1. **`gated`** additionally computes `total_incidents = count(*)` and keeps
   `distinct_sessions = count(DISTINCT session_id)` — both over the **full**
   exploded set, so reported counts stay truthful regardless of sampling.
2. **`ranked`** — within each `(artifact, signal_type, session_id)`,
   `row_number()` ordered by confidence priority DESC, then `ts`, `incident_id`:

   ```sql
   row_number() OVER (
     PARTITION BY e.artifact, e.signal_type, e.session_id
     ORDER BY (CASE e.confidence WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END) DESC,
              e.ts, e.incident_id
   ) AS rn_in_session
   ```

   (`confidence` is `VARCHAR 'high'/'medium'/'low'`; alphabetical order would rank
   `low` above `medium`, hence the `CASE`.)
3. **`sampled`** — within each `(artifact, signal_type)`, `row_number()` ordered by
   `rn_in_session ASC` *first* (round-robin: take every session's best, then every
   session's 2nd-best, …), then confidence/ts/id:

   ```sql
   row_number() OVER (
     PARTITION BY artifact, signal_type
     ORDER BY rn_in_session ASC,
              (CASE confidence WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END) DESC,
              ts, incident_id
   ) AS pick
   ```
4. Final SELECT keeps rows with `pick <= maxIncidents`, aggregates them into the
   `incidents` JSON array (`list(... ORDER BY pick)`), and joins `gated` for the
   `distinct_sessions` / `total_incidents` columns.

**Behavior:** with more sessions than the cap, every slot is a distinct session
(breadth — preserves diversity); with fewer sessions, slots deepen per session.
Counts always reflect true totals.

`maxIncidents <= 0` means uncapped: Go injects a large sentinel (e.g. `2^31-1`) so
the `WHERE pick <= N` is a no-op.

## Why no per-window trimming

Cap 24 × ~3 KB ≈ 72 KB ≈ ~18k tokens of incidents, plus ~6k for a global
`CLAUDE.md`, ≈ ~24k total — comfortable. One knob (incident count) solves the
problem. Per-window trimming and an `artifact_content` size guard are **deferred**
(YAGNI): instruction artifacts are bounded, and `ExcerptChars` already caps windows
at extract time.

## Surface changes

| File | Change |
|------|--------|
| `internal/analyst/cluster.go` | `clusterSQL(minSessions, maxIncidents int)`; add `ranked`/`sampled` CTEs + `total_incidents`. `Cluster` and `clusterRow` gain `TotalIncidents int json:"total_incidents"`. `ClusterDB`/`clusterRows` thread `maxIncidents`. |
| `cmd/analyst/main.go` | `--max-incidents-per-cluster` flag on `cluster` (default **24**; `0` = uncapped). |
| `internal/analyst/oracle.md` | Input section documents `total_incidents`; note `incidents[]` is a **representative session-stratified sample** of `total_incidents` occurrences across `distinct_sessions` sessions — reason from the pattern; a missing specific example is not evidence of absence. |
| `fixtures/analyst/RUNBOOK.md` | One line noting the new flag; golden eval (3 incidents, 1/session) stays green under any cap. |

## Testing

- **`cluster_test.go`** — seed many incidents across N sessions > cap (varied
  confidence) and assert:
  - `len(incidents) == cap`,
  - every session appears at least once (stratification),
  - `TotalIncidents` and `DistinctSessions` equal the full counts,
  - within a session, higher-confidence incidents are preferred.
- A second case with sessions < cap asserts deepening (multiple incidents/session)
  and `len(incidents) == total_incidents`.
- Existing `cluster_test.go` / `assemble_test.go` updated for the new field and
  signature.
- Golden eval (`fixtures/analyst/RUNBOOK.md`) remains the end-to-end acceptance.

## Acceptance bar

On a high-signal cluster, the Oracle input (a) stays at/under the cap, (b) preserves
≥1 window per distinct session up to the cap, (c) reports accurate
`distinct_sessions` and `total_incidents`, and (d) leaves `artifact_content` whole.
