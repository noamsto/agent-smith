---
description: Mine Claude Code session history for recurring glitches — extractor → incidents.db, analyst → clusters.json. Mines the whole corpus by default, recency-capped to the top 8; pass `repo` to narrow, `top N` to recap, `stale` for backlog.
allowed-tools: Bash, Read
---

You are running the **mine** phase of the agent-smith loop. The deterministic
steps are the `extractor`/`analyst` binaries; artifacts land in the cwd:
`incidents.db`, `clusters.json`.

**Step zero, always:** run the plugin's `scripts/bootstrap.sh` — at
`<base>/scripts/bootstrap.sh` (this command's plugin root); `./scripts/bootstrap.sh`
in a dev checkout; else
`ls -t ~/.claude/plugins/cache/agent-smith/agent-smith/*/scripts/bootstrap.sh | head -1` —
and capture its stdout (one line) as `$BIN`. Prefix every `extractor`/`analyst`/`applier`
invocation with `PATH="$BIN:$PATH"` (each Bash call is a fresh shell; the prefix
also lets the binaries find `duckdb`). If bootstrap fails, stop and show its error.

**Scope (default: WIDE — the whole corpus).** Mining is token-free DuckDB SQL, so
breadth is never the cost lever; the cost is the per-cluster Oracle/Skeptic/Editor
fan-out, bounded by `--top`. Parse `$ARGUMENTS` (space-separated, case-insensitive;
warn on unknown tokens):

- `repo` → narrow to the launch repo. Compute `REPO=$(git rev-parse --show-toplevel)`
  and pass `--artifact-prefix "$REPO"` to `analyst cluster` (the binary canonicalizes
  worktree roots, so a worktree launch still matches its main-repo artifacts).
- `top N` (an integer after `top`) → override the cap. Default cap is **8**.
- `stale` → pass `--include-stale` (rank the historical backlog by lifetime signal).
- `all` → alias for the default (no narrowing).
- `yes` → skip the checkpoint **block** (still print the table). For scripted use.

1. `applier reconcile --reason-log-dir reason-log` — refresh prior PR outcomes before
   mining. Skip only if `gh` is unauthenticated (warn, continue).
2. `extractor --out incidents.db` — always the full corpus; with `--since` omitted it
   auto-resumes from the `incidents.db.last-run` marker.
3. `analyst cluster --db incidents.db --out clusters.json --max-incidents-per-cluster 50 --top 8`
   plus `--artifact-prefix "$REPO"` if `repo`, and `--include-stale` if `stale`. The
   binary applies the pipeline: cluster → drop-missing → artifact-prefix → reason-log
   dedup → recency rank → top-N, and logs suppressed counts (backlog / closed-rejected)
   to stderr. `--min-sessions` defaults to 5; `--stale-after-days` (default 14) sets the
   recency window used to label backlog clusters.
4. **CHECKPOINT (always).** Print one row per fleet cluster from the index:
   `signal_type · artifact basename · repo · recent_sessions · distinct_sessions · last_seen`.
   Below it echo the stderr suppressed summary. Then print the cost estimate:
   *"N clusters → N Oracle + N Skeptic (propose), then ≤N Editor groups + reviewers
   (apply); rough total ≈ N × ~120k = … total tokens (scales with artifact size)."*
   Then **STOP and ask** (proceed / change `top N` / `repo` / `stale` / cancel) —
   **unless `$ARGUMENTS` contained `yes`**, in which case continue without blocking.
