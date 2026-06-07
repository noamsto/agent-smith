# glitch-2026-06-07-worktree-bash-cwd-and-edit-before-read

**Artifact:** /home/noams/Data/git/factify/mono/.worktrees/eng-5883-inbound-slack-impl/CLAUDE.md  
**Fix type:** escalate-out-of-instructions  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

The implicated CLAUDE.md is a bare redirect ('See @AGENTS.md') carrying zero operational guidance, yet it is blamed for a recurring family of harness-level tool errors. The single most consistent, deterministic failure is `cd <subdir> && ...` bash commands (e.g. `cd factapi`, `cd factapi/schema`) issued from the worktree root: because the agent thread's bash cwd resets between every call, a relative `cd` that worked once fails on the next invocation ('No such file or directory', and the doubled-path 'factapi/factapi/...' git add). The agent burns turns re-discovering cwd via `pwd`/`ls`. A secondary, equally mechanical failure is Edit-before-Read ('File has not been read yet'). Both are environmental invariants that a prose reminder cannot reliably enforce across sessions — a rule keeps being ignored because the model has no live signal about cwd state at command-construction time. This must become impossible at the harness level, not be re-described in prose.

## Evidence

- 670b5435-abbc-4043-bacd-53356073b13f:85
- 670b5435-abbc-4043-bacd-53356073b13f:64
- 670b5435-abbc-4043-bacd-53356073b13f:612
- db34b994-8cf9-431b-b846-140a706a3cf6:169
- 595bfa87-5e48-4df3-bffa-c8f1dd1fad56:154
- f4bdf800-aafd-4b96-9db4-d259faf4f8b0:145
- 4 sessions, 11 incidents

## Proposed change

```
Add a PreToolUse hook (NOT prose in this CLAUDE.md). Location: the repo/plugin hooks config that already governs this agent — e.g. a `hooks/normalize-bash-cwd.sh` referenced from the Nix `--settings` overlay (default.nix), registered as a `PreToolUse` matcher on `Bash`.

Behavior:
1. Inspect the proposed Bash `command`. If it begins with a relative `cd <path> && ...` whose `<path>` does not exist from the worktree root but DOES resolve as an absolute path under the worktree root, rewrite it to an absolute `cd /abs/worktree/<path> && ...` and let it through. This neutralizes the cwd-reset-between-calls invariant (the doubled `factapi/factapi` and `cd factapi: No such file or directory` cases).
2. If the rewrite is ambiguous (path matches nowhere), deny with a one-line reason instructing use of an absolute path — turning a silent exit-128 into an actionable, cwd-independent message.

Sketch (PreToolUse on Bash, reads tool_input.command from stdin JSON):
#!/usr/bin/env bash
set -euo pipefail
root="$CLAUDE_PROJECT_DIR"
cmd=$(jq -r '.tool_input.command')
if [[ "$cmd" =~ ^cd[[:space:]]+([^[:space:]&;|]+) ]]; then
  p="${BASH_REMATCH[1]}"
  if [[ "$p" != /* && ! -d "$p" && -d "$root/$p" ]]; then
    new="cd $root/${cmd#cd *}"
    jq -n --arg c "$new" '{hookSpecificOutput:{hookEventName:"PreToolUse",updatedInput:{command:$c}}}'
    exit 0
  fi
fi

Separately, the Edit-before-Read failures are already enforceable by the existing Read-gate (the error message itself is the gate working); no prose change in this artifact would add value, and this artifact (a one-line @AGENTS.md redirect) must not be padded with rules — leave its content untouched.
```

## Expected effect

Signal is tool_error across 4 sessions / 11 incidents. The artifact_content is a bare redirect with no guidance, so 'add'/'strengthen' on THIS file would be writing rules into a file whose sole job is to point at AGENTS.md — and prose rules are exactly what has failed here. The dominant deterministic sub-pattern (relative `cd` failing under the per-call cwd reset documented for agent threads) is a harness invariant, best 'defined out of existence' by a PreToolUse hook that rewrites relative cd to absolute, per the user's 'define errors out of existence' principle. Expected effect: eliminates the cd-no-such-directory / doubled-path retries and their follow-up pwd/ls probing turns, removing the largest share of incidents in this cluster. Confidence medium: the cluster mixes a few unrelated errors (SendMessage missing summary, Edit-before-Read) that the hook does not address, so it reduces but does not fully clear the signal.

<!-- PR link appended by the applier; outcome appended by deja-vu -->
