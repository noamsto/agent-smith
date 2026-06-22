# Safe wide-mine — always mine wide, recency-capped fan-out, cross-repo-safe

**Issue:** #39 · **Refs:** #36 · **Supersedes:** #37 · **Date:** 2026-06-18 · **Status:** design (rev 3)

## Revision history

- **rev 1** — initial design (decouple cost from scope; wide mine + `--top` + checkpoint).
- **rev 2** — adversarial review: recency made time-only; fossils ranked-not-dropped; checkpoint always-blocks-unless-`yes`; per-run subdir; success criteria; limitations.
- **rev 3** — second adversarial review (this rev). Fixes: recency is now a **recency-weighted score** (`recent_sessions`), not a binary live/fossil flag, killing "zombie" ranking; an explicit **backlog mode** so an empty live fleet no longer claims "the glitches stopped"; the `--artifact-prefix` canonicalization is corrected to the **cluster-stage worktree regex** (rev 2 wrongly named the applier's `mainRepoRoot`/`resolveRealPath`) and **extended to the sibling `<repo>-worktrees/` layout** this repo actually uses; **`merged`-fix dedup** (rev 2 only handled `closed`/`rejected` — verified `merged` is re-proposable); the per-run-dir **pointer race** removed; `/tmp` **cleanup**; honest cost units; **fail-open** cross-repo dedup; frozen **fixture DB** for acceptance.

## Problem

agent-smith's run is two phases with opposite cost profiles:

| Phase | What it is | Token cost |
|-------|-----------|------------|
| **Mine** (`extractor` + `analyst`) | Pure DuckDB SQL over `.jsonl` | **Token-free** — no model (compute grows with history; see Limitations) |
| **Propose + Apply** | One Opus subagent per cluster (Oracle → Skeptic), one Editor per surviving proposal group | **The entire bill** |

A single `one-repo / all` switch controls *both* mining breadth and diagnosis depth. So cutting cost (scope to one repo) also cuts breadth — and the per-repo artifact filter `jq 'select(.artifact | startswith($REPO))'` structurally discards the highest-value clusters: glitches against the global `~/.claude/CLAUDE.md`, global skills, and cross-repo patterns whose artifact doesn't live under the launch repo.

The fix: **decouple cost from scope.** Mining is token-free, so always mine wide; bound cost with a *recency-aware* `--top N` cap, not the repo filter; always checkpoint after mine.

## Design overview

```
extractor (always full corpus; incremental via --since auto-resume; token-free)
        │
        ▼
analyst cluster  --top 8  [--artifact-prefix $REPO]
        │        [--stale-after-days 14] [--include-stale]
        │  pipeline: cluster → drop-missing → artifact-prefix → reason-log dedup
        │           (closed/rejected + merged-with-no-recurrence)
        │           → recency score (recent_sessions) → rank → top-N
        ▼
clusters.json  (the fleet: ≤N clusters with recent activity, ranked by recent intensity)
   + stderr summary: suppressed counts (backlog / closed-rejected / merged-working / below-min)
        │
        ▼
CHECKPOINT (always shown; blocks unless invocation passed `yes`)
        │   fleet table + suppressed summary + honest cost estimate
        ▼
propose (Oracle+Skeptic per cluster)  → apply (Editor per group, per-target-repo dedup)
```

## Components

### 1. Default = wide (`commands/mine.md`)

- Bare `/agent-smith:mine` **drops** the default `--corpus` glob and the post-hoc artifact `jq` filter. `extractor` always runs over the full corpus (token-free, incremental); breadth is never the cost lever.
- Argument grammar — **exact accepted forms** (space-separated tokens in `$ARGUMENTS`, case-insensitive; unknown tokens warn and are ignored):
  | Token(s) | Effect |
  |---|---|
  | *(none)* / `all` | wide, default cap (`--top 8`), checkpoint blocks |
  | `repo` | narrow to launch repo (`--artifact-prefix "$(git rev-parse --show-toplevel)"`) |
  | `top N` | override cap to integer `N` |
  | `stale` | backlog mode (`--include-stale`) — rank historical glitches regardless of recency |
  | `yes` | skip the checkpoint **block** (still prints the table); for scripted/scheduled use |
  - `repo` excludes global `~/.claude` artifacts **by design**; the wide default is how you reach them.

### 2. Recency-weighted ranking + backlog mode (`analyst cluster`)

Recency is computed **purely from incident timestamps** (`ts` is ISO-8601; `incidents.db` has no opportunity data, so no "chance to recur" inference). Per cluster, over **all** its incidents (preserving full breadth):

- `distinct_sessions` (lifetime), `total_incidents`, `last_seen = max(ts)`.
- **`recent_sessions`** = `count(DISTINCT session_id)` with `ts >= live_cutoff`.
- **Live window:** `active_days` = distinct UTC dates with ≥1 incident in the corpus (measuring staleness against *how much you've actually worked since*, not wall-clock — a vacation shouldn't age out a glitch; for a daily user active≈calendar). `live_cutoff` = start of the most recent `D` active days (default `D=14`).

**Ranking (default):** `recent_sessions DESC`, then `distinct_sessions DESC`, then `last_seen DESC`. This is the fix for "zombie" clusters: a mostly-dead 80-session cluster with one stray recent incident has `recent_sessions=1` and ranks **below** a genuinely-active 6-this-week cluster (`recent_sessions=6`) — lifetime volume no longer jumps the queue.

**Fleet (default):** top-`N` of clusters with `recent_sessions > 0`. If fewer than `N` qualify, the fleet is **smaller** (don't pay to diagnose inactive glitches). `recent_sessions = 0` ⇒ backlog, excluded from the default fleet but counted on stderr; per-`clusters/<id>.json` files are written for *all* clusters (nothing lost).

**Backlog mode (`--include-stale`, alias `stale`):** rank by `distinct_sessions DESC, last_seen DESC` over **all** clusters (ignore the window); fleet = top-`N`. This is the explicit answer to "I just started agent-smith / I changed my own behavior — fix the historical glitches even if they've gone quiet." An empty default fleet must **not** claim "the glitches stopped" (it can't distinguish *fixed* from *not-recently-occurring*); it says *"0 clusters active in the last D active days; M in backlog — `--include-stale` to rank them."*

**Merged-fix dedup (fixes C12, verified):** `rejectedKeys` (`reasonlog.go`) currently suppresses only `closed`/`rejected`; **`merged` is re-proposable**, so a wide run re-opens already-merged fixes until they age out. The recency score mitigates the steady state (a working fix → `recent_sessions → 0`), but pre-merge incidents stay in the window briefly. So:
- `applier reconcile` records **`merged_at`** when it sets `outcome: merged` (from `gh pr view --json mergedAt` — cheap).
- A `merged` `(artifact, signal)` cluster is **suppressed** iff it has **no incidents with `ts > merged_at`** (no recurrence since the fix). If it **recurred after merge**, it is **kept and ranked normally** — a merged fix that didn't work is high-value, not noise.

Flags: `--stale-after-days D` (default 14), `--include-stale` (default false), `--top N`.

### 3. `--artifact-prefix` on `analyst cluster` (ordering + canonicalization, corrected)

Today the binary applies `--top` (`cmd/analyst/main.go:56-65`) and *then* the prompt's `jq` filter runs — so `repo top N` caps across the whole corpus, then discards out-of-repo clusters, and can dispatch **0**. Fix: `--artifact-prefix P` filters by canonical artifact prefix **before** ranking/`--top`; delete the `jq` filter.

**Canonicalization (corrected from rev 2).** Cluster artifacts are canonicalized **at cluster time by a SQL regex**, not by the applier's `mainRepoRoot`/`resolveRealPath` (those run in a later stage and never touch these paths). Today that regex is `regexp_replace(unnest(candidates), '/\.worktrees/[^/]+/', '/')` (`cluster.go:66`) — it maps the **in-repo** `<repo>/.worktrees/<name>/` layout back to the repo root, and handled 1200 historical incidents. But this repo's **current** worktrees are **siblings** (`<repo>-worktrees/<name>/`, per `wt list`), which that regex never matches — so the next mine will leave those artifacts un-canonicalized and `repo` narrowing (and cluster dedup) silently misses them.

So component 3 must:
1. **Extend the cluster-stage canonicalization to the sibling layout:** add `regexp_replace(path, '([^/]+)-worktrees/[^/]+/', '\1/')` so `…/agent-smith-worktrees/feat-x/foo` → `…/agent-smith/foo`. Both worktree conventions now canonicalize to the main repo root.
2. **`--artifact-prefix` canonicalizes `$REPO` to the same main-repo root** before matching — via `git` (`git rev-parse --git-common-dir` → main worktree), which is **layout-agnostic** and avoids coupling the prefix to the regexes. Append a trailing `/` to avoid sibling-repo false matches (`/x/repo` vs `/x/repo-tools`).
3. **Limitation (stated, not hidden):** the *artifact* side still relies on regexes, so any future worktree layout needs another regex line. The robust-but-costlier alternative — git-resolve every artifact at cluster time — is a follow-up (Limitations).

### 4. Cross-repo open-PR dedup (`applier`) — fail-open

`apply` runs `applier prepare --repo .`; the open-PR list is built once for the launch repo (`GhOpenPRs(run, dir)`) and reused for every proposal, so wide-mine re-opens **duplicate PRs in other repos**. Fix: each proposal's `Target` carries a canonical `RepoRoot` (`resolve.go`); build the open-PR list **per distinct `Target.RepoRoot`** (`GhOpenPRs(run, target.RepoRoot)`) and dedup against the matching one (a `map[RepoRoot][]PullRequest`).
- **Fail-open:** a target repo with no GitHub remote / no `gh` access (wide-mine makes heterogeneous remotes likely) **skips its dedup with a warning** — never blocks the run. Reason-log dedup (global) still applies.

### 5. Per-run proposals dir (`propose`, `apply`) — supersedes #37, no pointer race

`/tmp/agent-smith-proposals-in` is shared and never cleared → stale cross-repo proposals re-assembled. #37 wipes in place, which **races a concurrent run** (multiple Claude sessions per repo). This **supersedes #37** with per-run subdirs and **no shared mutable pointer**:

- `run` generates `RUNID` (`date +%Y%m%dT%H%M%S-$$`) once and passes it to `propose` and `apply`; both use `/tmp/agent-smith-$RUNID/`.
- Standalone `propose`: generate `RUNID`, create the subdir, and **print the exact `apply --proposals-dir <dir>` command** to run next (no pointer file to clobber).
- Standalone `apply`: `--proposals-dir` if given; else pick the **newest** `/tmp/agent-smith-*` subdir **and warn if more than one is recent** (so concurrent runs surface instead of silently crossing streams).
- **Cleanup:** `apply` removes its subdir on success; a startup sweep removes `agent-smith-*` dirs older than 24h (crashed runs).

### 6. Checkpoint (`commands/mine.md`, `run.md`) — always shown

- **Unconditional** (today only in the `all` branch). Always prints the fleet table + suppressed summary; **always blocks** unless the invocation passed `yes`. **`top N` does NOT skip the block** — bounding cost and previewing it are independent.
- `--top 8` passed **by default** at cluster time, so table and cap match.
- Fleet table: `signal_type · artifact basename · repo · recent_sessions · distinct_sessions · last_seen`. Below: `suppressed: M backlog (recent_sessions=0; 'stale' to rank), K closed/rejected, J merged-and-quiet`.
- **Cost estimate — honest.** Rough, **total tokens** (input+output), scales with artifact size. Calibration datapoint: comparable analysis subagents in development ran **60–90k total each**. Estimate Oracle+Skeptic ≈ **80–150k/cluster**, Editor group + reviewers ≈ **60–120k each**; a full top-8 run ≈ **1–2M total tokens**. State the formula + per-unit range + N-scaled total — never a fake-precise single number. **TODO before merge:** measure one real Oracle+Skeptic dispatch and replace these with cited numbers.
- **`/agent-smith:run` halts here** by not forwarding `yes`. Scheduled/autonomous use: `/agent-smith:run yes` forwards `yes` (and any `repo`/`top`/`stale`) **through `run` into `mine`**. So the decision is precisely "run halts unless `yes`," and the forwarding is explicit — not a contradiction.

## Decisions

- Default cap **N = 8**; window **D = 14** active days.
- Recency is a **weighted score** (`recent_sessions`), not a binary gate; backlog reachable via `stale`/`--include-stale`.
- **`merged` fixes** are suppressed only when they have **not recurred since merge**; recurrence-after-merge is kept and ranked.
- Fossils/backlog **excluded from the fleet but surfaced**, never silently dropped.
- `/agent-smith:run` **halts unless `yes`**; `run yes` forwards to `mine`.
- **Supersedes #37.**

## Success criteria (acceptance bar — run against a frozen fixture `incidents.db`, not the live corpus)

The criteria reference a **committed fixture DB** (`fixtures/safe-wide-mine.db`) so they're reproducible (the live corpus's "last seen" dates rot on every mine). On that fixture:
1. A bare `/agent-smith:mine` dispatches **≤ 8** Oracles.
2. A global `~/.claude/*` cluster appears in the fleet of a bare run but is **excluded under `repo`** — wide-mine reaches global artifacts single-repo scope filters out.
3. A zombie (80 lifetime sessions, 1 in-window) ranks **below** an active cluster (6 in-window) — `recent_sessions` ordering.
4. A pure backlog cluster (recent_sessions=0) is excluded by default and **included under `stale`**.
5. A `merged`-and-quiet `(artifact,signal)` is suppressed; the same artifact **recurring after `merged_at`** is kept.
6. `/agent-smith:mine repo` from a **worktree** (both `.worktrees/` and sibling `-worktrees/`) returns the launch repo's clusters, not 0.
7. Per-target-repo dedup opens **no duplicate PR**; a remote-less target **warns and proceeds**.
8. `nix develop -c go test ./...` green.

## Error handling & edge cases

- **`--artifact-prefix` matches 0:** checkpoint reports "0 clusters in `<repo>` — widen with bare `/agent-smith:mine`?" and stops.
- **Empty live fleet:** show the backlog count, suggest `stale`; do **not** claim the glitches stopped.
- **`top N` > qualifying clusters:** keep all qualifying.
- **`ts`** parsed as ISO-8601 UTC for `active_days`/`recent_sessions`.

## Testing

Push logic into the **binaries** (testable Go); keep prompts as thin arg-routers/renderers.
- **Go unit tests:** `recent_sessions` + ranking comparator (zombie case, boundary at `live_cutoff`); `--include-stale` backlog ranking; `--artifact-prefix` canonicalization for **both** worktree layouts + trailing-sep + ordering-before-cap; merged-with-recurrence vs merged-and-quiet suppression; per-target-repo dedup with one remote-less target (fail-open).
- **Fixture:** build `fixtures/safe-wide-mine.db` encoding the success-criteria scenarios; the acceptance checklist runs against it.
- **Prompt layer:** no unit tests; verification = the success-criteria checklist (criteria 1–7) on the fixture, plus a `repo`-from-worktree smoke test.
- `nix develop -c go test ./...` green.

## Known limitations / follow-ups (separate issues)

- **`incidents.db` grows unbounded; `analyst cluster` re-clusters the whole table each run** (recency computed over all of it). "Token-free" is a model-cost claim only. Follow-up: window/prune.
- **Artifact canonicalization is regex-coupled** to known worktree layouts (component 3.3); a git-resolve-per-artifact pass would be layout-agnostic but costlier.
- **Merged-fix handling is a déjà-vu *slice*** (recurrence-since-merge), not the full trend validation (does the rate drop? confounders?). Full déjà-vu remains Tier-3.

## Scope & sequencing (the spec has grown — read this)

Three folded-in fixes are arguably their own units; flagged so we can split if you want a tighter first PR:
- **Worktree-layout canonicalization (3.1)** is a **pre-existing bug** wide-mine merely exposes — could ship first as its own fix.
- **Merged-fix dedup (2, C12)** touches `reconcile` (`merged_at`) and is déjà-vu-adjacent — could defer, with the recency score as the interim guard.
- The rest (wide default, recency score, `--artifact-prefix`, cross-repo dedup, per-run dir, checkpoint) is the cohesive **core**.
Recommendation: one PR for the core + 3.1 (canonicalization is load-bearing for `repo`); merged-fix dedup as a fast follow if scope feels heavy.

## Migration & versioning

- **Bare `/agent-smith:mine` changes meaning** (was "this repo"; now "wide"). `repo` is the opt-in for the old default; `all` still works. Note in the PR body and command `description:` lines.
- **Plugin version bump** in `.claude-plugin/plugin.json` (repo convention; the updater skips content re-sync without it).
- **Scheduled runs** pass `/agent-smith:run yes`.

## Out of scope (tracked separately)

- **Tier-2 bugs** — reason-log partial-write corruption, `scanReasonLog` error-swallow, legacy outcome-marker migration, skeptic `inconclusive` verdict + skip-skipping. Separate PRs.
- **Tier-3** — pointer-file (`See @AGENTS.md`) reattribution (#36 item 3); full déjà-vu trend validation; HTML dashboard (#2); `incidents.db` retention.
