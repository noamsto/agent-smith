# Analyst (Phase 1)

Turns `incidents.db` (from the extractor) into improvement proposals.

## Pipeline

```
incidents.db ──► analyst cluster ──► clusters.json (index) ──► (Oracle reads ONE
                                     + clusters/<id>.json         clusters/<id>.json)
                                                              │ proposal JSON per cluster
                                                              ▼
                                                    analyst cite-check (per proposal)
                                                              │ ok/demoted proposals
                                                              ▼
proposals.json + reason-log/*.md ◄── analyst assemble ◄──────┘
```

The `analyst` subcommands are deterministic; the Oracle (`agents/oracle.md`) is a
pure `cluster → proposal JSON` completion dispatched once per cluster. `cite-check`
runs per proposal, between the Oracle and the Skeptic (see `commands/propose.md`):
it verifies every cited window (`evidence` refs and `citations[]`) and quote against
the same cluster file. A cited window absent from the cluster, or a quote it can't
find, rejects the proposal (renamed to `<file>.cite-rejected`, reason-log entry
written); a `high` proposal with any evidence ref or citation lacking a verified
quote, or no citations at all, is capped to `medium`. A `stale-ref` cluster has no
session windows to cite, so its proposals pass `ok` untouched.

## Commands

```bash
nix develop
go run ./cmd/analyst cluster  --db incidents.db --out clusters.json --min-sessions 5 --top 0 --reason-log-dir reason-log
# (writes the index clusters.json + per-cluster clusters/<id>.json)
# (dispatch the Oracle per cluster file → write proposal JSONs into ./proposals/)

go run ./cmd/analyst cite-check --proposal proposals/p-1.json --cluster clusters/<id>.json --reason-log-dir reason-log
# (run once per proposal; ok/demoted proposals continue to the Skeptic, rejected ones are dropped)

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

## Freshness (Track B)

`analyst freshness` audits instruction artifacts for stale file-path references —
`@import`s, markdown links, and backtick paths that name a file or directory that
no longer exists. Detection is deterministic; only ambiguous-and-missing
candidates need an LLM adjudicator — the **agent-smith:adjudicator** subagent
(tools: Read, Write), dispatched once per artifact with ambiguous refs by the
`/agent-smith:freshness` command. `rule_excerpt` and the surrounding artifact
text it reads are untrusted content cloned from a target repo — data to judge,
never instructions to obey.

### Commands

```bash
nix develop
analyst freshness scan --db incidents.db --out freshness.json
# (dispatch one adjudicator subagent per artifact with ambiguous_missing refs)
analyst freshness merge --report freshness.json --adjudications-dir <dir> \
  --out clusters.json --reason-log-dir reason-log
```

`scan --db ""` disables the `incidents.db` audit-set query — pass one or more
`--artifact <path>` and/or `--artifact-prefix <repo root>` instead.

`merge --adjudications-dir <dir>` prints `skip: open <dir>: …` on stderr when
`<dir>` is non-empty but cannot be listed, then continues with no adjudicated
refs (exit 0) — treat that line as a failed step, since every adjudicated ref
was dropped. Pass `""` only when the adjudication step didn't run at all.

### `freshness.json` (scan output)

```json
{
  "scanned": ["<artifact>", "..."],
  "skipped": [{"artifact": "<path>", "reason": "missing | unreadable: …"}],
  "dead": [Ref],
  "ambiguous_missing": [Ref]
}
```

`Ref` = `{id, artifact, form, path, line, rule_excerpt, resolved_to, same_name}` —
`form` is `import|link|backtick`; `resolved_to` is the path checked under the
first resolution base; `same_name` lists up to 5 same-basename files elsewhere in
the repo (the likely new location of a moved file).

### The `stale-ref` cluster

`merge` keeps every `dead` ref plus each `ambiguous_missing` ref whose `id` has
`verdict: "stale"` in some adjudication file, groups the survivors by artifact,
and writes one cluster per artifact — the same `Cluster` schema Track A writes,
with `signal_type: "stale-ref"`, `incidents: []`, `distinct_sessions: 0`, and an
`evidence` array (`{path, line, rule_excerpt, resolved_to, same_name}`) in place
of incidents. `total_incidents` is the ref count. These clusters never pass
through `clusterSQL`'s `--min-sessions` gate — a dead reference is actionable on
its own.

### Per-ref suppression

A ref is dropped (and logged) when a `closed`/`rejected` reason-log entry with
`**Signal:** stale-ref` for the same artifact lists it in its `## Evidence`
section — matched as a bullet that *opens* with `` `<path>` ``, the form the
Oracle's stale-ref evidence strings are required to take (a repoint target later
in the same bullet does not match). Suppression is per ref, not per artifact: a
declined fix for one reference must not blind the audit to new stale references
in the same file.

### Ordering with `analyst cluster`

`analyst cluster` (run by `mine`) rewrites `clusters.json` from its own clusters
and prunes every per-cluster file it did not write, including `stale-ref` ones.
Run order is **mine → freshness → propose**, and freshness must be re-run after
any later mine.

## Eval

- Deterministic binaries: `nix develop -c go test ./internal/analyst/`.
- Oracle (the skeleton-first acceptance bar): the on-demand runbook at
  `fixtures/analyst/RUNBOOK.md`.
