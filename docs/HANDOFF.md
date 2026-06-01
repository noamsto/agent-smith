# Handoff — agent-smith

> For a fresh Claude Code session started inside this repo. Read this, then the spec.

## Where things stand (2026-06-01)

- **Repo:** `github.com/noamsto/agent-smith` (public). Local: `~/Data/git/noamsto/agent-smith`.
- **Design:** complete & approved via a brainstorm. Lives in
  [`docs/specs/2026-06-01-agent-smith-design.md`](specs/2026-06-01-agent-smith-design.md). **Read it first.**
- **README:** written (Matrix-themed, Mermaid pipeline diagram, badges).
- **Code:** none yet. Nothing under `extractor/`, `freshness/`, `analyst/`, `applier/` exists.
- **Next step:** turn the **Phase 1 (MVP)** spec into an implementation plan
  (use the `superpowers:writing-plans` skill), then build.

## What agent-smith is (one paragraph)

A meta-agent that improves the instruction artifacts steering Claude Code agents
(subagent `.md`, skills, `CLAUDE.md`, slash commands). **Two tracks feed one
analyst feed one cross-repo applier.** Track A mines `~/.claude/projects/**/*.jsonl`
session history with duckdb/jq for behavioral *glitches* (errors, corrections,
inefficiency, orchestrator-overrules-subagent). Track B audits the artifacts'
*external claims* (tool/flag/API/URL freshness) by fanning out explorer agents
(WebSearch/WebFetch/context7). The analyst (Opus) clusters, applies a ≥3-session
threshold, diagnoses a `fix_type`, and emits proposals + reason logs. The applier
opens a PR against whichever repo owns the artifact. `deja-vu` later re-mines to
confirm the glitch rate dropped.

## Phase 1 scope (what to build first)

1. **Extractor** (`extractor/`, no LLM) — duckdb/jq over the jsonl corpus →
   `incidents.db`. Five signal detectors, one module each under `extractor/signals/`.
   `implicated_artifact` resolution. See spec §4.
2. **Freshness** (`freshness/`) — claim extraction from artifacts + explorer fan-out
   → verdicts. See spec §5.
3. **Analyst** (`analyst/`) — sensei subagent prompt + cluster/diagnose → proposals.
   See spec §6 (incl. the fix-type taxonomy).
4. **Applier** (`applier/`) — proposals → cross-repo edits + `gh pr create`
   (gated, Phase 1). Reason logs into `reason-log/`. See spec §7.

**Acceptance bar (the canonical fixture):** the *skeleton-first whole-file-read*
glitch. The global `CLAUDE.md` already has a "Reading Code (skeleton-first)" rule,
yet agents read whole large files. Extractor must flag those as `inefficiency`
incidents; analyst must trace them to the *existing* rule and choose `strengthen`
(or `escalate-out-of-instructions` → a hook), **not** a duplicate `add`. Build this
as a fixture under `fixtures/`. See spec §9.

## Key decisions already locked

- Output: Phase 1 = PR-gated. Phase 2 = autonomous loop (auto-commit nix-config
  artifacts only; **never** auto-commit to factify/work repos).
- Validation: reason-log + trend (`deja-vu`), deferred to Phase 2.
- Cross-repo PRs honor branch-naming rules (Linear naming for factify-inc repos).
- agent-smith may propose **hooks**, not just prose (`escalate-out-of-instructions`).
- `incidents.db`, `proposals.json`, `corpus/` are machine-local (already gitignored).
- Reason logs live in THIS repo (`reason-log/`), not the target repos.

## Environment notes

- Corpus to mine: `~/.claude/projects/**/*.jsonl` (~929 files, ~463MB at design time).
- jsonl line types seen: `assistant`, `user`, `system`, `attachment`, `last-prompt`,
  `permission-mode`, `pr-link`, `ai-title`, `queue-operation`, `file-history-snapshot`.
  Errors: `user.message.content[].tool_result.is_error == true`.
  Subagents: `assistant` tool_use `name == "Task"`, `.input.subagent_type`.
- nix-config (the main consumer) is at `~/nix-config`; agents live in
  `home/ai/claude-code/agents/`, skills in `home/ai/claude-code/skills/`.
- Still need: `flake.nix` (devshell: duckdb, jq, nodejs), repo scaffolding dirs.

## First move for the new session

Read the spec, then invoke `superpowers:writing-plans` to produce a Phase 1
implementation plan. Do **not** start coding before the plan exists.
