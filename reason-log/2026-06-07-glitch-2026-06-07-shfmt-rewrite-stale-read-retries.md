# glitch-2026-06-07-shfmt-rewrite-stale-read-retries

**Artifact:** /home/noams/Data/git/noamsto/lazytmux/CLAUDE.md#pre-commit-hooks  
**Fix type:** strengthen  **Confidence:** high  **Date:** 2026-06-07

## Diagnosis

The shfmt/trim-trailing-whitespace pre-commit hooks (and the commit step itself) rewrite shell, Nix, and Go files in place, so an Edit issued against a previously-Read file fails with 'File has been modified since read ... Read it again before attempting to write it', forcing a Read+Edit retry on nearly every post-format edit. A second strand of the same retry pattern: committing inside a worktree fails repeatedly with 'No .pre-commit-config.yaml file was found' because the generated dev-shell config isn't present, and the agent re-issued the identical commit 2-3 times before adding PRE_COMMIT_ALLOW_NO_CONFIG=1. The current 'Pre-commit Hooks' section only lists which hooks run; it gives no workflow guidance about hooks rewriting files or the missing-config case, so the agent re-discovers both the hard way each session.

## Evidence

- f2ad5712-66f9-42e5-b6e3-d5b9312634e5:884
- 874ec83b-1632-436e-ab96-2b1ec235295d:749
- b8f3d12f-328f-4441-8206-56a959b5bf9a:374
- a09e2144-a2f7-4cf6-a8d9-4b188d1992f5:875
- 422f89c3-63c2-4804-900d-e0437e0eb8cc:705
- 7 sessions

## Proposed change

```
## Pre-commit Hooks

Entering the dev shell (`nix develop`) installs these hooks: `statix`, `deadnix`, `alejandra` (Nix); `shellcheck`, `shfmt` (shell); `typos`, `check-merge-conflicts`, `trim-trailing-whitespace` (general).

**These hooks rewrite files in place — plan edits around that, don't fight it:**

- `shfmt` (tabs), `alejandra`, and `trim-trailing-whitespace` reformat on commit, so a `git commit` mutates the very files you just edited. After **any commit** that touched a file you intend to edit again, the prior Read is stale — **re-Read before the next Edit** instead of letting it fail with "File has been modified since read". Cheapest path: run the formatter yourself first (`shfmt -w <file>`) and Edit the post-format text once, rather than Edit-then-commit-then-re-Edit.
- Editing a worktree under `.worktrees/` that you have not yet Read in this session also produces the stale-Read error — Read first.
- Committing inside a worktree can fail with `No .pre-commit-config.yaml file was found` (the dev-shell-generated config isn't materialized there). Do **not** retry the identical commit — prefix once with `PRE_COMMIT_ALLOW_NO_CONFIG=1 git commit ...`; `nix flake check` still runs the hooks for real.
```

## Expected effect

The retry signal is dominated by 'File has been modified since read' errors whose own assistant turns name the cause ('modified by the shfmt hook', 'changed after the commit (shfmt)') across 4+ distinct sessions, plus a repeated triple-retry of a commit blocked by a missing pre-commit config. The 'Pre-commit Hooks' section already exists, so per the hard rule this is strengthen, not add. Strengthening it to state that hooks rewrite files (re-Read after commit / format first) and to handle the missing-config case in one shot should eliminate the stale-Read Read+Edit retry loop and the duplicated commit attempts, which is the bulk of total_incidents=34.

**PR:** https://github.com/noamsto/lazytmux/pull/14

<!-- outcome appended by deja-vu -->
