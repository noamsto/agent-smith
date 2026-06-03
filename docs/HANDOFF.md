# Handoff — agent-smith

> For a fresh Claude Code session started inside this repo. Read this first, then
> the relevant spec/plan/doc for whatever you're picking up.

## Where things stand (updated 2026-06-03)

- **Repo:** `github.com/noamsto/agent-smith` (public). Local: `~/Data/git/noamsto/agent-smith`. Default branch `main`.
- **Design:** approved. Top-level design: [`docs/specs/2026-06-01-agent-smith-design.md`](specs/2026-06-01-agent-smith-design.md).
- **Built & merged to `main`:**
  - **Extractor (Track A)** — `cmd/extractor` + `internal/extractor`. Usage: [`docs/extractor.md`](extractor.md).
  - **Analyst** — `cmd/analyst` + `internal/analyst` (the `cluster` + `assemble` binaries and the **Oracle** prompt). Spec: [`docs/superpowers/specs/2026-06-01-analyst-design.md`]. Plan: [`docs/superpowers/plans/2026-06-01-analyst.md`]. Usage: [`docs/analyst.md`](analyst.md).
  - **Applier** — `cmd/applier` + `internal/applier` (the `prepare`/`open`/`submit` binary + the **Editor** prompt + verify gate). Usage: [`docs/applier.md`](applier.md). Runbook: `fixtures/applier/RUNBOOK.md`.
- **Acceptance bar (skeleton-first) — MET end-to-end:** extractor flags whole-file large Reads as `inefficiency`; `analyst cluster` traces them (via candidate explosion) to the global `CLAUDE.md`; the Oracle chose `strengthen` (not duplicate `add`) in a real golden-eval run → `assemble` wrote the proposal + reason-log.
- **Next:** **Track B** (freshness audit) plus the `/agent-smith` orchestration command (the applier is now built — it closes the extractor→analyst→applier loop).

## What agent-smith is (one paragraph)

A meta-agent that improves the instruction artifacts steering Claude Code agents
(subagent `.md`, skills, `CLAUDE.md`, slash commands). **Two tracks feed one
analyst feed one cross-repo applier.** Track A mines `~/.claude/projects/**/*.jsonl`
session history with duckdb for behavioral *glitches*. Track B (not built yet)
audits the artifacts' *external claims* for freshness. The analyst clusters
incidents, applies a ≥3-session threshold, diagnoses a `fix_type`, and emits
proposals + reason logs. The applier opens a PR against whichever
repo owns the artifact. `deja-vu` (Phase 2) re-mines to confirm the glitch dropped.

## Phase 1 status

| Unit | Status | Where |
|------|--------|-------|
| Extractor (Track A) | ✅ on `main` | `cmd/extractor`, `internal/extractor`, `docs/extractor.md` |
| Analyst | ✅ on `main` | `cmd/analyst`, `internal/analyst`, `docs/analyst.md` |
| Track B — Freshness audit | ⬜ not started | spec §5 |
| Applier (proposals → PR) | ✅ on `main` | `cmd/applier`, `internal/applier`, `docs/applier.md` |
| `/agent-smith` command (orchestration) | ⬜ deferred | build with the applier |

## How to build / test / run

```bash
nix develop                       # devshell: go, duckdb, jq, gopls
go test ./...                     # all tests (extractor + analyst)
go build ./...                    # both binaries
nix build .#default               # packaged binaries (result/bin/{extractor,analyst,applier}); extractor/analyst duckdb-wrapped, applier git+gh-wrapped

# Track A end-to-end:
go run ./cmd/extractor --out incidents.db                  # mine the corpus
go run ./cmd/analyst cluster --db incidents.db --out clusters.json
#   → dispatch the Oracle (internal/analyst/oracle.md) per cluster → proposal JSONs
go run ./cmd/analyst assemble --proposals-dir proposals --out proposals.json --reason-log-dir reason-log

# Applier (Phase 1, consumes proposals.json):
go run ./cmd/applier prepare --proposals proposals.json --out apply-plan.json
#   → per ready entry: open → dispatch Editor subagent → verify gate → submit (PR)
go run ./cmd/applier submit --plan apply-plan.json --proposals proposals.json --id <id> --worktree <wt> --editor-result editor-result.json
```

Analyst golden-eval runbook (the on-demand Oracle acceptance check): `fixtures/analyst/RUNBOOK.md`.
Applier runbook (editor + verify dispatch): `fixtures/applier/RUNBOOK.md`.

## Key decisions locked (this matters for the next unit)

- **Tech:** Go thin-orchestrator + **detector/SQL logic run via the `duckdb` CLI** (no CGO duckdb driver). stdlib-only Go. Nix flake (`buildGoModule`, `vendorHash=null`, binaries wrapped with duckdb on PATH).
- **Corpus loader:** `read_ndjson_objects(...)` (raw JSON per line, no schema inference) — NOT `read_csv`/`read_json`. Streams the corpus without OOM.
- **LLM pieces = Claude Code subagents, not the Anthropic API.** The Oracle is a **pure `prompt → JSON` completion** (inputs inlined, no tool use) so it's harness/provider-neutral; only the *dispatch* is CC-specific. Decoupling-from-Claude = a corpus adapter (Track A) + a domain mapping (artifacts), NOT the Oracle — see `docs/extractor.md` §Deferred signals and the analyst spec §9.
- **Output:** Phase 1 = PR-gated. `proposals.json` is machine-local (gitignored); **`reason-log/` is committed to THIS repo** (append-only; applier appends PR link, deja-vu appends outcome).
- **agent-smith may propose hooks** (`escalate-out-of-instructions`), not just prose.
- **`orchestrator_disagreement` was removed from the Phase-1 extractor** (deferred): it's a semantic judgment with no cheap structural anchor on this async-fan-out corpus. Phase-2 path = attribute a subagent's *own* sidechain glitches to its `.md` + analyst-judged async correlation. See `docs/extractor.md` §Deferred signals.

## Environment / corpus notes (verified, not assumptions)

- Corpus: `~/.claude/projects/**/*.jsonl`, ~203k records, **live (grows every session)** — counts drift run-to-run.
- **Subagents spawn via the `Agent` tool** in this environment (input `.subagent_type`), NOT `Task` (the original spec §4 said `Task`; the corpus has 0 `Task` uses, 637 `Agent`). Code matches `Agent`/`Task`.
- Extractor signals (4, all structural): `inefficiency`, `tool_error`, `retry`, `user_correction`. `repeated_guidance` is analyst-side.
- `incidents` schema: `incident_id` (md5 `session:turn:signal`, PK, idempotent), `session_id`, `project`, `ts`, `signal_type`, `implicated_artifact`, `candidates` (JSON array), `"window"` (JSON; quoted — reserved word), `confidence`, `detail` (JSON).
- nix-config (the main consumer) is at `~/nix-config`; agents in `home/ai/claude-code/agents/`, skills in `home/ai/claude-code/skills/`.

## First move for a new session

Pick the next unit (**Applier** is the natural one — it consumes the analyst's
`proposals.json` and completes the extractor→analyst→applier loop). Brainstorm it
(`superpowers:brainstorming`) → spec → `superpowers:writing-plans` → build via
`superpowers:subagent-driven-development`. Do **not** code before the plan exists.
