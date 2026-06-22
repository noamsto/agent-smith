# Track B v1 — Instruction Freshness Audit (file/path references)

**Issue:** #45
**Status:** design
**Date:** 2026-06-22

## 1. Problem

Agent-smith ships only **Track A** — backward-looking glitch mining (`extractor` →
`incidents.db` → `analyst cluster` → Oracle). **Track B**, the forward-facing
freshness audit specified in `docs/specs/2026-06-01-agent-smith-design.md §5`, was
never built. The `fix-stale` fix-type is plumbed end-to-end through Oracle, Skeptic,
Editor, and `assemble` validation — but **nothing generates it**. A stale reference
in an instruction file (a rule naming a file that was renamed, moved, or deleted) is
caught only *reactively*: it must first cause a glitch, that glitch must cluster
across ≥5 sessions, and the Oracle must then infer staleness through the
ubiquity noise that dominates Track A clusters. That path is leaky by construction,
so stale references sit undetected until they break something.

Track B v1 closes this: it **proactively** detects instruction rules that reference
repo files/artifacts which no longer exist, and routes each into the existing
`fix-stale` pipeline.

### Why this is higher-precision than Track A

| | Track A | Track B |
|---|---|---|
| Input | past session failures | the instruction files themselves |
| Logic | *correlational* — a glitch occurred while the file was loaded | *deterministic* — does the named file exist? |
| Precision | noise-dominated (attribution by ubiquity) | a missing file is objectively wrong |
| Timing | after a failure, buried in noise | before any failure |

A Track B `dead` verdict is **actionable on its own** — it does not need the
`min-sessions` threshold (default 5) Track A applies to kill one-off noise.

## 2. Scope

**In scope (v1):** file-path and artifact references — does the file/dir/skill the
rule names still exist, relative to its repo.

**Out of scope (later slices, YAGNI):** CLI flags/subcommands, library APIs, version
numbers, URLs, web explorers (context7/WebSearch/WebFetch), and instruction files
that have never appeared in a session.

**Decisions locked during brainstorming:**

- **Verification surface:** file/path references only — bulletproof and deterministic.
- **Extraction:** hybrid — deterministic candidate generation, with an LLM adjudicator
  invoked *only* on ambiguous-and-missing candidates.
- **Integration:** feed the existing Oracle. Track B emits `stale-ref` cluster entries;
  the Oracle drafts the concrete `fix-stale`/`remove` change. Propose/apply untouched.
- **Audit set:** the distinct instruction artifacts already present in `incidents.db`.

## 3. Architecture

Track B is purely a new **producer** of `clusters.json` entries. Everything downstream
(Oracle → Skeptic → `assemble` → applier) is reused unchanged except a small
additive branch in the Oracle prompt.

```
incidents.db (distinct artifact list)
   → analyst freshness            (Go, deterministic, no LLM)
        → { dead claims, ambiguous-missing candidates }
   → /agent-smith:freshness skill
        → LLM-adjudicate ambiguous-missing only  (default-drop on uncertainty)
        → write stale-ref cluster files, merge into clusters.json
   → existing propose phase:
        Oracle (stale-ref branch) → Skeptic → assemble → applier
```

### 3.1 `internal/freshness` package + `analyst freshness` subcommand

Lives under `analyst` (reuses its duckdb access and cluster-writer; no fourth binary).
Pure detection, no LLM:

1. Query `incidents.db` for the distinct `artifact` list (the canonicalized files
   Track A saw loaded).
2. For each artifact still present on disk: read it, run **deterministic candidate
   extraction** (§4.1).
3. Classify each candidate → *confident claim* / *ambiguous* / *skip* (§4.2) and resolve
   each non-skip candidate against the filesystem.
4. Emit JSON: `dead` (confident claim, resolves to a missing path) and
   `ambiguous_missing` (ambiguous form, also missing — needs adjudication). Live and
   skipped candidates are dropped.

### 3.2 `/agent-smith:freshness` skill (orchestrator layer)

1. Bootstrap (as the other skills do), run `analyst freshness`.
2. For each `ambiguous_missing` candidate, dispatch an LLM adjudicator subagent:
   *"Is this token a genuine claim that a repo file exists, or an example/runtime/prose
   path?"* — **default-drop on uncertainty** (mirrors the Skeptic's default-drop).
3. Group all confirmed-stale references **by artifact** and write one `stale-ref`
   cluster file per artifact, appended to `clusters.json`.
4. Report the dead references found (and the ambiguous ones dropped), then hand off to
   `/agent-smith:propose`.

## 4. Detection detail

### 4.1 Candidate extraction (deterministic)

Scan the artifact for path-shaped tokens in these forms:

- **`@imports`** — `@path` / `@~/path` (CLAUDE.md import syntax).
- **Markdown local links** — `[text](./path)` / `[text](path/to/file)` (non-URL targets).
- **Backtick paths** — inline-code tokens that are repo-relative *with* a separator.

### 4.2 Classification

- **Skip** (never flagged): `/tmp...`, tokens containing `$` (vars), glob metacharacters
  (`* ? [ ]`), URLs (`scheme://`), absolute paths outside any known repo root, and bare
  filenames with no path separator.
- **Confident claim**: `@imports`, markdown local links, and backtick repo-relative paths
  with a separator. Resolved directly — **exists → drop, missing → `dead`.**
- **Ambiguous**: a path-shaped token that is neither clearly skip nor clearly confident.
  Only sent to the LLM adjudicator **if it is also missing on disk**; an ambiguous token
  that resolves to a live file is dropped regardless.

### 4.3 Resolution rules

- Resolve relative to the **artifact's own repo root**, reusing `canonicalizeRepoPrefix`
  (worktree-aware: in-repo `.worktrees/` and sibling `<repo>-worktrees/`).
- `@imports` resolve relative to the **importing file's directory**, with `~` expansion
  to `$HOME`.
- **A path that exists is never flagged** — zero false positives on live files is the
  hard contract.

## 5. The `stale-ref` cluster record

Track B writes cluster files in the schema the Oracle already consumes, with:

- `signal_type: "stale-ref"`
- `artifact`, `artifact_content` (so the Oracle sees the rules in context).
- In place of session `incidents`, an **`evidence`** array: one entry per dead reference
  — `{ path, line, rule_excerpt, resolved_to }`.
- These entries are written **after** `clusterSQL`'s `HAVING min-sessions` gate, so they
  are never subject to it — a `stale-ref` cluster is actionable on its own.

## 6. Integration touch-points

- **`agents/oracle.md`** — add a `stale-ref` diagnosis branch: input is the artifact
  content + the `evidence` list; output a `fix-stale` (repoint the reference) or `remove`
  (drop the rule) proposal, or `skip` if the Oracle judges the reference benign. The
  `fix-stale` type, Skeptic verification, Editor application, and `assemble` validation
  already exist — **no change required there.**
- **No change** to `extractor`, `cluster`, `propose.md`, `apply.md`, or the applier.

## 7. Error handling

- An artifact in `incidents.db` that no longer exists on disk → skipped (Track A already
  drops these; freshness does the same).
- An unreadable artifact → logged to stderr, skipped, run continues.
- The LLM adjudicator erroring or returning no verdict → treat as drop (default-drop on
  unverified, consistent with the Skeptic).
- A `stale-ref` cluster whose artifact the Oracle judges benign → `skip` proposal, logged
  to the reason-log like any other skip (so it is not re-surfaced every run).

## 8. Testing

- **Unit (`internal/freshness`):** the claim-form classifier (confident / ambiguous /
  skip), path resolution + worktree canonicalization, and the skiplist (`/tmp`, `$vars`,
  globs, URLs, bare filenames).
- **Golden fixture:** a `CLAUDE.md` mixing live refs, dead refs, example paths, `/tmp`
  runtime paths, `$VAR` paths, and URLs → assert **exactly** the genuine dead references
  are emitted and **zero false positives**.
- **Schema test:** an emitted `stale-ref` cluster parses as an Oracle-consumable cluster.
- The LLM adjudication is orchestrator-layer; it is tested at the deterministic boundary
  (what `analyst freshness` marks `ambiguous_missing`).

## 9. Out of scope / future

- Flag/subcommand freshness (needs per-tool `--help` parsing).
- API/library/version freshness via web explorers (context7/WebSearch/WebFetch) — the
  full Track B design.
- Auditing instruction files never seen in a session (glob-discovery).
- Folding freshness into `mine`/`run` as an automatic step (v1 is a standalone skill).
