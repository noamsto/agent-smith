---
description: Run the agent-smith loop — mine session glitches, diagnose fixes (Oracle), and open draft PRs (Editor). Bare = full autonomous run; or pass mine|propose|apply [<id>]|status.
argument-hint: "[mine|propose|apply [<id>]|status]"
allowed-tools: Bash, Read, Write, Agent, Skill
---

You are orchestrating the **agent-smith** loop. The deterministic steps are the
`extractor`/`analyst`/`applier` binaries (on PATH). The judgement steps are the
bundled subagents `agent-smith:oracle` and `agent-smith:editor`, which you dispatch
with the Agent tool. Work from the current repo (the agent-smith checkout). All
intermediate artifacts live in the cwd: `incidents.db`, `clusters.json`,
`proposals.json`, `apply-plan.json`, and `reason-log/`.

`$ARGUMENTS` selects the phase. Empty → run the **full autonomous loop**
(`mine` → `propose` → `apply` for every ready proposal). Otherwise dispatch on the
first word: `mine`, `propose`, `apply` (optional second word = a single proposal id),
or `status`.

## mine
1. `extractor --out incidents.db`
2. `analyst cluster --db incidents.db --out clusters.json --min-sessions 3 --max-incidents-per-cluster 50`
3. Print a one-line-per-cluster summary (signal_type, artifact basename, distinct_sessions, total_incidents, sampled count) using `jq`.

## propose
Precondition: `clusters.json` exists (else run `mine` first).
1. `mkdir -p /tmp/agent-smith-proposals-in`
2. For each cluster object in `clusters.json` (iterate with `jq -c '.[]'`):
   - Write that single cluster object to `/tmp/agent-smith-proposals-in/cluster-<i>.json`.
   - Dispatch the **agent-smith:oracle** subagent (Agent tool) with this prompt:
     "Read the cluster at `/tmp/agent-smith-proposals-in/cluster-<i>.json` and follow
     your instructions to produce ONE proposal. Write ONLY the JSON object to
     `/tmp/agent-smith-proposals-in/p-<i>.json`." (Delete the cluster temp after.)
   - If the Oracle errors or writes no file, log a skip and continue.
3. `analyst assemble --proposals-dir /tmp/agent-smith-proposals-in --out proposals.json --reason-log-dir reason-log`
   (Pass `--date <today>` only if needed; default is today.)
4. Report the assembled proposals (id, fix_type, confidence). This phase is
   review-only — no edits, no PRs.

## apply [<id>]
Precondition: `proposals.json` exists (else run `propose` first).
1. `applier prepare --proposals proposals.json --out apply-plan.json`
2. Determine the targets: if `<id>` was given, just that id; else every entry with
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

## status
Report which of `incidents.db`, `clusters.json`, `proposals.json`, `apply-plan.json`,
and `reason-log/` entries exist, the cluster/proposal counts, and the next phase to run.

## Final report (full run)
Print a table: `proposal_id | repo | fix_type | verify verdict | PR link or skip reason`.
All PRs are **drafts** — tell the user to review / `nix build` / merge them at their leisure.
