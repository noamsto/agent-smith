---
description: Apply proposals — editor subagent edits in a worktree, verify gate, then one draft PR per artifact group. Optional arg = one proposal id (applies its whole group).
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

`$ARGUMENTS` (optional) = a single proposal id; its **whole group** (every ready
proposal on the same artifact) is applied, so the resulting PR stays conflict-free.

The unit of work is a **group**: ready proposals sharing a `group_id` (same repo +
artifact) land in one worktree, one branch, one PR. A lone proposal is a group of
one. This avoids the guaranteed conflict of N PRs all editing the same file.

1. `applier prepare --proposals proposals.json --out apply-plan.json --settings-repo "$AGENT_SMITH_SETTINGS_REPO" --reason-log-dir reason-log --repo .`
   — `--settings-repo` routes `escalate-out-of-instructions` proposals to the repo
   owning the Claude Code settings layers; without it they are `skip-unrouted`. This
   also runs the **pending-work dedup gate**: a proposal whose artifact+behavior
   already has an open agent-smith PR or an unresolved reason-log entry is marked
   `skip-duplicate` (with a `supersedes` field) instead of opening a second
   conflicting PR. Surface these in the final report — do not silently drop them.
2. Determine the target **groups**: if an id was given, the `group_id` of that
   entry in `apply-plan.json` (read with `jq`); else every distinct `group_id`
   among entries with `status == "ready"`. `skip-duplicate` and the other `skip-*`
   statuses are not targets.
3. For each target group id:
   a. `applier open --plan apply-plan.json --group <gid>` → capture line 1 as `$WT`
      (worktree), line 2 as `$FILE`, and lines 3+ as the group's proposal ids in
      apply order.
   b. **For each proposal id in the group, in that order** (sequential — each edit
      must see the prior one): extract that proposal object from `proposals.json` to
      a temp file and dispatch the **agent-smith:editor** subagent (Agent tool) with
      the proposal temp-file path, `file=$FILE`, `repo_root=$WT`, and the instruction
      to follow its own contract: write its result JSON to
      `/tmp/agent-smith-proposals-in/editor-result-<id>.json` (this path is the
      editor's output sink — writing it is not an artifact edit, so it is fine that
      it lives outside the worktree) and return only its one-line final message, not
      the JSON. Do NOT dispatch the group's editors in parallel.
   c. **Verify gate** on `git -C "$WT" diff` (the combined group diff):
      - Always run the `deslop` skill/review on the diff.
      - If the diff touches a hook, `settings.json`, or a Nix `*.nix` overlay, also
        run `find-bugs` and `code-review`.
      - If a reviewer reports a **substantive** (Critical/Important) finding, dispatch
        `agent-smith:editor` once more for the implicated proposal with the findings
        appended (one revision pass). Otherwise carry the notes forward (PR body).
   d. `applier submit --plan apply-plan.json --proposals proposals.json --group <gid>
      --worktree "$WT" --editor-result-dir /tmp/agent-smith-proposals-in --reason-log-dir reason-log --draft`
      (always `--draft`). The PR enumerates every proposal in the group; each
      proposal's reason-log entry gets the shared PR link.
   e. If every editor declined (`applied:false`), the combined diff was empty, or any
      step failed: record a **skip** with the reason and continue to the next group.
      Never abort the whole run for one bad group.
4. After all groups: commit the reason-log link update in this repo
   (`git add reason-log/ && git commit -m "docs(reason-log): link agent-smith PRs"`).
5. Report per group: `group_id | repo | proposal ids | verify verdict | PR link or skip reason`.
   All PRs are **drafts** — tell the user to review / `nix build` / merge them at their leisure.
