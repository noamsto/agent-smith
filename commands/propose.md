---
description: Dispatch the Oracle per mined cluster — assembles proposals.json + reason-log entries. Review-only; no edits, no PRs.
allowed-tools: Bash, Read, Write, Agent, Skill
---

You are running the **propose** phase of the agent-smith loop. The judgement
step is the bundled **agent-smith:oracle** subagent (Agent tool); the
deterministic steps are the `analyst` binary. Artifacts land in the cwd:
`proposals.json`, `reason-log/`.

**Step zero, always:** run the plugin's `scripts/bootstrap.sh` — at
`<base>/scripts/bootstrap.sh` (this command's plugin root); `./scripts/bootstrap.sh`
in a dev checkout; else
`ls -t ~/.claude/plugins/cache/agent-smith/agent-smith/*/scripts/bootstrap.sh | head -1` —
and capture its stdout (one line) as `$BIN`. Prefix every `extractor`/`analyst`/`applier`
invocation with `PATH="$BIN:$PATH"` (each Bash call is a fresh shell; the prefix
also lets the binaries find `duckdb`). If bootstrap fails, stop and show its error.

Precondition: `clusters.json` exists in the cwd — if missing, run the
**agent-smith:mine** skill (Skill tool) first.

1. `mkdir -p /tmp/agent-smith-proposals-in`
2. For each cluster object in `clusters.json` (iterate with `jq -c '.[]'`):
   - Write that single cluster object to `/tmp/agent-smith-proposals-in/cluster-<i>.json`.
   - Dispatch the **agent-smith:oracle** subagent (Agent tool) with this prompt:
     "Read the cluster at `/tmp/agent-smith-proposals-in/cluster-<i>.json` and follow
     your instructions to produce ONE proposal. Write the JSON proposal to
     `/tmp/agent-smith-proposals-in/p-<i>.json` and return only your one-line final
     message — not the JSON." (Delete the cluster temp after.)
   - If the Oracle errors or writes no file, log a skip and continue.
3. `analyst assemble --proposals-dir /tmp/agent-smith-proposals-in --out proposals.json --reason-log-dir reason-log`
   (Pass `--date <today>` only if needed; default is today.)
4. Report the assembled proposals (id, fix_type, confidence). This phase is
   review-only — no edits, no PRs.
