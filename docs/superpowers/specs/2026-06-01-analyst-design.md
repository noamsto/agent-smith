# agent-smith Analyst — Design

> The Oracle reads the glitches the extractor found, sees which instruction
> already exists and is being ignored, and prescribes the fix — strengthen the
> rule, don't write a louder duplicate.

**Status:** Design approved (brainstorm), pending implementation plan.
**Date:** 2026-06-01
**Parent design:** [`docs/specs/2026-06-01-agent-smith-design.md`](../../specs/2026-06-01-agent-smith-design.md) §6 (Analyst).
**Depends on:** the Track A extractor (`incidents.db`), merged in PR #1.

---

## 1. Purpose & scope

The analyst turns the extractor's raw behavioral `incidents` into **actionable
proposals** to improve instruction artifacts. It is the Phase-1 unit that closes
the **skeleton-first acceptance bar**: trace recurring whole-file-read
inefficiency to the *existing* global `CLAUDE.md` reading-code rule and choose
`strengthen` (or `escalate-out-of-instructions`), **never** a duplicate `add`.

**This plan builds the analyst core only:**
1. `analyst cluster` — deterministic clustering binary (`incidents.db` → `clusters.json`).
2. **the Oracle** — a Claude Code subagent that diagnoses one cluster → a proposal.
3. `analyst assemble` — deterministic binary (`proposals.json` + `reason-log/*.md`).
4. Evals — CI unit tests for the binaries + an on-demand golden eval for the Oracle.

**Out of scope (deferred):**
- The `/agent-smith` slash command that wires extractor → analyst → applier end-to-end.
- Track B claim-verdict ingestion (the analyst merges those later; see §8).
- The applier (consumes `proposals.json`; separate plan).
- nix-config installation of the Oracle as a registered subagent + the slash command.

---

## 2. Design decisions (from the brainstorm)

| Decision | Choice |
|----------|--------|
| Deterministic/LLM split | **SQL clusters + ≥3 gate (deterministic); the LLM only diagnoses actionable clusters.** Cost scales with #clusters, not #incidents — mirrors the extractor. |
| Plan scope | **Analyst core only** (binaries + Oracle + eval); orchestration command deferred. |
| Proposal output | **Concrete diff.** The clustering step pre-includes the implicated artifact's current content; the Oracle reads the existing rule, decides strengthen-vs-add from what's actually there, and emits a concrete edit. |
| Naming | The diagnosing subagent is **the Oracle** (Matrix-themed; it sees and prescribes). |
| Eval | **Split:** unit-test the binaries in CI; verify the Oracle with an **on-demand golden eval** (real subagent), not in CI. |

---

## 3. Architecture

```
incidents.db ──► [analyst cluster] ──► clusters.json ──► (CC dispatches one
                  Go + duckdb SQL        actionable          Oracle subagent
                  no LLM                 clusters +          per cluster)
                                         artifact content)         │
                                                                   ▼
proposals.json   ◄── [analyst assemble] ◄── per-cluster proposal JSONs
+ reason-log/*.md     Go (stdlib), no LLM    (the Oracle's output)
```

**Design bet (inherited):** the binaries are *dumb and cheap*; the Oracle is
*smart and narrow* — it reads only incident windows + the artifact text, never
raw sessions. The glue (cluster → dispatch Oracle per cluster → assemble) is a
Claude Code orchestration; in Phase 1 that glue is the **eval runbook**, not a
shipped command.

### Units (each independently testable)

| Unit | Input → Output | Tech | Verified by |
|------|----------------|------|-------------|
| `analyst cluster` | `incidents.db` → `clusters.json` (≥3-gated clusters + artifact content) | Go + duckdb CLI | CI unit tests |
| **the Oracle** | one cluster → one proposal JSON | CC subagent (`oracle.md`) | on-demand golden eval |
| `analyst assemble` | proposal JSONs → `proposals.json` + `reason-log/*.md` | Go (stdlib) | CI unit tests |

---

## 4. Clustering (`analyst cluster`)

Deterministic. Steps:

1. Read `incidents.db`.
2. **Explode each incident across its `candidates`** — an incident with
   `candidates: [global CLAUDE.md, project CLAUDE.md]` yields two rows,
   `(incident, global)` and `(incident, project)`. Fall back to
   `implicated_artifact` when `candidates` is empty.
3. Group by `(candidate_artifact, signal_type)`.
4. **Gate:** keep groups with `COUNT(DISTINCT session_id) ≥ 3`.
5. For each surviving cluster, read the artifact file's **current content** from
   disk (note if missing) and bundle it with the member incidents.
6. Emit `clusters.json`.

**Why explode-by-candidate (the acceptance-bar-critical choice):** the
skeleton-first inefficiency incidents carry `candidates: [global, project]` with
*project* as the primary guess, but the reading-code rule lives in the **global**
`CLAUDE.md`, and each individual project sees only a few sessions. Exploding and
grouping by candidate means the shared global artifact accumulates incidents
across *all* projects → `≥3 distinct sessions` fires on the `global / inefficiency`
cluster, and the Oracle receives that cluster *with the global file's content*
→ `strengthen`. The rejected alternative (cluster by the single primary artifact)
would scatter inefficiency under each project's `CLAUDE.md`, never reach the
threshold, and — if it did — point the Oracle at a file lacking the rule, inviting
a wrong `add`.

**Accepted tradeoff:** an incident can appear in several clusters (one per
candidate). Deliberate over-collection — the `≥3` gate plus the Oracle's judgment
filter it. If both a project-level and a global-level cluster clear the gate, both
surface; minor redundancy, fine for Phase 1.

### `clusters.json` schema

```json
[
  {
    "cluster_id": "inefficiency::/home/noams/.claude/CLAUDE.md",
    "signal_type": "inefficiency",
    "artifact": "/home/noams/.claude/CLAUDE.md",
    "artifact_content": "<current file text, or null if missing>",
    "artifact_exists": true,
    "distinct_sessions": 3,
    "incidents": [
      {
        "incident_id": "…",
        "session_id": "sess-a",
        "ts": "2026-05-10T08:00:00Z",
        "confidence": "high",
        "detail": { "tool": "Read", "file_path": "…", "total_lines": 1200 },
        "window": [ { "turn": 80, "type": "assistant", "excerpt": "…" } ]
      }
    ]
  }
]
```

`cluster_id` = `signal_type::artifact` (deterministic, stable across runs).

---

## 5. The Oracle (`oracle.md`)

A Claude Code subagent, dispatched **once per cluster** with the cluster inlined
into its prompt. Returns **only** a proposal JSON as its final message. Pure: no
file-writing, no session access.

**Decision procedure (the prompt instructs):**
1. Read the windows → state the recurring behavior in one line.
2. Scan `artifact_content` → **does a relevant rule already exist?** (the whole game)
3. Choose `fix_type`:
   - **`add`** — no relevant guidance exists → write the missing rule.
   - **`strengthen`** — guidance exists but is ignored → raise it, make it imperative, add a red-flag table, tighten scope.
   - **`fix-stale`** — the rule names something renamed/removed/outdated → correct the reference.
   - **`remove`** — guidance is contradictory or *causing* the glitch → cut/rewrite.
   - **`escalate-out-of-instructions`** — a prose rule keeps failing across the sessions → recommend a **non-prose** fix (a hook/permission/default).
4. Draft a **concrete `proposed_change`**: for `strengthen`, the actual edited text or a unified diff against the artifact; for `escalate`, a sketch of the hook + where it lives.
5. Write `diagnosis` and `reason_log` (why this change, which signal drove it, expected effect); set `confidence`.

**Acceptance-bar guard (explicit in the prompt):** *"If the artifact already
contains guidance on this behavior, you MUST NOT propose `add` — choose
`strengthen` or `escalate-out-of-instructions`."*

**Constraints:** propose changes to *this artifact only*; reason only from the
supplied windows + artifact content (never Read raw sessions); prefer the minimal
effective edit over a rewrite; emit valid JSON only.

### Proposal record (schema = parent spec §6)

```json
{
  "id": "glitch-2026-06-01-skeleton-reads",
  "implicated_artifact": "/home/noams/.claude/CLAUDE.md#reading-code",
  "fix_type": "strengthen",
  "evidence": ["sess-a:turn-12", "sess-b:turn-4", "sess-c:turn-9", "≥3 sessions"],
  "diagnosis": "skeleton-first rule present but violated in N sessions; whole-file Reads of large files precede no edit to most of the file",
  "proposed_change": "<concrete edited text or unified diff>",
  "confidence": "high",
  "reason_log": "why this change, what signal drove it, expected effect"
}
```

`id` is a kebab slug the Oracle proposes; `assemble` dedups/namespaces it.

---

## 6. Output (`analyst assemble`)

Deterministic. Reads the per-cluster proposal JSONs and writes:

- **`proposals.json`** — array of validated proposal records; machine-local
  (gitignored); the applier's unit-of-work feed.
- **`reason-log/<YYYY-MM-DD>-<slug>.md`** — one append-only file per proposal,
  **committed to this repo** (per parent spec §7, so `deja-vu` retains history
  after PRs merge). Contains diagnosis, evidence, the proposed diff, expected
  effect, confidence. The **PR link** is appended later by the applier; the
  **outcome** (↑/↓/flat) later by `deja-vu`.

Rigor lives here: validate every Oracle JSON against the schema (reject + report
malformed output), dedup/namespace `id`s, stamp the date, write idempotently
(don't double-write an existing reason-log). Unit-testable with canned proposal
JSONs — real CI coverage of the deterministic half.

---

## 7. Evaluation & acceptance

**Deterministic (CI `go test`):**
- `analyst cluster`: fixture `incidents.db` → the expected actionable cluster
  (right artifact, `signal_type`, `distinct_sessions`, artifact content bundled);
  a 2-session group must **not** survive the gate; candidate explosion verified
  (an incident with `[global, project]` contributes to both; only ≥3 survives).
- `analyst assemble`: canned proposal JSONs → correct `proposals.json` +
  `reason-log/*.md`; malformed JSON rejected; dedup works.

**Golden eval (on-demand, real Oracle — the analyst's half of the acceptance bar):**
- Fixture: a small `CLAUDE.md` containing a "Reading Code (skeleton-first)" rule +
  3-session jsonl of whole-file large reads whose `candidates` point at that
  `CLAUDE.md`.
- Procedure (a documented runbook executed in a Claude Code session — a CC
  subagent can't be driven from a Go test without the Anthropic API, which we are
  deliberately not using): build `incidents.db` from the jsonl **via the existing
  extractor**, run `analyst cluster`, dispatch the Oracle on the single cluster
  (inlining `oracle.md`), capture the proposal JSON.
- Assert: `fix_type ∈ {strengthen, escalate-out-of-instructions}` (**never `add`**);
  `implicated_artifact` resolves to that `CLAUDE.md`'s reading-code section;
  `proposed_change` is a concrete edit that references the existing rule.

---

## 8. Repo layout

```
cmd/analyst/main.go              # subcommands: cluster, assemble
internal/analyst/                # cluster SQL (duckdb CLI) + assemble (stdlib) + Go schemas
internal/analyst/oracle.md       # the Oracle prompt (embedded; nix-config install deferred)
fixtures/analyst/                # CLAUDE.md fixture + 3-session jsonl + eval runbook
reason-log/                      # append-only ledger (committed)
```

Mirrors the extractor: detector/clustering logic in SQL run via the `duckdb` CLI,
Go as the thin orchestrator; stdlib-only for `assemble`.

---

## 9. Open questions / future

- **Track B merge:** when the freshness audit lands, its `changed`/`dead` claim
  verdicts feed the analyst as actionable items *without* the ≥3 gate (they're
  actionable on their own). `cluster` will gain a second input source.
- **`/agent-smith` command:** the production orchestration (extractor → cluster →
  Oracle → assemble → applier) is built with the applier.
- **Oracle as a registered subagent:** Phase 1 inlines `oracle.md` into a
  dispatch; nix-config installs it as a first-class agent type later.
- **Reason-log lifecycle:** the applier appends the PR link and `deja-vu` the
  outcome; `assemble` only creates the entry.
- **Cross-cluster themes:** pure-SQL clustering can't spot a glitch that spans
  *different* artifacts/signals; if that proves valuable, a later optional LLM
  pass could merge related clusters (the brainstorm's "hybrid" option).
