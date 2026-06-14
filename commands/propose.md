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

Precondition: the cluster index `clusters.json` exists in the cwd, alongside its
`clusters/` dir of per-cluster files — if missing, run the **agent-smith:mine**
skill (Skill tool) first.

1. Reset the proposal input dir, then create it:
   `rm -rf /tmp/agent-smith-proposals-in && mkdir -p /tmp/agent-smith-proposals-in`.
   `assemble` (step 4) globs every `*.json` here, so a prior run's leftover
   `p-*.json` — which may target *other* repos — would be swept into this run and
   open PRs against the wrong repo. Always start from an empty dir.
2. For each index entry in `clusters.json` (iterate with `jq -r '.[].file'`; each
   entry's `file` is the per-cluster JSON path relative to `clusters.json`'s dir):
   - Dispatch the **agent-smith:oracle** subagent (Agent tool) with this prompt:
     "Read the cluster at `<file>` and follow your instructions to produce ONE
     proposal. Write the JSON proposal to `/tmp/agent-smith-proposals-in/p-<i>.json`
     and return only your one-line final message — not the JSON. Read the per-cluster
     file directly — do NOT pass the whole index."
   - If the Oracle errors or writes no file, log a skip and continue.
3. **Skeptic pass — one per Oracle proposal.** A single Oracle pass turns directly
   into human triage; an unverified inference (e.g. "no guidance exists" judged
   without resolving `@AGENTS.md`) propagates to wrong PRs. For each `p-<i>.json`
   the Oracle wrote, dispatch the **agent-smith:skeptic** subagent (Agent tool):
   "Read the proposal at `/tmp/agent-smith-proposals-in/p-<i>.json` and follow your
   instructions to refute it against the actual repo. Write the verdict JSON to
   `/tmp/agent-smith-proposals-in/v-<i>.json` and return only your one-line final
   message — not the JSON."
   - If the skeptic returns `verdict: refuted`, **drop** that proposal: delete
     `p-<i>.json` so it never reaches assembly. If it errors or writes no verdict,
     treat that as refuted (default-drop on unverified) and drop the proposal too.
     Fold any `caveats` from an `upheld` verdict into the kept proposal's
     `reason_log` so they ride into the PR.
   - Record every dropped proposal (id + skeptic `reason`) for the report — surface
     them, never silently discard.
4. `analyst assemble --proposals-dir /tmp/agent-smith-proposals-in --out proposals.json --reason-log-dir reason-log`
   (Pass `--date <today>` only if needed; default is today.)
5. Report the assembled proposals (id, fix_type, confidence) AND the proposals the
   skeptic refuted (id + reason). This phase is review-only — no edits, no PRs.
