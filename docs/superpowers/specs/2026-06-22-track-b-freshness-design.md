# Track B v1 — Instruction Freshness Audit (file/path references)

**Issue:** #45
**Status:** v1
**Date:** 2026-06-22

## 1. Problem

Agent-smith's **Track A** is backward-looking glitch mining (`extractor` →
`incidents.db` → `analyst cluster` → Oracle). **Track B**, the forward-facing
freshness audit from `docs/specs/2026-06-01-agent-smith-design.md §5`, covers what
Track A cannot see. The `fix-stale` fix-type is plumbed end-to-end through Oracle,
Skeptic, Editor, and `assemble` validation, but Track A can only reach it
*reactively*: a stale reference in an instruction file (a rule naming a file that was
renamed, moved, or deleted) must first cause a glitch, that glitch must cluster across
≥5 sessions, and the Oracle must then infer staleness through the ubiquity noise that
dominates Track A clusters. That path is leaky by construction, so stale references
sit undetected until they break something.

Track B v1 **proactively** detects instruction rules that reference files which no
longer exist, and routes each into the existing `fix-stale` pipeline.

### Why this is higher-precision than Track A

| | Track A | Track B |
|---|---|---|
| Input | past session failures | the instruction files themselves |
| Logic | *correlational* — a glitch occurred while the file was loaded | *deterministic* — does the named file exist? |
| Precision | noise-dominated (attribution by ubiquity) | a missing file is objectively wrong |
| Timing | after a failure, buried in noise | before any failure |

A Track B `dead` verdict is **actionable on its own** — it does not need the
`--min-sessions` threshold (default 5) Track A applies to kill one-off noise.

## 2. Scope

**In scope (v1):** file-path references — does the file or directory the rule names
still exist.

**Out of scope (later slices):** CLI flags/subcommands, library APIs, version
numbers, URLs, web explorers (context7/WebSearch/WebFetch), instruction files never
seen in a session or reachable from one by `@import`, and folding freshness into
`mine`/`run`.

**Decisions:**

- **Verification surface:** file/path references only — deterministic.
- **Extraction:** hybrid — deterministic candidate generation, with an LLM adjudicator
  invoked *only* on ambiguous-and-missing candidates.
- **Integration:** feed the existing Oracle. Track B writes `stale-ref` cluster files;
  the Oracle drafts the concrete `fix-stale`/`remove` change. Propose/apply untouched.
- **Audit set:** the distinct instruction artifacts in `incidents.db`, plus the files
  they `@import` (§3.1).

## 3. Architecture

Track B is a new **producer** of cluster files. Everything downstream (Oracle →
Skeptic → `assemble` → applier) is reused; the Oracle gains a self-contained
`stale-ref` section and the Skeptic one line on how to verify it.

```
incidents.db (distinct canonical artifacts)
   → analyst freshness scan     (Go, deterministic, no LLM)  → freshness.json
        { dead, ambiguous_missing }
   → /agent-smith:freshness skill
        → LLM-adjudicate ambiguous_missing only  (default-drop)  → adj-*.json
   → analyst freshness merge    (Go)  → clusters/<id>.json + clusters.json entries
   → existing propose phase: Oracle (stale-ref section) → Skeptic → assemble → applier
```

### 3.1 `internal/freshness` + `analyst freshness scan`

`analyst freshness` is a subcommand of the existing `analyst` binary (no new binary),
with two modes, `scan` and `merge`. `scan` is pure detection:

1. **Audit set.** `SELECT DISTINCT` (sorted) the candidate artifacts from `incidents`,
   canonicalized with the *same* SQL worktree expression `clusterSQL` uses (one shared
   Go constant, so the two cannot drift). `--artifact <path>` (repeatable) adds paths
   directly; `--artifact-prefix <repo root>` keeps only artifacts under that root, like
   `analyst cluster`. An artifact missing on disk is skipped and counted.
2. **Import expansion.** Each audited artifact's `@import` targets that exist on disk
   are audited too, transitively in sorted order (a visited set keyed on the real path breaks cycles
   and dedups symlinked copies). Extractor candidates are only `CLAUDE.md` files, and a
   common layout is a `CLAUDE.md` that is just `See @AGENTS.md` — without this step the
   file carrying the rules would never be audited. An expanded artifact is recorded
   under its import-resolved path (not its symlink-resolved one), matching how
   `analyst cluster` re-attributes pointer artifacts. An import is followed only when
   its symlink-resolved target lies under the importing artifact's repo root or
   `$HOME`, outside `/proc`, `/sys`, and `/dev`, and is a regular file of at most
   1 MiB — its content is embedded in a cluster file, so an import must not pull
   arbitrary files into the pipeline. The import ref itself is still resolved and
   reported either way.
3. For each audited artifact: extract candidates (§4.1), classify (§4.2), resolve
   (§4.3).
4. Emit `freshness.json` (§5.1). Live and skipped candidates are dropped.

On the real corpus most canonical candidates are missing on disk (repos without a
root `CLAUDE.md`, and the centralized worktree layout in §4.3), so the audit set is
small — the few live `CLAUDE.md` files and what they import.

The audit runs on the filesystem, not git: a file that exists in the main checkout is
live whether or not it is tracked.

### 3.2 `/agent-smith:freshness` skill (`commands/freshness.md`)

1. Bootstrap `$BIN` exactly as the other command files do, then
   `analyst freshness scan --db incidents.db --out freshness.json` (`repo` argument →
   `--artifact-prefix "$(git rev-parse --show-toplevel)"`). Precondition:
   `incidents.db` exists — otherwise run `/agent-smith:mine` first.
2. For each artifact with `ambiguous_missing` refs, dispatch one
   `agent-smith:adjudicator` subagent (`agents/adjudicator.md`, tools `Read, Write`)
   with that artifact's refs. The artifact text comes from repos the user cloned, so
   the adjudicator treats it as data and has no tool that could act on an injected
   instruction. It reads the artifact around each line and answers, per ref: *is
   this token a genuine claim that a file exists at this path in this repo, or an
   example / placeholder / file-to-be-created / runtime output / other-repo /
   branch-or-slug token?* It writes `[{"id", "verdict": "stale"|"drop", "reason"}]`
   to `<run dir>/adj-<i>.json`, where the run dir comes from
   `mktemp -d /tmp/agentsmith-fresh.XXXXXX` (outside `apply`'s `/tmp/agent-smith-*`
   pickup glob). **Default-drop:** only an explicit `stale` keeps a ref.
3. `analyst freshness merge --report freshness.json --adjudications-dir "<run dir>"
   --out clusters.json --reason-log-dir reason-log`. A non-empty adjudications dir
   that cannot be listed is reported on stderr and keeps no ambiguous ref.
4. Report dead refs, ambiguous refs kept/dropped, clusters written; hand off to
   `/agent-smith:propose`.

### 3.3 `analyst freshness merge`

1. Kept refs = every `dead` ref ∪ each `ambiguous_missing` ref whose `id` has
   `verdict: "stale"` in some `adj-*.json`. An unparseable adjudication file is
   reported on stderr and contributes nothing (default-drop); unknown ids are ignored.
2. **Per-ref suppression.** A ref is dropped (and logged) when a `closed`/`rejected`
   reason-log entry with `**Signal:** stale-ref` for the same artifact lists it in its
   `## Evidence` section — matched as an evidence bullet that *opens* with
   `` `<path>` ``, the form the Oracle's stale-ref evidence strings are required to
   take (a repoint target later in the same bullet does not match). Suppression is per ref, not per
   artifact: a declined PR about one reference must not blind the audit to new stale
   references in the same file, and the most-referenced artifacts (the global
   `CLAUDE.md`, shared rule files) are exactly the ones that would otherwise go dark.
3. Group the remaining refs **by artifact** → one `stale-ref` cluster per artifact
   (§5.2).
4. Merge into the index: remove every existing `stale-ref` entry from `clusters.json`
   and delete its per-cluster file, write the new files under `clusters/`, append the
   new entries. Track A entries and files are left untouched. A missing
   `clusters.json` is created.

### 3.4 Ordering with `analyst cluster`

`analyst cluster` rewrites `clusters.json` from its own clusters and prunes every
per-cluster file it did not write, so it removes `stale-ref` clusters. The order is
therefore **mine → freshness → propose**, and freshness is re-run after any later
mine. Freshness is cheap to regenerate: the scan is deterministic, and only the
ambiguous refs cost an adjudicator call.

## 4. Detection detail

### 4.1 Candidate extraction

Fenced code blocks (```` ``` ```` / `~~~`) are skipped entirely — they hold commands
and examples. A fence marker is recognised at any indentation (fences nested in list
items sit 4+ spaces in); over-skipping is the safe direction. Outside fences, three
forms:

- **`@import`** — `@path` at the start of a line or after whitespace, outside inline
  code (Claude Code does not evaluate imports inside code). Trailing `.,;:)` is
  stripped.
- **Markdown local link** — `[text](target)` / `![alt](target)`, outside inline code.
  An optional `"title"` and `<…>` wrapping are removed; the `#fragment` is stripped;
  a target that is only a fragment is ignored.
- **Backtick path** — an inline code span whose content has no whitespace. A trailing
  line reference — `:N`, `:N-M`, `:N:C`, `#LN`, `#LN-LM`, `#LN-M` — is stripped.

Each candidate carries its artifact, 1-based line, and the trimmed line text.

### 4.2 Classification

**Skip** (never flagged), any form:

- URLs and URIs (`scheme://`, `//host/…`, `mailto:`), and scheme-less URLs whose
  first segment is a hostname followed by `/` (`github.com/o/r`, `pkg.go.dev/fmt`).
- Tokens containing `$` (variables),
  glob/brace metacharacters `* ? [ ] { }`, placeholder markers `< >`, `|`, `=`,
  quotes, `(`, `)`, `,`, `…`, or `...`.
- Tokens starting with `-` (flags), bare `~`, or `~user`.
- Placeholder paths: a segment `foo`, `bar`, `baz`, `qux`, or `xxx`, or a `path/to/`
  run.
- `/tmp/…`, and any absolute path (after `~/` expansion) outside every known repo root
  (§4.3).
- Backtick form only: bare filenames with no `/`, tokens containing `@` (package
  scopes, git refs), and relative paths when the artifact has no repo root (no base to
  resolve against).

**Ambiguous** (flagged only if missing, and only after adjudication):

- A backtick path whose last segment has no extension, with no trailing `/` and no
  `./`/`../` prefix — `origin/main`, `owner/repo`, `feat/12-x`, `cmd/tool`.
- A backtick path on a line containing an example marker (`e.g.`, `i.e.`,
  `for example`, `such as`, `example`).
- A backtick path whose first segment exists under no resolution base — nothing of
  the tree it names is present, so it may be another repo's path.
- An `@import` whose last segment has no extension (`@me`, `@types/node`,
  `@anthropic-ai/sdk`).
- A confident ref that is missing **and** gitignored (`git check-ignore`) — a
  local-only or generated path (`.claude/settings.local.json`, `result/bin/x`) that an
  instruction may tell the agent to create. When `git` is unavailable or errors, every
  missing confident ref in that repo is treated as ambiguous: the check fails toward
  adjudication, never toward `dead`.

**Confident** — every other `@import`, markdown link, and backtick path. Resolved
directly: **exists → dropped, missing → `dead`.**

An ambiguous token that resolves to a live path is dropped like any other.

### 4.3 Resolution

- **Repo root** of an artifact: walk up from the artifact's symlink-resolved directory
  to the nearest ancestor containing `.git` (a directory or, in a worktree, a file).
  The **known repo roots** are the roots of every audited artifact.
- **Bases tried per form** — a candidate is live if it exists under **any** base:
  - `@import`: the importing file's directory (both its nominal and symlink-resolved
    forms); `~/` expands to `$HOME`.
  - Markdown link: the artifact's directory (nominal and resolved). An absolute
    target is also tried repo-root-relative (GitHub style); that base comes first
    only when the target lies under no known root.
  - Backtick relative path: the repo root, the artifact's resolved directory, and
    its nominal directory (a symlinked `~/.claude/CLAUDE.md` names files beside the
    symlink as well as beside its target).
  - Absolute path (any form): as-is, then canonicalized with the worktree rules
    `canonicalizeRepoPrefix` applies (in-repo `.worktrees/<name>/` and sibling
    `<repo>-worktrees/<name>/` → the main repo), so a reference into a removed
    worktree is checked against the main checkout.
- "Missing" is an `os.Stat` failure with `ENOENT` or `ENOTDIR` only (symlinks
  followed, so a dangling symlink is missing). Any other error — a permission-denied
  directory, a symlink loop — counts as live: the audit cannot prove absence.
- **Suffix liveness.** A relative backtick ref missing under every base is still live when some
  existing repo path ends in `/<ref>` — instruction files routinely name files relative
  to a subproject (`daemon/conn.go` for `picker/remotebridge/daemon/conn.go`). The
  lookup uses the repo walk below. Links and imports resolve file-relative by
  definition, so they do not get it.
- **Symbol liveness.** A ref is live when stripping a trailing symbol leaves an
  existing path: `:Ident` or `#anchor` after a file, or `.Ident` after an existing file
  or directory (`internal/analyst.ClusterDB`, `cluster.go:ClusterDB`). `Ident` is
  letter-led and, for `.Ident`, not a known file extension — so `docs/plan.md` is not
  kept alive by a `docs/plan/` directory.
- **A path that exists is never flagged** — zero false positives on live files is the
  hard contract.
- For a missing ref, `resolved_to` is the path under the first base, and `same_name`
  lists up to 5 repo-relative files elsewhere in the repo with the same basename —
  the likely new location of a moved file. The repo is walked once, lazily, the first
  time one of its refs is missing under every base (the walk also backs suffix
  liveness); `.git`, `node_modules`, `vendor`, `.worktrees`, `.direnv`,
  `result`, `target`, and `dist` are not descended. The walk is capped at 200,000
  entries. An index that hit the cap, or whose walk met an unreadable subtree, is
  incomplete: suffix liveness then treats every relative backtick ref in that repo
  as live (absence is unprovable), and a capped index yields an empty `same_name`.

Canonicalization caveat: artifacts under a centralized worktree dir
(`<root>/.worktrees/<owner>/<repo>/<branch>/`) canonicalize to a path that does not
exist and are skipped as missing — the same way `analyst cluster` drops them. Their
main-checkout copies are in the audit set on their own.

## 5. Records

### 5.1 `freshness.json` (scan output)

```json
{
  "scanned": ["<artifact>", "..."],
  "skipped": [{"artifact": "<path>", "reason": "missing | unreadable: …"}],
  "dead": [Ref],
  "ambiguous_missing": [Ref]
}
```

`Ref` = `{id, artifact, form, path, line, rule_excerpt, resolved_to, same_name}` —
`id` is a stable hash of artifact + line + path; `form` is `import|link|backtick`;
`path` is the token as written (suffixes stripped); `rule_excerpt` is the trimmed line,
capped.

### 5.2 The `stale-ref` cluster file

The same `Cluster` schema the Oracle consumes, with:

- `cluster_id: "stale-ref::<artifact>"`, `signal_type: "stale-ref"`.
- `artifact`, `artifact_content` (truncated like Track A's), `artifact_exists: true`.
- `incidents: []`, `distinct_sessions: 0`, `recent_sessions: 0`,
  `likely_resolved: false`, `last_seen: ""`.
- `total_incidents` = the number of evidence entries.
- An **`evidence`** array, one entry per stale reference:
  `{path, line, rule_excerpt, resolved_to, same_name}`.

These clusters never pass through `clusterSQL`, so its `HAVING` min-sessions gate
never applies to them. The index entry is a normal `ClusterIndexEntry`
(`sampled_incidents: 0`).

## 6. Integration touch-points

- **`agents/oracle.md`** — a self-contained `stale-ref` section: input is
  `artifact_content` + `evidence[]` (no incidents); output one proposal — `fix-stale`
  (repoint, typically to a `same_name` hit, or correct the path), `remove` (drop a rule
  whose target is gone), or `skip` (the reference is benign). Each `evidence` string
  opens with the ref's `path` in backticks — the key per-ref suppression matches on.
  In a mixed cluster the proposal fixes the refs it judges stale and cites only those;
  benign refs are left out rather than dragging the whole proposal to `skip`. `fix-stale`, `assemble`
  validation, and Editor application already exist.
- **`agents/skeptic.md`** — one line: for a `stale-ref` cluster, verify each cited path
  is still missing on disk and any repoint target exists; there are no windows or
  session counts to check.
- **`internal/analyst`** — `Cluster` gains an optional `evidence` field (omitted for
  Track A); an exported merge writer; the shared canonicalization SQL; an audit-set
  query.
- **No change** to `extractor`, `propose.md`, `apply.md`, `run.md`, or the applier.

## 7. Error handling

- An artifact that no longer exists on disk → skipped and counted.
- An unreadable artifact → reported in `skipped`, run continues.
- The adjudicator erroring, writing no file, or returning an unknown verdict → drop.
- A benign reference → the Oracle's `skip`. Only `closed`/`rejected` entries suppress
  (per ref, §3.3); a `skip` entry is recorded `open` like Track A's skips, so a benign
  ref is re-diagnosed on the next run.

## 8. Testing

- **Unit (`internal/freshness`):** extraction per form (fenced blocks and inline code
  excluded for imports/links), the classifier (confident / ambiguous / skip — every
  skip rule), resolution (per-form bases, worktree canonicalization of absolute paths,
  `@import` `~` expansion, symlinked artifacts, suffix and symbol liveness, line-range
  stripping, gitignored-missing → ambiguous), import expansion with a cycle.
- **Golden fixture** (`internal/freshness/testdata/golden/`): a `CLAUDE.md` mixing
  live refs, dead refs, example paths, `/tmp` paths, `$VAR` paths, URLs, placeholders,
  and fenced blocks → `dead` is exactly the genuine dead refs, and no live path appears
  in `dead` or `ambiguous_missing`.
- **DB path:** a constructed `incidents.db` whose candidates include a worktree copy
  of the fixture → the audit set is the canonical artifact.
- **Schema/merge:** a written `stale-ref` file decodes as an `analyst.Cluster` with
  `evidence`; merge preserves Track A entries and files and replaces prior `stale-ref`
  ones; a subsequent `analyst cluster` run removes the `stale-ref` clusters (§3.4); a
  closed/rejected reason-log entry suppresses exactly the refs its evidence names,
  and a new ref in the same artifact still produces a cluster.
- The LLM adjudication is tested at its deterministic boundary: what `scan` marks
  `ambiguous_missing`, and how `merge` treats adjudication files (keep on `stale`,
  drop otherwise, unparseable file → nothing kept).
