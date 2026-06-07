# glitch-2026-06-07-wt-invented-flags

**Artifact:** /home/noams/Data/git/factify/mono/CLAUDE.md#worktrees  
**Fix type:** add  **Confidence:** high  **Date:** 2026-06-07

## Diagnosis

The dominant, identically-recurring tool_error in this cluster is the agent invoking `wt` with invented packed short flags — `wt -yqn <branch>`, `wt -yq <branch>`, `wt -yqn <branch>` inside `cd "$(...)"` — which `wt` rejects with `error: unexpected argument '-y' found`. `wt` has no top-level positional/flag form: branch creation must go through the `switch` subcommand (`wt switch -c <branch>`). Every occurrence forces a recovery loop of `wt --help` + `wt switch --help` before the agent can proceed, burning a turn per worktree creation. This repo's CLAUDE.md is a one-line `See @AGENTS.md` stub and carries no `wt` guidance, so nothing in the repo-local instruction surface tells the agent the correct invocation.

## Evidence

- 57ef5be4-96bd-43c0-a2ce-6b9ede7aa7c1:98
- 715cfb68-93fd-4b89-b031-2c0941eadd8c:17
- ba264db9-14e0-445c-b1f2-983dda9faf64:168
- 39dfcc35-7afe-486d-9102-24d237d3e2b1:79
- 4+ sessions

## Proposed change

```
Append to /home/noams/Data/git/factify/mono/CLAUDE.md:

--- a/CLAUDE.md
+++ b/CLAUDE.md
@@
 See @AGENTS.md
+
+## Worktrees (`wt`)
+
+`wt` has NO bare top-level form — every worktree action goes through a subcommand. Do not invent packed short flags like `-yqn`/`-yq`/`-n`; they are not accepted and `wt` exits with `error: unexpected argument '-y' found`.
+
+Create + switch to a new branch worktree:
+
+```bash
+wt switch -c <branch>      # creates branch + worktree, then navigates
+wt switch <branch>         # switch to existing (creates worktree if branch exists)
+wt switch                  # interactive picker
+wt list                    # list worktrees
+```
+
+- `-y` / `--yes` skips approval prompts; it is a flag ON `wt switch`, not a top-level flag.
+- `wt switch` handles navigation itself (a post-switch hook owns `cd`) — never wrap it as `cd "$(wt ...)"`.
+- Branch shortcuts for `<branch>`: `^` (default), `-` (previous), `@` (current), `pr:{N}`.

```

## Expected effect

Drove by the tool_error signal: the `wt -y...` failure is the only pattern that repeats with an identical root cause across 4+ distinct sessions (57ef5be4, 715cfb68, ba264db9, 39dfcc35), unlike the heterogeneous one-off denials (self-config edit, symlink write, TLS timeout) in the rest of the sample. Chose `add` not `strengthen` because artifact_content is the bare stub `See @AGENTS.md` — there is NO existing `wt` guidance in this artifact to strengthen (the global ~/.claude/CLAUDE.md documents `wt switch`, but it is clearly not preventing the error and is not this artifact). Stating the hard rule 'no bare top-level form, all actions via a subcommand' plus the canonical `wt switch -c` form makes the invented-flag invocation a non-starter, eliminating the per-worktree help-discovery recovery loop. Confidence high: consistent error string and consistent malformed input across sessions.

**PR:** https://github.com/factify-inc/mono/pull/2022

<!-- outcome appended by deja-vu -->
