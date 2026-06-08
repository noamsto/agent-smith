# Applier (Phase 1)

Turns the analyst's `proposals.json` into human-gated PRs against the repos that
own the implicated artifacts, and records the PR link in the reason-log.

## Pipeline

```
proposals.json ─► applier prepare ─► apply-plan.json ─► (per group, RUNBOOK loop)
                                                          open → editor subagent ×N (same worktree)
                                                          → verify (deslop/find-bugs)
                                                          → submit → one PR + reason-log link per proposal
```

The unit of work is a **group**: every ready proposal that targets the same
artifact in the same repo shares one worktree, one branch, and one PR (issue #9).
One-PR-per-proposal against a shared file is a guaranteed conflict — three PRs all
appending to the same one-line `CLAUDE.md` cannot all merge. A single proposal is
just a group of one, so the common case is unchanged.

The binary (`prepare`/`open`/`submit`) is deterministic; the **editor**
(`agents/editor.md`) and **verify** steps are Claude Code subagent
dispatches driven by `fixtures/applier/RUNBOOK.md`. Each group gets one isolated
`git worktree` (branched from `origin/<base>` when that ref exists, so unpushed
local commits never leak into a PR); the editor is dispatched once per proposal
**into that same worktree, in plan order**, so each edit sees the prior one and
they don't clobber each other. Live checkouts are never touched. Phase 1 always
opens a PR — never an auto-commit. Before pushing, `submit` runs a deterministic
**preflight**: a single-prefix title lint, exactly one commit over the base, and no
diff files beyond the union of every editor's `files_changed` — failing any check
aborts instead of opening a malformed PR. The PR title/body enumerate every
proposal the group carries (id + summary), and each proposal's reason-log entry
gets the shared PR link.

Commits run with `PRE_COMMIT_ALLOW_NO_CONFIG=1` so a fresh worktree lacking
`.pre-commit-config.yaml` does not hard-fail a repo whose commit hook invokes
pre-commit. When `submit` fails, the worktree is **preserved** (it holds the
applied edit) and its path is printed, so the orchestrator can retry without
losing the editor's work; it is dropped only on success or a clean no-op. `open`
**reuses** an orphan branch left empty by such a failed run (resetting it onto
the base), and refuses to reset a branch that carries its own commits.

## Commands

```bash
nix develop
go run ./cmd/applier prepare --proposals proposals.json --out apply-plan.json \
    --settings-repo "$AGENT_SMITH_SETTINGS_REPO"   # repo owning the settings layers; escalations route here
go run ./cmd/applier suggest --plan apply-plan.json --proposals proposals.json --out suggestions.md  # dry run: review-only, no edits/PRs
go run ./cmd/applier open    --plan apply-plan.json --group <group-id>     # → worktree + file + proposal ids
#   (dispatch the editor subagent once per id into the worktree → editor-result-<id>.json; run the verify gate)
go run ./cmd/applier submit  --plan apply-plan.json --proposals proposals.json \
    --group <group-id> --worktree <path> --editor-result-dir <dir-of-editor-result-<id>.json> \
    --reason-log-dir reason-log
```

`open` prints the worktree path (line 1), the file every proposal in the group
edits (line 2), and the group's proposal ids in apply order (lines 3+). `submit`
reads `editor-result-<id>.json` for each of those ids from `--editor-result-dir`.

## Dry run

`suggest` renders a side-effect-free markdown index of what the loop *would* do —
NO git, worktrees, edits, or PRs. It joins the resolved plan with the proposals and
writes one section per **group** (one PR: where it would land — branch/base/repo —
and the proposals it carries, each with its diagnosis and the Oracle's proposed
change), followed by a list of skipped entries with their status. Read
`suggestions.md` to review the whole batch before running the real `open`/`submit`
loop.

## Resolution, grouping & status

`prepare` resolves each `implicated_artifact` (`path#section`) to its owning repo
(`git rev-parse`) and owner class (nix-config/personal/factify-inc). Ready entries
are then **grouped** by their resolved `(repo, artifact)` pair; each group gets a
shared `group_id` and a branch `<type>/agent-smith-<repo-artifact-slug>` (`docs` for
prose fixes, `chore` when any member is an escalate). Status:
`ready`, `skip-unresolved` (no git repo), or `skip-missing-file`
(`strengthen`/`fix-stale`/`remove` on an absent file — `add` is allowed to create).
Skipped entries belong to no group.

`escalate-out-of-instructions` proposals are special: the proposed hook/permission/
default belongs in a Claude Code settings layer (the `--settings` overlay at
`home/ai/claude-code/default.nix`, or `settings.json`), which lives in the
**settings-owning repo** (nix-config), NOT in the repo whose CLAUDE.md surfaced the
glitch. `prepare` routes their `repo_root` to `--settings-repo`
(`$AGENT_SMITH_SETTINGS_REPO`) so the editor lands the change in a worktree of that
repo. When no settings repo is configured (or it is not a git repo), the proposal is
marked `skip-unrouted` with a routing `reason` — surfaced on stderr and in
`suggestions.md` — instead of dispatching an editor that would predictably decline.

## Eval

- Deterministic units: `nix develop -c go test ./internal/applier/`.
- Editor + verify (the skeleton-first bar): `fixtures/applier/RUNBOOK.md`.

## Deferred

factify Linear-ticket/branch naming; nix-config auto-commit (Phase 2); the
`/agent-smith` orchestration command; deja-vu outcome tracking.
