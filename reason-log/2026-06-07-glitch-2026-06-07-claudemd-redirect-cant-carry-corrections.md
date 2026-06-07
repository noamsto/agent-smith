# glitch-2026-06-07-claudemd-redirect-cant-carry-corrections

**Artifact:** /home/noams/Data/git/factify/mono/.worktrees/chore-nango-coding-agent-skill/CLAUDE.md  
**Fix type:** escalate-out-of-instructions  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

The implicated artifact is a bare redirect: its entire content is `See @AGENTS.md`. It carries no behavioral guidance of its own, so there is no rule here to strengthen or fix-stale, and adding prose to a one-line pointer would either duplicate guidance that already lives in AGENTS.md / .claude/rules/ (a different artifact this proposal must not touch) or be ignored exactly like the rules it would duplicate. The recurring user_correction pattern across all 3 sessions is a cross-worktree context problem: the session is anchored in the `chore-nango-coding-agent-skill` worktree while the actual work spans other worktrees (eng-6014/eng-6015/eng-6016) and /tmp-staged diffs/checkouts (/tmp/eng6014.diff, /tmp/pr1938-review), and the shell cwd keeps resetting back to the anchor worktree between Bash calls (turn 555: "Shell cwd was reset to .../chore-nango-coding-agent-skill"). A prose line in CLAUDE.md cannot make that ambiguity impossible.

## Evidence

- ca002fc9-cd89-4304-b870-e4c98c4412a4:391
- ca002fc9-cd89-4304-b870-e4c98c4412a4:555
- ca002fc9-cd89-4304-b870-e4c98c4412a4:778
- 4a9928f9-e2fb-4011-83ff-6cf8bbb6cfcb:56
- 4a9928f9-e2fb-4011-83ff-6cf8bbb6cfcb:103
- 32b084c7-cfab-422b-a5be-4d0ea7f31917:81
- 32b084c7-cfab-422b-a5be-4d0ea7f31917:255
- 3 distinct sessions, 35 incidents

## Proposed change

```
Do NOT add prose to this redirect file; substantive conventions belong in AGENTS.md, not duplicated here. Instead add a deterministic SessionStart + PreToolUse(Bash) hook that surfaces the true working context so the agent stops conflating the anchor worktree with the work target.

Location: register in the Nix-generated `--settings` overlay if it references a /nix/store script path, otherwise in `home/ai/claude-code/settings.json` under `hooks` (per the global settings-architecture rule). Hook script (e.g. `~/.claude/hooks/worktree-context.sh`, shellcheck-clean, fish-invoked but POSIX body):

  #!/usr/bin/env bash
  # SessionStart + PreToolUse(Bash): emit the resolved worktree so the agent
  # does not assume the anchor worktree is the work target.
  set -euo pipefail
  root=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
  branch=$(git -C "$root" symbolic-ref --quiet --short HEAD 2>/dev/null || echo DETACHED)
  printf 'WORKTREE_CONTEXT root=%s branch=%s cwd=%s\n' "$root" "$branch" "$PWD"

Wire it as a SessionStart hook (prints the anchor once) and a PreToolUse matcher on Bash (re-prints before each shell call, since cwd resets between calls). This makes "which worktree am I in / is this the work target?" answerable from tool output rather than from a prose rule that keeps getting corrected.
```

## Expected effect

Decisive branch: artifact_content is `See @AGENTS.md` — a pure redirect with zero behavioral guidance, so `add` would either duplicate rules that already live in AGENTS.md/.claude/rules/ (the exact failure mode this system prevents) or be ignored like them, and there is nothing concrete to `strengthen`/`fix-stale`/`remove`. The user_correction signal recurs across 3 sessions/35 incidents around work that spans multiple worktrees and /tmp diffs while the shell cwd resets to the anchor worktree (turn 555). A prose rule in a one-line pointer cannot make cross-worktree ambiguity impossible, so the correct fix is to escalate to a SessionStart + PreToolUse(Bash) hook that deterministically emits resolved git root/branch/cwd. Expected effect: the agent always sees its true work target before acting, removing the recurring corrections. Confidence is medium because the sampled windows show the launch/dispatch context around each correction rather than the verbatim correction text, but the cwd-reset + multi-worktree pattern is consistent across all 3 sessions and the artifact being a bare redirect is dispositive for ruling out add/strengthen.

<!-- PR link appended by the applier; outcome appended by deja-vu -->
