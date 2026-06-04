# Handoff — agent-smith

> For a fresh Claude Code session started inside this repo. Read this first, then
> the relevant spec/plan/doc for whatever you're picking up.

## Where things stand (updated 2026-06-03)

- **Repo:** `github.com/noamsto/agent-smith` (public). Local: `~/Data/git/noamsto/agent-smith`. Default branch `main`.
- **Design:** approved. Top-level design: [`docs/specs/2026-06-01-agent-smith-design.md`](specs/2026-06-01-agent-smith-design.md).
- **Built & merged to `main`:**
  - **Extractor (Track A)** — `cmd/extractor` + `internal/extractor`. Usage: [`docs/extractor.md`](extractor.md).
  - **Analyst** — `cmd/analyst` + `internal/analyst` (the `cluster` + `assemble` binaries and the **Oracle** prompt). Spec: [`docs/superpowers/specs/2026-06-01-analyst-design.md`]. Plan: [`docs/superpowers/plans/2026-06-01-analyst.md`]. Usage: [`docs/analyst.md`](analyst.md). Plus: **Oracle big-cluster ingestion** — `analyst cluster` now session-stratified-samples incidents (`--max-incidents-per-cluster`, default 24; `0` = uncapped) and reports `total_incidents`, so high-signal clusters fit the Oracle. Spec: [`docs/superpowers/specs/2026-06-04-oracle-cluster-sampling-design.md`].
  - **Applier** — `cmd/applier` + `internal/applier` (the `prepare`/`open`/`submit` binary + the **Editor** prompt + verify gate). Usage: [`docs/applier.md`](applier.md). Runbook: `fixtures/applier/RUNBOOK.md`. Plus: **`suggest`** subcommand (side-effect-free dry-run index across all proposals — no edits/PRs); **symlink + worktree resolution** in `resolve.go` (`Resolve` EvalSymlinks the artifact and maps linked worktrees to their main repo). Resolution spec: [`docs/superpowers/specs/2026-06-03-applier-resolution-symlink-worktree.md`].
- **Acceptance bar (skeleton-first) — MET end-to-end:** extractor flags whole-file large Reads as `inefficiency`; `analyst cluster` traces them (via candidate explosion) to the global `CLAUDE.md`; the Oracle chose `strengthen` (not duplicate `add`) in a real golden-eval run → `assemble` wrote the proposal + reason-log.
- **Next (highest-value):** with Oracle big-cluster ingestion shipped, the chain can now run the Oracle on the high-signal artifacts end-to-end — the remaining gate to a **real end-to-end PR** is a live golden run of `analyst cluster` (capped) → Oracle dispatch → `assemble` → `applier prepare` on the top clusters. After that: **Track B** (freshness audit, spec §5) + the `/agent-smith` orchestration command.

## Live-run findings (2026-06-03, real corpus)

A full run (`extractor` → `analyst cluster` → `applier prepare`/`suggest`) on the live corpus: **5,386 incidents → 36 clusters → 16 ready / 20 skipped** (before the resolution fix; the global `~/.claude/CLAUDE.md` now resolves to `nix-config`). Open items, in priority order:

1. **✅ Oracle big-cluster ingestion — RESOLVED (2026-06-04).** Root cause was incident *count* (≈3 KB/window × thousands), not window size. `analyst cluster` now does **session-stratified sampling**: `--max-incidents-per-cluster` (default **50**, `0` = uncapped) caps each cluster to a round-robin-across-sessions sample (high-confidence first), while `total_incidents`/`distinct_sessions` still report true counts and `artifact_content` is never truncated. The Oracle prompt documents that `incidents[]` is a sample. Per-window trimming was deferred (YAGNI). Spec/plan: `docs/superpowers/{specs,plans}/2026-06-04-oracle-cluster-sampling*`. **Verified end-to-end (2026-06-04):** a live golden run (5,511 incidents → 34 clusters; biggest `retry` cluster 2344 incidents/8 MB → 24-sample/104 KB) fed the Oracle the `inefficiency`/global-`CLAUDE.md` cluster → `escalate-out-of-instructions` (PreToolUse Read-guard hook), acceptance PASS.
1b. **✅ `retry` detector noise — FIXED (2026-06-04).** The golden run revealed ~half of `retry` was duplicate *successful* calls (verify loops, dup reads): the detector flagged any identical tool+input within 5 turns with no failure check. Now requires an earlier identical call in the window to have errored (`tool_results.is_error`). Spec/plan: `docs/superpowers/{specs,plans}/2026-06-04-retry-detector-noise*`.
2. **🟡 Dead/removed worktree paths** stay `skip-missing-file` (correct, deferred). Upstream path canonicalization (cluster de-fragmentation so worktree-session glitches accumulate on the canonical repo file) is also deferred.
3. **🟡 Idempotent `open` retry** after a mid-`submit` failure needs manual `git branch -D` today (applier spec §8, deferred).

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
| Applier `suggest` (dry-run index) | ✅ on `main` | `internal/applier/suggest.go`, `docs/applier.md` §Dry run |
| Symlink + worktree resolution | ✅ on `main` | `internal/applier/resolve.go`, spec `2026-06-03-applier-resolution-*` |
| Oracle big-cluster ingestion (sampling) | ✅ on `main` | `internal/analyst/cluster.go` (`--max-incidents-per-cluster`), spec `2026-06-04-oracle-cluster-sampling-design.md` |
| `/agent-smith` command (orchestration) | ⬜ deferred | analyst+applier built; wire the full loop |

## How to build / test / run

```bash
nix develop                       # devshell: go, duckdb, jq, gopls, git, gh
go test ./...                     # all tests (extractor + analyst + applier)
go build ./...                    # all three binaries
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

The extractor→analyst→applier loop is built and the Oracle now ingests big clusters
(session-stratified sampling) on a verified golden run; the `retry` signal is de-noised.
The remaining piece of that golden run is to **land a real PR**: take the Oracle's
`inefficiency`/global-`CLAUDE.md` proposal (a PreToolUse Read-guard hook, `escalate`)
through `assemble` → `applier prepare`/`submit` against `nix-config` — the first
end-to-end PR. (NB: it's an `escalate-out-of-instructions` hook proposal, so the Editor
subagent writes a hook in the Nix `--settings` overlay, not prose.) Alternatives:
**Track B** (freshness audit, spec §5) or the **`/agent-smith`** orchestration command.
Whichever you pick: brainstorm
(`superpowers:brainstorming`) → spec → `superpowers:writing-plans` → build via
`superpowers:subagent-driven-development`, in an isolated `wt` worktree. Do **not** code
before the plan exists. NB: review-agent Bash sometimes leaves a stray `strings.Cut`
modernization in the working tree — `git restore` it; merge only reviewed commits.
