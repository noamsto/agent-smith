---
description: Where the agent-smith loop stands — which artifacts exist, cluster/proposal counts, and the next phase to run.
allowed-tools: Bash, Read
---

You are running the **status** phase of the agent-smith loop.

**Step zero, always** (doubles as the install health check): run the plugin's
`scripts/bootstrap.sh` — at `<base>/scripts/bootstrap.sh` (this command's plugin
root); `./scripts/bootstrap.sh` in a dev checkout; else
`ls -t ~/.claude/plugins/cache/agent-smith/agent-smith/*/scripts/bootstrap.sh | head -1` —
and capture its stdout (one line) as `$BIN`. If bootstrap fails, stop and show
its error.

Report which of `incidents.db`, `clusters.json`, `proposals.json`,
`apply-plan.json`, and `reason-log/` entries exist in the cwd, the
cluster/proposal counts (`jq`), and the next phase to run
(`/agent-smith:mine` → `/agent-smith:propose` → `/agent-smith:apply`).
