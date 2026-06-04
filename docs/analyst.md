# Analyst (Phase 1)

Turns `incidents.db` (from the extractor) into improvement proposals.

## Pipeline

```
incidents.db ──► analyst cluster ──► clusters.json ──► (Oracle subagent, per cluster)
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
go run ./cmd/analyst cluster  --db incidents.db --out clusters.json --min-sessions 3
# (dispatch the Oracle per cluster → write proposal JSONs into ./proposals/)
go run ./cmd/analyst assemble --proposals-dir proposals --out proposals.json --reason-log-dir reason-log
```

## Clustering

Incidents are **exploded across their `candidates`** and grouped by
`(artifact, signal_type)`; a group is actionable at `>= --min-sessions` (default 3)
distinct sessions. A shared artifact (e.g. the global `CLAUDE.md`) accumulates
incidents across projects, so cross-project glitches against a shared rule converge
on the right artifact. Each cluster bundles the artifact's current file content so
the Oracle can choose `strengthen` over a duplicate `add`.

## Outputs

- `proposals.json` — validated, deduped proposals (machine-local, gitignored).
- `reason-log/<date>-<slug>.md` — append-only, committed; the applier later appends
  the PR link and `deja-vu` the outcome.

## Eval

- Deterministic binaries: `nix develop -c go test ./internal/analyst/`.
- Oracle (the skeleton-first acceptance bar): the on-demand runbook at
  `fixtures/analyst/RUNBOOK.md`.
