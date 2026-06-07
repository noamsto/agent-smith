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
- After clustering, keep only clusters whose artifact lives in this repo:
  `jq --arg r "$REPO/" '[.[] | select(.artifact | startswith($r))]' clusters.json`
  (write back to `clusters.json`). This guarantees downstream PRs only ever
  target the launch repo.

With `all`: no corpus or artifact filter — but after step 2, STOP and show the
cluster table with a rough cost estimate (each cluster ≈ one Oracle + one
Editor + review, ~50k tokens), and ask the user which scope to proceed with
(everything / one repo / top-N) before any propose/apply phase runs.

1. `extractor --out incidents.db` (plus `--corpus` per the scope rule)
2. `analyst cluster --db incidents.db --out clusters.json --min-sessions 3 --max-incidents-per-cluster 50`
   (then the artifact filter per the scope rule)
3. Print a one-line-per-cluster summary (signal_type, artifact basename,
   distinct_sessions, total_incidents, sampled count) using `jq`.
