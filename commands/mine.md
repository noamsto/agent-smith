---
description: Mine Claude Code session history for recurring glitches — extractor → incidents.db, analyst → clusters.json. Scoped to the current repo by default; pass `all` for cross-repo.
allowed-tools: Bash, Read
---

You are running the **mine** phase of the agent-smith loop. The deterministic
steps are the `extractor`/`analyst` binaries; artifacts land in the cwd:
`incidents.db`, `clusters.json`.

**Step zero, always:** run the plugin's `scripts/bootstrap.sh` — at
`<base>/scripts/bootstrap.sh` (this command's plugin root); `./scripts/bootstrap.sh`
in a dev checkout; else
`ls -t ~/.claude/plugins/cache/agent-smith/agent-smith/*/scripts/bootstrap.sh | head -1` —
and capture its stdout (one line) as `$BIN`. Prefix every `extractor`/`analyst`/`applier`
invocation with `PATH="$BIN:$PATH"` (each Bash call is a fresh shell; the prefix
also lets the binaries find `duckdb`). If bootstrap fails, stop and show its error.

**Scope (default: the repo you're launched in).** Unless `$ARGUMENTS` contains
`all`, the run is scoped to the current repo, two ways:

- Compute `REPO=$(git rev-parse --show-toplevel)` and its Claude project-dir
  encoding `ENC` (replace every `/` and `.` in `$REPO` with `-`). Mine only this
  repo's sessions: pass `--corpus "$HOME/.claude/projects/${ENC}*/*.jsonl"`
  (the trailing `*` catches worktree project dirs).
- After clustering, keep only index entries whose artifact lives in this repo:
  `jq --arg r "$REPO/" '[.[] | select(.artifact | startswith($r))]' clusters.json`
  (write back to `clusters.json` — the index). The orchestrator only dispatches
  indexed clusters, so filtering the index is enough; leftover per-cluster files
  in `clusters/` are simply never read. This guarantees downstream PRs only ever
  target the launch repo.

With `all`: no corpus or artifact filter — but after step 2, STOP and show the
cluster table with a rough cost estimate (each cluster ≈ one Oracle + one
Editor + review, ~50k tokens), and ask the user which scope to proceed with
(everything / one repo / top-N) before any propose/apply phase runs.

1. `applier reconcile --reason-log-dir reason-log` — refresh prior PR outcomes
   (merged/closed) from GitHub before mining, so the next step's deja-vu skip
   sees the latest rejections. Skip only if `gh` is unauthenticated (warn, continue).
2. `extractor --out incidents.db` (plus `--corpus` per the scope rule). With
   `--since` omitted it auto-resumes from the `incidents.db.last-run` marker.
3. `analyst cluster --db incidents.db --out clusters.json --max-incidents-per-cluster 50`
   (writes the index `clusters.json` + per-cluster files under `clusters/`; then the
   artifact filter per the scope rule). `--min-sessions` defaults to 5; add `--top N`
   to cap the Oracle fleet at the N highest-signal clusters when the user picks a
   top-N scope above (the command logs the drop count and cutoff). It also drops
   clusters whose `(artifact, signal)` was already closed/rejected in the reason-log
   (logged to stderr), so a fresh `clusters.json` never re-proposes declined work.
4. Print a one-line-per-cluster summary from the index (signal_type, artifact
   basename, distinct_sessions, total_incidents, sampled_incidents) using `jq`.
