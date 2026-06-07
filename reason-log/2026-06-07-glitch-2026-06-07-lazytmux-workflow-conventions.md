# glitch-2026-06-07-lazytmux-workflow-conventions

**Artifact:** /home/noams/Data/git/noamsto/lazytmux/CLAUDE.md#contribution-workflow  
**Fix type:** add  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

lazytmux/CLAUDE.md is an exhaustive architecture reference but says nothing about how to actually work in the repo: when to reach for the heavyweight planning/brainstorming/debugging skills, the GitHub-issue + worktree workflow this personal repo uses (branches like feat/2-pr-window-enrichment seen in session b9e41256), or commit/branch conventions. With no project-local workflow rule, the agent repeatedly defaults to invoking superpowers skills (writing-plans, brainstorming, systematic-debugging, deslop) and process steps at moments the user does not want them, and the user corrects/interrupts (e.g. the explicit [Request interrupted by user] in session 24d7adf6 mid-grep). The recurring user_correction signal is a mismatch between the agent's default process and this repo's lightweight expectations, and the artifact provides no anchor to resolve it.

## Evidence

- 24d7adf6-0c44-407d-b473-33ca999f9c0f:29
- 6b5111c6-1a2d-42b3-900b-5d2c7c3c58fe:23
- c07ec795-19fd-4677-92af-04c774ea6620:90
- 211c3d0a-efd2-408b-a974-a9cf2e0a5e85:62
- e50af285-b5f6-422e-a890-3779f99f111d:9
- f2ad5712-66f9-42e5-b6e3-d5b9312634e5:10
- b9e41256-19e0-4066-9778-695be92d1bdf:50
- b8f3d12f-328f-4441-8206-56a959b5bf9a:8
- 2a533577-5872-4f7e-8fc2-8d296d76a8bc:220
- 209de575-b2f9-467b-baaa-09a7adcdf7e4:28
- 14 sessions / 198 incidents

## Proposed change

```
Append a new section to /home/noams/Data/git/noamsto/lazytmux/CLAUDE.md, after the '## Key Conventions' section:

## Contribution Workflow

lazytmux is a **personal repo, GitHub Issues only** (no Linear). Apply the personal-repo rules:

- **Branches:** `type/id-desc` where `id` is the GitHub issue number and `type` is a commit prefix (`feat`, `fix`, `refactor`, `chore`, `docs`) — e.g. `feat/2-pr-window-enrichment`. No issue → drop the id (`chore/bump-flake-lock`). Do NOT use Linear naming.
- **Worktrees:** use `wt switch -c <branch>` for new work; don't wrap `wt` in `cd`.
- **Validation before done:** `nix build .` and `nix flake check` (which runs the pre-commit hooks + `tests/enrich.bats`). Shell scripts must pass `shellcheck`.

### Skill usage in this repo

Most work here is a small, surgical edit to one script or `config/tmux.conf.nix` — match the surrounding file and stay in scope. **Do not auto-launch heavyweight skills.** Reach for planning/brainstorming/systematic-debugging skills only when the user asks for a plan or design, or when a bug genuinely resists a direct fix — not as a default first move on a scoped task. When in doubt, do the direct edit and let the user request more process.
```

## Expected effect

The user_correction signal recurs across 14 sessions; the session-stratified sample is dominated by the agent invoking superpowers/personal skills (writing-plans, brainstorming, systematic-debugging, deslop, nix-rebuild, commit) as a default move, with at least one explicit user interruption mid-investigation. CLAUDE.md currently has no 'how to work here' guidance at all — only architecture — so add (not strengthen) is required by the hard rules. The new Contribution Workflow section states the repo's GitHub-issue/worktree/validation conventions and, critically, scopes skill invocation to user request or genuinely stuck bugs, so the agent stops front-loading process the user keeps correcting. Expected effect: fewer interruptions/corrections around unsolicited skill launches and clearer branch/validation defaults.

**PR:** https://github.com/noamsto/lazytmux/pull/13

<!-- outcome appended by deja-vu -->
