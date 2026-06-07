---
description: Mine Claude Code session history for recurring glitches — extractor → incidents.db, analyst → clusters.json.
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

1. `extractor --out incidents.db`
2. `analyst cluster --db incidents.db --out clusters.json --min-sessions 3 --max-incidents-per-cluster 50`
3. Print a one-line-per-cluster summary (signal_type, artifact basename,
   distinct_sessions, total_incidents, sampled count) using `jq`.
