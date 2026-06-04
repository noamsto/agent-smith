# Applier (Phase 1)

Turns the analyst's `proposals.json` into human-gated PRs against the repos that
own the implicated artifacts, and records the PR link in the reason-log.

## Pipeline

```
proposals.json ─► applier prepare ─► apply-plan.json ─► (per ready entry, RUNBOOK loop)
                                                          open → editor subagent
                                                          → verify (deslop/find-bugs)
                                                          → submit → PR + reason-log link
```

The binary (`prepare`/`open`/`submit`) is deterministic; the **editor**
(`agents/editor.md`) and **verify** steps are Claude Code subagent
dispatches driven by `fixtures/applier/RUNBOOK.md`. Every edit happens in an
isolated `git worktree` (branched from `origin/<base>` when that ref exists, so
unpushed local commits never leak into a PR), so live checkouts are never touched.
Phase 1 always opens a PR — never an auto-commit. Before pushing, `submit` runs a
deterministic **preflight**: a single-prefix title lint, exactly one commit over
the base, and no diff files beyond the editor's `files_changed` — failing any
check aborts instead of opening a malformed PR.

## Commands

```bash
nix develop
go run ./cmd/applier prepare --proposals proposals.json --out apply-plan.json
go run ./cmd/applier suggest --plan apply-plan.json --proposals proposals.json --out suggestions.md  # dry run: review-only, no edits/PRs
go run ./cmd/applier open    --plan apply-plan.json --id <proposal-id>     # → worktree + file path
#   (dispatch the editor subagent → editor-result.json; run the verify gate)
go run ./cmd/applier submit  --plan apply-plan.json --proposals proposals.json \
    --id <proposal-id> --worktree <path> --editor-result editor-result.json \
    --reason-log-dir reason-log
```

## Dry run

`suggest` renders a side-effect-free markdown index of what the loop *would* do —
NO git, worktrees, edits, or PRs. It joins the resolved plan with the proposals and
writes one section per `ready` proposal (where it would land — branch/base/repo —
plus the diagnosis and the Oracle's proposed change), followed by a list of skipped
entries with their status. Read `suggestions.md` to review the whole batch before
running the real `open`/`submit` loop.

## Resolution & status

`prepare` resolves each `implicated_artifact` (`path#section`) to its owning repo
(`git rev-parse`), owner class (nix-config/personal/factify-inc), and a branch
`<type>/agent-smith-<slug>` (`docs` for prose fixes, `chore` for escalate). Status:
`ready`, `skip-unresolved` (no git repo), or `skip-missing-file`
(`strengthen`/`fix-stale`/`remove` on an absent file — `add` is allowed to create).

## Eval

- Deterministic units: `nix develop -c go test ./internal/applier/`.
- Editor + verify (the skeleton-first bar): `fixtures/applier/RUNBOOK.md`.

## Deferred

factify Linear-ticket/branch naming; nix-config auto-commit (Phase 2); the
`/agent-smith` orchestration command; deja-vu outcome tracking.
