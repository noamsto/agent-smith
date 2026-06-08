# Analyst (Phase 1)

Turns `incidents.db` (from the extractor) into improvement proposals.

## Pipeline

```
incidents.db ──► analyst cluster ──► clusters.json (index) ──► (Oracle reads ONE
                                     + clusters/<id>.json         clusters/<id>.json)
                                                              │ proposal JSON per cluster
                                                              ▼
proposals.json + reason-log/*.md ◄── analyst assemble ◄──────┘
```

The two binaries are deterministic; the Oracle (`agents/oracle.md`) is a
pure `cluster → proposal JSON` completion dispatched once per cluster. Phase-1 glue
is the eval runbook (`fixtures/analyst/RUNBOOK.md`); the `/agent-smith` command is
deferred.

## Commands

```bash
nix develop
go run ./cmd/analyst cluster  --db incidents.db --out clusters.json --min-sessions 5 --top 0 --reason-log-dir reason-log
# (writes the index clusters.json + per-cluster clusters/<id>.json)
# (dispatch the Oracle per cluster file → write proposal JSONs into ./proposals/)

go run ./cmd/analyst assemble --proposals-dir proposals --out proposals.json --reason-log-dir reason-log
```

## Deja-vu memory (skipping rejected proposals)

`cluster` reads the reason-log (`--reason-log-dir`, default `reason-log`) and
**drops any cluster whose `(artifact, signal_type)` already has an entry marked
`closed` or `rejected`** — a proposal the user declined on a prior run. Each skip
is logged to stderr (`skip <cluster_id>: a prior proposal was closed/rejected`),
never silently dropped. Matching strips an entry's `#section` suffix and resolves
symlinks, so a since-deleted worktree path still matches its canonical artifact.

The `(artifact, signal_type)` key is recorded in the entry header — the Oracle
echoes the cluster's `signal_type` into its proposal, and `assemble` writes a
`**Signal:**` line plus a machine-readable `<!-- outcome: open -->` marker that
the applier later flips to `merged`/`closed` (see `applier reconcile`).

## Clustering

Incidents are **exploded across their `candidates`**, each candidate is
**canonicalized** (a path under a git worktree — `<repo>/.worktrees/<name>/…`,
worktrunk's layout — maps back to the repo-root artifact), then grouped by
`(artifact, signal_type)`; a group is actionable at `>= --min-sessions` (default 5)
distinct sessions. Canonicalization keeps worktree copies of a file from
fragmenting into duplicate clusters. A shared artifact (e.g. the global `CLAUDE.md`)
accumulates incidents across projects, so cross-project glitches against a shared
rule converge on the right artifact. Each cluster bundles the artifact's current
file content so the Oracle can choose `strengthen` over a duplicate `add`; clusters
whose canonical artifact no longer exists on disk (deleted worktree, removed file)
are dropped, and the count is logged.

Since each cluster becomes an Oracle dispatch, the cluster count bounds the Oracle
fleet size and cost. Two complementary levers keep a run scoped to signal:

- `--min-sessions` (default 5) — the floor. A 3-session bar admitted a long tail of
  one-off noise; 5 keeps genuinely recurring multi-session glitches while dropping
  most of the tail, without zeroing out small runs the way ~10 would.
- `--top N` (default 0 = keep all) — the ceiling. After `--min-sessions` gating,
  clusters are ranked by signal strength (`distinct_sessions`, then
  `total_incidents`, with `cluster_id` as a deterministic tiebreak) and only the top
  `N` are kept. When clusters are dropped, the cluster command logs the drop count
  and the cutoff cluster's session/incident counts to stderr, so the truncation is
  never silent.

`--min-sessions` gating runs first (in SQL); `--top` ranks and truncates the gated
set.

## On-disk layout

`analyst cluster` writes **one pretty-printed file per cluster** under
`clusters/<id>.json` plus an index array at `clusters.json` (the `--out` path).
The Oracle reads only its own cluster file, so a single minified blob can no
longer blow the Read token cap. Each index entry carries `cluster_id`,
`signal_type`, `artifact`, `artifact_exists`, `distinct_sessions`,
`total_incidents`, `sampled_incidents`, and `file` (the per-cluster path,
relative to the index) — enough for the orchestrator to dispatch and summarise
without reading the bodies.

To keep a cluster file comfortably under the Read cap, the writer caps the
bloat: `artifact_content` is truncated to ~12 KB, each transcript window keeps
its last 4 turns with excerpts capped, and at most 25 incidents are written per
file. `total_incidents` stays the true count, so the Oracle still reasons from a
representative sample (`sampled_incidents`) against the real totals.

## Outputs

- `proposals.json` — validated proposals, deduped by `id` within the run
  (machine-local, gitignored). Dedup of **pending work across runs** (an open PR or
  an unresolved reason-log entry for the same artifact+behavior) is the applier's
  job, in `prepare` — see `docs/applier.md`.
- `reason-log/<date>-<slug>.md` — append-only, committed. Carries the
  `(artifact, signal_type)` key and an outcome marker; the applier later fills the PR
  link (`submit`) and reconciles the outcome merged/closed (`reconcile`). Two readers
  consume it: the applier's pending-work dedup gate (keys on the `**Artifact:**` line
  + `#section` anchor, treats an entry as pending until its outcome is recorded) and
  the analyst's deja-vu skip (keys on `(artifact, signal_type)`, drops clusters whose
  prior proposal was closed/rejected).

## Eval

- Deterministic binaries: `nix develop -c go test ./internal/analyst/`.
- Oracle (the skeleton-first acceptance bar): the on-demand runbook at
  `fixtures/analyst/RUNBOOK.md`.
