# agent-smith — Design

> An Agent whose purpose is propagating improvements into other agents.
> It patrols the agents-matrix, finds the glitches where reality stuttered
> (a correction, a tool error, an orchestrator overruling a subagent, a stale
> claim), and rewrites the instructions so that particular déjà vu stops
> happening.

**Status:** Design approved (brainstorm), pending implementation plan.
**Date:** 2026-06-01
**Home:** standalone repo (`github.com/noamsto/agent-smith`), consumed by `nix-config`.

---

## 1. Purpose & scope

agent-smith improves the **instruction artifacts** that steer Claude Code agents
by learning from how those agents actually behaved, and by checking whether what
the artifacts claim is still true in the world.

**Target artifacts** (anything that instructs an agent):
- Subagent `.md` files (`go-reviewer`, `nixos-expert`, `security-reviewer`, …)
- Skill `.md` files (`nix-rebuild`, `thinkfan-tuning`, …)
- `CLAUDE.md` files (global + per-project)
- Slash-command definitions
- …and other instruction-bearing files as they appear (design stays generic)

These targets are **cross-repo**: global artifacts live in `nix-config`, but
per-project `CLAUDE.md`/agents live in work repos (e.g. `factify/mono`). agent-smith
operates on whichever repo owns the implicated artifact.

**Out of scope:** changing agent *code/harness*; editing non-instruction files.

---

## 2. Operating model & phasing

| Aspect | Decision |
|--------|----------|
| Output mode | **Phase 1:** auto-edit → **PR** (human-gated). **Phase 2:** autonomous loop (auto-commit nix-config-owned artifacts only). |
| Audit trail | Reason logs from day one — every change records what & why. |
| Validation | Reason-log + trend (`deja-vu`): re-mine later sessions, compare incident rate per artifact. |

**Phases:**
- **Phase 1 (MVP):** Both intelligence tracks (A + B) → analyst → applier-as-PR + reason logs. Manual trigger. Validated against the skeleton-first fixture.
- **Phase 2:** `deja-vu` trend validation; flip nix-config artifacts to auto-commit; scheduled runs (the autonomous loop).
- **Phase 3 (later):** inline capture hook to cheapen ongoing corpus mining (the "Track A optimization").

---

## 3. Architecture

Two intelligence tracks feed one analyst, which feeds one cross-repo applier.
Tracks map directly onto the two original feature requests.

```
                         TRACK A — corpus mining (feature #2)
  .jsonl corpus ──► extractor (duckdb/jq) ──► incidents.db ──┐
  (~929 sessions, 463MB)   cheap, deterministic              │
                                                             ▼
                                                        ┌─────────┐
  TRACK B — freshness audit (feature #1)                │ ANALYST │ (Opus subagent)
  artifact files ──► claim extractor ──► explorer ──────►│ cluster │
                     (tool/flag/API/URL)   fan-out        │ + ≥3    │
                     WebSearch/WebFetch/context7          │ + diag  │
                     → still-current|changed|dead         └────┬────┘
                                                               │ proposals.json + reason logs
                                                               ▼
                                                        ┌──────────┐
                                                        │ APPLIER  │ resolve owning repo
                                                        │ edits +  │ → PR there (Phase 1)
                                                        │ PR/commit│ → auto-commit (Phase 2)
                                                        └────┬─────┘
                                                             ▼ (deferred)
                                                        ┌──────────┐
                                                        │ DEJA-VU  │ re-mine post-merge,
                                                        │ trend    │ append outcome to
                                                        │ validator│ reason log
                                                        └──────────┘
```

**Design bet:** the extractor is *dumb and cheap* (SQL/regex over the whole
corpus); the analyst is *smart and narrow* (reads only incident slices + claim
verdicts). Cost scales with **number of glitches**, not corpus size. The 2→3
evolution is a gate change in the applier, not a rearchitecture.

### Units (each independently testable)

| Unit | Input → Output | Tech |
|------|----------------|------|
| **Extractor** (Track A) | jsonl corpus → `incidents.db` | duckdb + jq, no LLM |
| **Claim extractor + explorers** (Track B) | artifact files → claim verdicts | parallel explorer agents (WebSearch/WebFetch/context7) |
| **Analyst** | incidents.db + verdicts → proposals + reason logs | sensei subagent (Opus) |
| **Applier** | proposals → cross-repo edits + PR/commit | script + git/gh |
| **deja-vu** | incidents.db history → per-artifact trend | duckdb (deferred) |

---

## 4. Track A — corpus mining

### Incident schema (`incidents.db`)

| Field | Meaning |
|-------|---------|
| `session_id`, `project`, `ts` | provenance |
| `signal_type` | one of the signals below |
| `implicated_artifact` | best guess at which agent/skill/CLAUDE.md was in play |
| `window` | transcript slice (~3–8 turns around the event) |
| `confidence` | extractor heuristic confidence (analyst triages on this) |

### Signal detectors (pure SQL/jq over jsonl)

| Signal | Heuristic |
|--------|-----------|
| **tool_error / retry** | `tool_result.is_error == true`; or same `tool_use.input` repeated within N turns |
| **user_correction** | user message right after an assistant tool_use matching negation patterns (`no`, `don't`, `actually`, `revert`, `that's wrong`) **or** an interruption marker (`Request interrupted`) |
| **repeated_guidance** | *not* single-window — emitted by analyst when the same correction clusters across **≥3 sessions** |
| **inefficiency** | whole-file Reads of large files, redundant search chains, long tool runs (threshold-based) |
| **orchestrator_disagreement** | a `Task` tool_result followed by orchestrator text matching disagreement patterns or re-doing the subagent's work |

### `implicated_artifact` resolution

- **Subagent sessions** carry agent identity in the opening system prompt → whole
  session implicates that agent (also where `orchestrator_disagreement` lands).
- **Main sessions** implicate `CLAUDE.md` (global or project, resolved from session
  `cwd`/project dir) or an invoked skill (from `Skill` tool-uses in the window).
- Ambiguous → attach *candidates*, let the analyst decide.

The extractor over-collects on purpose; SQL is cheap, judgment is the analyst's job.

---

## 5. Track B — freshness audit (online)

For each artifact, extract its **external claims** — tool/flag names, library APIs,
version numbers, URLs, "best practice" assertions. **Fan out one explorer per claim**
(embarrassingly parallel):

- **context7** → library/framework/SDK docs
- **WebSearch** → general practice / recommendations
- **WebFetch** → changelogs / specific URLs

Each explorer returns `still-current | changed | dead`. `changed`/`dead` claims
become `fix-stale` proposals through the same analyst → applier → reason-log path.

---

## 6. Analyst (clustering → proposals)

The sensei subagent (Opus) reads `incidents.db` + claim verdicts, **never raw sessions**:

1. **Cluster** incidents by `implicated_artifact` + signal similarity; merge Track B verdicts.
2. **Apply the ≥3 threshold** — a corpus cluster is actionable only if it spans ≥3 sessions (kills one-off noise). Track B `changed`/`dead` verdicts are actionable on their own.
3. **Diagnose fix type**, emit a proposal per cluster.

### Fix-type taxonomy

| Fix type | When | Move |
|----------|------|------|
| **add** | no guidance exists | add a rule/section |
| **strengthen** | guidance exists but is ignored | move higher, make imperative, add red-flag table, or escalate to a hook |
| **fix-stale** | references a renamed file / removed flag / outdated API | correct the reference (primary Track B output) |
| **remove** | guidance is contradictory or causing the glitch | delete/rewrite |
| **escalate-out-of-instructions** | a prose rule keeps failing | recommend a *non-prose* fix — a hook, permission, or default ("define the error out of existence") |

`escalate-out-of-instructions` lets agent-smith propose **hooks** (deterministic
harness enforcement), not just prose — applying the "define errors out of existence"
principle to the agents-matrix. Approved as in-scope.

### Proposal record (unit of work + reason-log entry)

```json
{
  "id": "glitch-2026-06-01-skeleton-reads",
  "implicated_artifact": "/home/noams/.claude/CLAUDE.md#reading-code",
  "fix_type": "strengthen",
  "evidence": ["session_id:turn", "...", "≥3 sessions"],
  "diagnosis": "skeleton-first rule present but violated in N sessions; large whole-file Reads precede no edit to most of the file",
  "proposed_change": "<concrete diff or prose>",
  "confidence": "high",
  "reason_log": "why this change, what signal drove it, expected effect"
}
```

---

## 7. Applier — cross-repo edits, PRs, reason logs

Resolve the repo owning `implicated_artifact` (`git -C <dir> rev-parse`) and act there:

- **nix-config-owned** → branch + PR (`gh pr create --assignee @me`). Phase 2: direct commit on a sensei branch.
- **factify-inc-owned** → branch + PR using **Linear branch naming** (per CLAUDE.md rule); **never** auto-commit to work repos.
- Phase 1: always PR (gated).

**Reason logs** live in the **agent-smith repo** (not target repos), append-only, so
`deja-vu` retains history after PRs merge:

```
agent-smith/reason-log/2026-06-01-skeleton-reads.md   # diagnosis, evidence, diff, expected effect, PR link
```

`deja-vu` later appends an **outcome**: re-run the extractor on sessions after the
merge date, compare incident rate for that artifact, record ↑/↓/flat.

---

## 8. Repo layout

```
agent-smith/
├── extractor/        # Track A: duckdb + jq; jsonl → incidents.db (no LLM)
│   └── signals/      # one module per signal detector (testable in isolation)
├── freshness/        # Track B: claim extraction + explorer fan-out
├── analyst/          # sensei subagent prompt + cluster/diagnose logic
├── applier/          # proposals → cross-repo edits + PR
├── deja-vu/          # trend validator (deferred phase)
├── reason-log/       # append-only ledger
├── fixtures/         # test corpus incl. skeleton-first whole-file-read case
├── docs/specs/       # this design
└── flake.nix         # packages the engine; devshell
```

### nix-config footprint (the only changes there)

- Package the engine (flake input / overlay).
- `agent-smith` subagent `.md` under `home/ai/claude-code/agents/`.
- `/agent-smith` slash command (manual Phase 1 trigger).
- Phase 2: systemd user timer for scheduled runs.

---

## 9. Canonical test fixture

**Skeleton-first violation.** The global `CLAUDE.md` already contains a
"Reading Code (skeleton-first)" rule, yet agents still read whole large files.
This is the reference case because it exercises the hardest behavior:

- Track A extractor **must** flag large whole-file Reads as `inefficiency` incidents.
- Analyst **must** trace them to the *existing* rule and choose `strengthen`
  (or `escalate-out-of-instructions` → a hook), **not** propose a duplicate `add`.

A correct end-to-end run on this fixture is the Phase 1 acceptance bar.

---

## 10. Open questions / future

- Tuning extractor thresholds (retry window, "large file" cutoff, inefficiency heuristics) against real false-positive rates.
- Phase 3 inline capture hook design (cheapen ongoing Track A mining).
- How `deja-vu` attributes a trend change to a specific merge vs. confounders.
