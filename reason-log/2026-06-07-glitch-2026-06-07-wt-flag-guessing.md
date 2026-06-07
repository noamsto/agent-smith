# glitch-2026-06-07-wt-flag-guessing

**Artifact:** /home/noams/.claude/CLAUDE.md#git-worktrees  
**Fix type:** strengthen  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

The Git Worktrees section documents `wt switch` and the long-form flags (`-y`/`--yes`, `-b`, `-x`), but it is being ignored as a closed vocabulary: the agent invents non-existent invocations such as `wt -yqn <branch>` (no subcommand, bundled `-q`/`-n` flags that do not exist), gets `error: unexpected argument '-y' found` / `Exit code 2`, and then burns 2-3 turns running `wt --help` and `wt switch --help` to recover. The existing rule lists what wt CAN do but does not forbid guessing flags/subcommands that aren't in the list, so wt invocations remain a recurring source of tool errors.

## Evidence

- 57ef5be4-96bd-43c0-a2ce-6b9ede7aa7c1:98
- 57ef5be4-96bd-43c0-a2ce-6b9ede7aa7c1:100
- 57ef5be4-96bd-43c0-a2ce-6b9ede7aa7c1:102
- b595c180-5f50-4323-93fd-ef9ad7421b76:23
- ≥3 sessions in a 218-session tool_error cluster

## Proposed change

```
Append to the end of the `## Git Worktrees` section (after the `Useful flags / shortcuts` list):

```
**The commands and flags listed above are the COMPLETE wt vocabulary — do not invent others.** Every wt action goes through one of the listed subcommands (`switch`, `list`, `remove`, `merge`); there is no bare `wt <branch>` form and no short flags beyond those listed (`-y`, `-b`, `-x`). Flags attach to the subcommand, e.g. `wt switch -c -y <branch>`, never `wt -yqn <branch>`. Red flags — if you catch yourself typing any of these, STOP and use the listed form instead:

| Wrong (invented) | Right |
|------------------|-------|
| `wt -yqn <branch>` / `wt <branch>` | `wt switch -c <branch>` (add `-y` to skip prompts) |
| `wt -q` / `wt -n` (no such flags) | `wt switch -y` |
| `cd "$(wt ...)"` | just `wt switch <branch>` — the post-switch hook owns navigation |

If you are unsure whether a flag exists, prefer the plain `wt switch <branch>` / `wt switch -c <branch>` form rather than guessing — do NOT spelunk through `wt --help` mid-task.
```
```

## Expected effect

Drove by the tool_error signal: among the sampled incidents the only cleanly artifact-addressable, repeatable failure is wt invocation guessing (session 57ef5be4 shows `wt -yqn` → Exit code 2 → two recovery `--help` calls). The artifact already has a Git Worktrees section, so per the hard rules this is `strengthen`, not `add`: I convert the descriptive flag list into a closed-vocabulary rule with a red-flag table so invented forms are caught before execution, and explicitly steer to the safe plain form instead of mid-task `--help` spelunking. The other sampled errors (EISDIR on directory Read, `git diff` needing `--`, ssh permission denied, TLS timeout, gh unknown JSON field, edit-before-read, self-modification block) are heterogeneous exploratory probes not governed by any single rule in this global file, so I do not over-fit the change to them. Expected effect: eliminates the recurring wt flag/subcommand errors and their multi-turn help-recovery tax; low-to-medium because the broad cluster is dominated by irreducible one-off probes that no prose rule can prevent.

**PR:** https://github.com/noamsto/nix-config/pull/10

<!-- outcome appended by deja-vu -->
