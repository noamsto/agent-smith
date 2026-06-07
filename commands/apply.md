---
description: Apply proposals — editor subagent edits in a worktree, verify gate, then a draft PR per proposal. Optional arg = one proposal id.
argument-hint: "[<id>]"
allowed-tools: Bash, Read, Write, Agent, Skill
---

You are running the **apply** phase of the agent-smith loop. The judgement step
is the bundled **agent-smith:editor** subagent (Agent tool); the deterministic
steps are the `applier` binary. Artifacts land in the cwd: `apply-plan.json`,
`reason-log/`.

**Step zero, always:** run the plugin's `scripts/bootstrap.sh` — at
`<base>/scripts/bootstrap.sh` (this command's plugin root); `./scripts/bootstrap.sh`
in a dev checkout; else
`ls -t ~/.claude/plugins/cache/agent-smith/agent-smith/*/scripts/bootstrap.sh | head -1` —
and capture its stdout (one line) as `$BIN`. Prefix every `extractor`/`analyst`/`applier`
invocation with `PATH="$BIN:$PATH"` (each Bash call is a fresh shell; the prefix
also lets the binaries find `duckdb`). If bootstrap fails, stop and show its error.

Precondition: `proposals.json` exists in the cwd — if missing, run the
**agent-smith:propose** skill (Skill tool) first.

`$ARGUMENTS` (optional) = a single proposal id to apply.

1. `applier prepare --proposals proposals.json --out apply-plan.json`
2. Determine the targets: if an id was given, just that id; else every entry with
   `status == "ready"` in `apply-plan.json` (read with `jq`).
3. For each target id:
   a. `applier open --plan apply-plan.json --id <id>` → capture line 1 as `$WT`
      (worktree) and line 2 as `$FILE`.
   b. Extract that proposal object from `proposals.json` to a temp file. Dispatch the
      **agent-smith:editor** subagent (Agent tool) with: the proposal temp-file path,
      `file=$FILE`, `repo_root=$WT`, and the instruction to follow its own contract
      and write its result JSON to `/tmp/agent-smith-proposals-in/editor-result-<id>.json`
      (this path is the editor's output sink — writing it is not an artifact edit, so it is
      fine that it lives outside the worktree).
   c. **Verify gate** on `git -C "$WT" diff`:
      - Always run the `deslop` skill/review on the diff.
      - If the diff touches a hook, `settings.json`, or a Nix `*.nix` overlay, also
        run `find-bugs` and `code-review`.
      - If a reviewer reports a **substantive** (Critical/Important) finding, dispatch
        `agent-smith:editor` once more with the findings appended (one revision pass).
        Otherwise carry the notes forward (they go in the PR body).
   d. `applier submit --plan apply-plan.json --proposals proposals.json --id <id>
      --worktree "$WT" --editor-result /tmp/agent-smith-proposals-in/editor-result-<id>.json --reason-log-dir reason-log --draft`
      (always `--draft`).
   e. If the editor declined (`applied:false`), the diff was empty, or any step
      failed: record a **skip** with the reason and continue to the next id. Never abort
      the whole run for one bad proposal.
4. After all targets: commit the reason-log link update in this repo
   (`git add reason-log/ && git commit -m "docs(reason-log): link agent-smith PRs"`).
5. Report per target: `proposal_id | repo | fix_type | verify verdict | PR link or skip reason`.
   All PRs are **drafts** — tell the user to review / `nix build` / merge them at their leisure.
