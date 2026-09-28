---
description: Scan instruction artifacts for stale file-path references, adjudicate the ambiguous ones via subagents, and merge survivors into stale-ref clusters — Track B of the agent-smith loop.
allowed-tools: Bash, Read, Write, Agent
---

You are running the **freshness** phase of the agent-smith loop (Track B). The
deterministic steps are the `analyst freshness` subcommand; the judgement step is
an ad-hoc adjudicator subagent (Agent tool) per ambiguous artifact. Artifacts land
in the cwd: `freshness.json`, an updated `clusters.json` + `clusters/`.

**Step zero, always:** run the plugin's `scripts/bootstrap.sh` — at
`<base>/scripts/bootstrap.sh` (this command's plugin root); `./scripts/bootstrap.sh`
in a dev checkout; else
`ls -t ~/.claude/plugins/cache/agent-smith/agent-smith/*/scripts/bootstrap.sh | head -1` —
and capture its stdout (one line) as `$BIN`. Prefix every `extractor`/`analyst`/`applier`
invocation with `PATH="$BIN:$PATH"` (each Bash call is a fresh shell; the prefix
also lets the binaries find `duckdb`). If bootstrap fails, stop and show its error.

Parse `$ARGUMENTS` (space-separated, case-insensitive; warn on unknown tokens):

- `repo` → narrow to the launch repo: `REPO=$(git rev-parse --show-toplevel)`,
  pass `--artifact-prefix "$REPO"` to `scan`.

Precondition: `incidents.db` exists in the cwd — if missing, run the
**agent-smith:mine** skill (Skill tool) first.

1. `PATH="$BIN:$PATH" analyst freshness scan --db incidents.db --out freshness.json`
   (plus `--artifact-prefix "$REPO"` if `repo`). Reports
   `scanned N artifact(s) (S skipped): D dead, A ambiguous-missing → freshness.json`.
2. `FRESH_DIR=$(mktemp -d /tmp/agentsmith-fresh.XXXXXX)` — deliberately outside
   `apply.md`'s `/tmp/agent-smith-*` newest-dir glob, so an apply run never picks
   this dir up by mistake. Group `freshness.json`'s `ambiguous_missing` by
   `artifact` (`jq`). For each artifact with ambiguous refs, dispatch ONE
   general-purpose adjudicator subagent (Agent tool) with a prompt carrying the
   artifact path and its refs (`id`, `path`, `line`, `rule_excerpt`), asking, per
   ref: *is this token a genuine claim that a file exists at this path in this
   repo, or an example / placeholder / file-to-be-created / runtime output /
   other-repo path / branch-or-slug token?* Only an explicit, confident "genuine
   stale claim" earns `stale`; anything uncertain is `drop` (default-drop). The
   subagent reads the artifact around each line (read-only), writes
   `[{"id","verdict":"stale"|"drop","reason"}]` to `$FRESH_DIR/adj-<i>.json`, and
   returns one line. An adjudicator that errors or writes nothing drops all its
   refs — no retry needed.
3. `PATH="$BIN:$PATH" analyst freshness merge --report freshness.json
   --adjudications-dir "$FRESH_DIR" --out clusters.json --reason-log-dir reason-log`.
   Reports `wrote N stale-ref cluster(s) (R refs) into clusters.json (P suppressed
   by reason-log)`. Remove `$FRESH_DIR` after a successful merge.
4. Report: dead refs (`artifact:line` `path`), ambiguous refs kept/dropped with
   reasons, clusters written, refs suppressed by a prior closed/rejected
   reason-log entry. Ordering note: `analyst cluster` (run by **mine**) rewrites
   `clusters.json` and prunes every `stale-ref` cluster it did not write itself —
   run freshness AFTER mine, and re-run it after any later mine.

Finally: `echo "next: /agent-smith:propose"`.
