---
description: Scan instruction artifacts for stale file-path references, adjudicate the ambiguous ones via subagents, and merge survivors into stale-ref clusters — Track B of the agent-smith loop.
allowed-tools: Bash, Read, Write, Agent, Skill
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
**agent-smith:mine** skill (Skill tool) first. `analyst cluster` (run by
**mine**) rewrites `clusters.json` and prunes every `stale-ref` cluster it did
not write itself — run freshness AFTER mine, and re-run it after any later
mine.

1. `PATH="$BIN:$PATH" analyst freshness scan --db incidents.db --out freshness.json`
   (plus `--artifact-prefix "$REPO"` if `repo`). Reports
   `scanned N artifact(s) (S skipped): D dead, A ambiguous-missing → freshness.json`.
2. Run `mktemp -d /tmp/agentsmith-fresh.XXXXXX` **on its own** — deliberately
   outside `apply.md`'s `/tmp/agent-smith-*` newest-dir glob, so an apply run
   never picks this dir up by mistake — and capture its one-line stdout as the
   adjudications dir. Each Bash call is a fresh shell, so a shell variable does
   not survive to the next call: substitute that literal path into every
   adjudicator prompt below, the merge call in step 3, and that step's cleanup —
   never re-derive it, and never pass an empty string (the merge binary errors
   on a non-empty adjudications dir that doesn't exist, and an empty path would
   silently drop every adjudicated ref).

   Group `freshness.json`'s `ambiguous_missing` by `artifact` (`jq`). For each
   artifact with ambiguous refs, dispatch ONE **agent-smith:adjudicator**
   subagent (Agent tool) with a prompt carrying the artifact path, its refs
   (`id`, `path`, `line`, `rule_excerpt`), and the output file
   `<adjudications-dir>/adj-<i>.json` (the literal path from above). The
   subagent reads the artifact around each line (read-only), writes
   `[{"id","verdict":"stale"|"drop","reason"}]` to that file, and returns one
   line. An adjudicator that errors or writes nothing drops all its refs — no
   retry needed.
3. `PATH="$BIN:$PATH" analyst freshness merge --report freshness.json
   --adjudications-dir "<adjudications-dir>" --out clusters.json --reason-log-dir reason-log`
   (the same literal path from step 2). Reports `wrote N stale-ref cluster(s)
   (R refs) into clusters.json (P suppressed by reason-log)`. Remove the
   adjudications dir after a successful merge.
4. Report: dead refs (`artifact:line` `path`), ambiguous refs kept/dropped with
   reasons, clusters written, refs suppressed by a prior closed/rejected
   reason-log entry.

Finally: `echo "next: /agent-smith:propose (not /agent-smith:run — its mine step prunes stale-ref clusters)"`.
