# Applier RUNBOOK — manual dispatch loop (Phase 1)

The applier binary is deterministic; the **editor** and **verify** steps are Claude
Code subagent dispatches the orchestrator drives.

> **Superseded by `/agent-smith`.** This manual loop is now automated by the
> `/agent-smith` plugin command (`commands/agent-smith.md`); keep this RUNBOOK for
> debugging individual steps and as the editor+verify reference.

## Prerequisites

- `proposals.json` + `reason-log/` from the analyst (see `docs/analyst.md`).
- `gh` authenticated; push access to the target repos.
- Built binary: `nix build .#default` (or `go run ./cmd/applier`).

## Loop

1. **Prepare the plan:**
   ```bash
   applier prepare --proposals proposals.json --out apply-plan.json \
     --settings-repo "$AGENT_SMITH_SETTINGS_REPO"
   ```
   `--settings-repo` (defaults to `$AGENT_SMITH_SETTINGS_REPO`) is the repo owning
   the Claude Code settings layers; `escalate-out-of-instructions` proposals route
   their worktree there. Review skips printed to stderr (`skip-unresolved`,
   `skip-missing-file`, `skip-unrouted` — escalation with no settings repo).

   **Dry run (review-only):** to preview every proposal without touching any repo,
   run `applier suggest --plan apply-plan.json --proposals proposals.json --out suggestions.md`
   and read `suggestions.md`. No worktrees, edits, or PRs are created.

2. **For each `ready` group (by `group_id` — ready entries sharing a repo +
   artifact land in one worktree/branch/PR):**

   a. **Open a worktree for the group:**
      ```bash
      applier open --plan apply-plan.json --group <group-id>
      ```
      Line 1 = worktree path (`$WT`); line 2 = the file every proposal edits
      (`$FILE`); lines 3+ = the group's proposal ids in apply order.

   b. **Dispatch the editor subagent once per proposal id, sequentially** (Agent
      tool) — each edit must see the prior one, so do NOT parallelize. Inline the
      prompt from `agents/editor.md`, plus that proposal's JSON (from
      `proposals.json`), `file=$FILE`, and `repo_root=$WT`. Capture each result to
      `editor-result-<id>.json` in a shared dir.

   c. **Verify gate** — dispatch on the combined worktree diff (`git -C $WT diff`):
      - Always: the `deslop` skill/agent on the diff (prose artifacts attract slop).
      - If the diff touches a hook / `settings.json` / the Nix overlay: also
        `find-bugs` and `code-review`.
      - If findings are substantive: re-dispatch the editor for the implicated
        proposal (one revision pass) with the findings appended, or append them to
        the PR body in the next step.

   d. **Submit** (one commit · push · one PR enumerating every proposal · reason-log
      link per proposal, then drop the worktree):
      ```bash
      applier submit --plan apply-plan.json --proposals proposals.json \
        --group <group-id> --worktree "$WT" --editor-result-dir <dir> \
        --reason-log-dir reason-log
      ```
      Prints the PR URL, or a skip reason if every editor declined / made no change.

3. **Commit the reason-log update** in THIS repo (the applier filled the `**PR:**`
   line):
   ```bash
   git add reason-log/ && git commit -m "docs(reason-log): link applier PRs"
   ```

## Recovering from a mid-loop failure

Phase-1 retries are not yet fully idempotent (deferred — see the spec §8):

- **`submit` failed after the commit but before/at the PR** — the branch and commit
  exist locally. Re-running `open --group <same>` will fail (`git worktree add -b`
  refuses an existing branch). Either resume `submit` against the worktree from the
  first `open`, or discard and restart: `git -C <repo> worktree remove --force <wt>`
  then `git -C <repo> branch -D <branch>`.
- **`git push` rejected (remote branch diverged)** — inspect first, then push with
  `git -C <wt> push --force-with-lease` only if you're sure the remote branch is a
  stale agent-smith attempt.

## Acceptance check — skeleton-first (the Phase-1 bar)

Prove the loop end-to-end against a throwaway repo, so no real PR is opened:

1. Make a scratch repo with a CLAUDE.md that ALREADY has a weak skeleton-first rule:
   ```bash
   tmp=$(mktemp -d); git -C "$tmp" init -b main
   printf '# Reading Code\n\nPrefer reading only the part you need.\n' > "$tmp/CLAUDE.md"
   git -C "$tmp" add -A && git -C "$tmp" -c user.email=t@t -c user.name=t commit -m seed
   git -C "$tmp" remote add origin https://github.com/noamsto/scratch.git
   ```
2. Write a one-proposal `proposals.json` with the skeleton-first `strengthen`
   proposal (artifact = `$tmp/CLAUDE.md#reading-code`) and run the analyst's
   `assemble` to seed `reason-log/`, OR hand-write the reason-log entry with the
   applier placeholder.
3. `applier prepare` → `open` → dispatch the editor → inspect `git -C $WT diff`:
   - **PASS:** the existing rule is strengthened IN PLACE (raised/made imperative);
     no duplicate "Reading Code" section is added. `deslop` reports clean.
   - **FAIL:** a second skeleton-first section appears, or the edit is slop.
4. Skip the real push/PR (no such remote); confirm the commit-message shape via
   `git -C $WT log -1` after a manual `git -C $WT commit -am test`.

This mirrors the analyst Oracle's "strengthen, don't duplicate" acceptance bar,
one hop downstream.
