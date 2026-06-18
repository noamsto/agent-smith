# Safe wide-mine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `/agent-smith:mine` mine the full corpus by default and bound the Opus fan-out with a recency-weighted `--top N` cap plus an always-on checkpoint, so cross-repo and global-artifact glitches surface without an unbounded token bill.

**Architecture:** Push all selection logic into the `analyst` binary (testable Go): the clustering SQL gains recency columns; a new `RankClusters` selects the fleet (recent-first, fossils excluded but surfaced, `--include-stale` backlog mode); `--artifact-prefix` narrows to a repo with dual-layout worktree canonicalization. The `applier` dedups open PRs per *target* repo (fail-open). The four command prompts become thin arg-routers: wide default, exact arg grammar, per-run proposals dir, unconditional checkpoint.

**Tech Stack:** Go 1.23, DuckDB (SQL via the `analyst`/`extractor` binaries), Claude Code command prompts (markdown), nix devshell (`nix develop -c go test ./...`).

**Scope:** This plan is the **core + canonicalization** PR (issue #39). **Out of scope, fast-follow PR:** merged-fix dedup (`reconcile` records `merged_at`; suppress merged-and-quiet clusters) — the recency score is the interim guard. Out of scope entirely: Tier-2 bugs, full déjà-vu.

**All Go work runs in the nix devshell:** prefix Go commands with `nix develop -c` (e.g. `nix develop -c go test ./internal/analyst/...`). `go` is not on the bare PATH.

---

## File structure

| File | Responsibility | Change |
|---|---|---|
| `internal/analyst/cluster.go` | clustering SQL, structs, fleet ranking, prefix filter | Modify |
| `internal/analyst/cluster_test.go` | analyst unit tests | Modify |
| `cmd/analyst/main.go` | `analyst cluster` flag wiring + pipeline + stderr summary | Modify |
| `internal/applier/prepare.go` | proposal resolution + per-repo dedup | Modify |
| `internal/applier/dedup_test.go` | applier dedup unit tests | Modify |
| `cmd/applier/main.go` | `applier prepare` flag wiring | Modify |
| `commands/mine.md` | wide default, arg grammar, checkpoint | Modify |
| `commands/propose.md` | per-run proposals dir | Modify |
| `commands/apply.md` | per-run dir pickup + cleanup + per-target dedup | Modify |
| `commands/run.md` | halt-unless-`yes`, forward args | Modify |
| `.claude-plugin/plugin.json` | plugin version bump | Modify |
| `fixtures/safe-wide-mine.sql` | acceptance fixture builder | Create |

---

## Task 1: Recency columns in clustering

Add `last_seen` and `recent_sessions` to the clustering query and the structs that carry it. `recent_sessions` = distinct sessions whose incident date is within the most recent `staleDays` *active* corpus days (dates with ≥1 incident). All string ops on ISO-8601 `ts` — no date casting.

**Files:**
- Modify: `internal/analyst/cluster.go`
- Test: `internal/analyst/cluster_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/analyst/cluster_test.go`:

```go
func TestClusterRecencyColumns(t *testing.T) {
	// Corpus newest active day = 2026-06-20. With staleDays=3 the live window starts
	// 2026-06-18. Cluster X: 3 sessions, all on/after 06-18 → recent_sessions=3.
	// Cluster Y: 3 sessions, all in May (before the window) → recent_sessions=0.
	ins := `INSERT INTO incidents VALUES
	 (md5('x1'),'sx1','/p','2026-06-18T10:00:00Z','retry','/g/X.md','["/g/X.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('x2'),'sx2','/p','2026-06-19T10:00:00Z','retry','/g/X.md','["/g/X.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('x3'),'sx3','/p','2026-06-20T10:00:00Z','retry','/g/X.md','["/g/X.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('y1'),'sy1','/p','2026-05-01T10:00:00Z','retry','/g/Y.md','["/g/Y.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('y2'),'sy2','/p','2026-05-02T10:00:00Z','retry','/g/Y.md','["/g/Y.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('y3'),'sy3','/p','2026-05-03T10:00:00Z','retry','/g/Y.md','["/g/Y.md"]'::JSON,'[]'::JSON,'high','{}'::JSON);`
	db := makeIncidentsDB(t, ins)

	rows, err := clusterRows(context.Background(), db, 3, 0, 3)
	if err != nil {
		t.Fatalf("clusterRows: %v", err)
	}
	got := map[string]clusterRow{}
	for _, r := range rows {
		got[r.Artifact] = r
	}
	if got["/g/X.md"].RecentSessions != 3 {
		t.Errorf("X recent_sessions = %d, want 3", got["/g/X.md"].RecentSessions)
	}
	if got["/g/Y.md"].RecentSessions != 0 {
		t.Errorf("Y recent_sessions = %d, want 0", got["/g/Y.md"].RecentSessions)
	}
	if got["/g/X.md"].LastSeen != "2026-06-20T10:00:00Z" {
		t.Errorf("X last_seen = %q, want 2026-06-20T10:00:00Z", got["/g/X.md"].LastSeen)
	}
}
```

- [ ] **Step 2: Run it — expect a compile failure**

Run: `nix develop -c go test ./internal/analyst/ -run TestClusterRecencyColumns`
Expected: FAIL — `clusterRows` takes 4 args not 5; `clusterRow` has no `RecentSessions`/`LastSeen`.

- [ ] **Step 3: Add the struct fields**

In `internal/analyst/cluster.go`, add to `clusterRow` (after `TotalIncidents`):

```go
	LastSeen         string          `json:"last_seen"`
	RecentSessions   int             `json:"recent_sessions"`
```

Add the same two fields to `Cluster` (after `TotalIncidents`):

```go
	LastSeen         string          `json:"last_seen"`
	RecentSessions   int             `json:"recent_sessions"`
```

- [ ] **Step 4: Rewrite `clusterSQL` with recency**

Replace the whole `clusterSQL` function with:

```go
// clusterSQL explodes each incident across its candidate artifacts, groups by
// (artifact, signal_type), keeps groups with >= minSessions distinct sessions, and
// aggregates the member incidents into a JSON array per cluster. It also computes
// last_seen and recent_sessions: distinct sessions whose incident date falls within
// the most recent staleDays *active* corpus days (dates with >=1 incident). All
// recency comparisons are string ops on ISO-8601 ts (sortable as text).
// Incidents are sampled session-stratified up to maxIncidents; maxIncidents <= 0 = uncapped.
func clusterSQL(minSessions, maxIncidents, staleDays int) string {
	capN := maxIncidents
	if capN <= 0 {
		capN = math.MaxInt32 // uncapped
	}
	return fmt.Sprintf(`
WITH active_days AS (
  SELECT DISTINCT substr(ts, 1, 10) AS d FROM incidents
),
cutoff AS (
  SELECT min(d) AS live_cutoff FROM (SELECT d FROM active_days ORDER BY d DESC LIMIT %d)
),
exploded AS (
  SELECT incident_id, session_id, ts, confidence, detail, "window", signal_type,
         -- canonicalize worktree copies to the main repo root: in-repo
         -- (<repo>/.worktrees/<name>/) then sibling (<repo>-worktrees/<name>/) layout.
         regexp_replace(
           regexp_replace(unnest(CAST(candidates AS VARCHAR[])), '/\.worktrees/[^/]+/', '/'),
           '([^/]+)-worktrees/[^/]+/', '\1/') AS artifact
  FROM incidents
),
gated AS (
  SELECT e.artifact, e.signal_type,
         count(DISTINCT e.session_id) AS distinct_sessions,
         count(DISTINCT e.incident_id) AS total_incidents,
         max(e.ts) AS last_seen,
         count(DISTINCT e.session_id) FILTER (WHERE substr(e.ts, 1, 10) >= c.live_cutoff) AS recent_sessions
  FROM exploded e CROSS JOIN cutoff c
  GROUP BY e.artifact, e.signal_type
  HAVING count(DISTINCT e.session_id) >= %d
),
ranked AS (
  SELECT e.*,
         row_number() OVER (
           PARTITION BY e.artifact, e.signal_type, e.session_id
           ORDER BY (CASE e.confidence WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END) DESC,
                    e.ts, e.incident_id
         ) AS rn_in_session
  FROM exploded e
  JOIN gated g USING (artifact, signal_type)
),
sampled AS (
  SELECT *,
         row_number() OVER (
           PARTITION BY artifact, signal_type
           ORDER BY rn_in_session ASC,
                    (CASE confidence WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END) DESC,
                    ts, incident_id
         ) AS pick
  FROM ranked
)
SELECT s.artifact,
       s.signal_type,
       g.distinct_sessions,
       g.total_incidents,
       g.last_seen,
       g.recent_sessions,
       to_json(list(struct_pack(
         incident_id := s.incident_id, session_id := s.session_id, ts := s.ts,
         confidence := s.confidence, detail := s.detail, "window" := s."window")
         ORDER BY s.pick)) AS incidents
FROM sampled s
JOIN gated g USING (artifact, signal_type)
WHERE s.pick <= %d
GROUP BY s.artifact, s.signal_type, g.distinct_sessions, g.total_incidents, g.last_seen, g.recent_sessions
ORDER BY g.recent_sessions DESC, g.distinct_sessions DESC, s.artifact, s.signal_type;`,
		staleDays, minSessions, capN)
}
```

- [ ] **Step 5: Thread `staleDays` through `clusterRows` and `ClusterDB`**

Change `clusterRows` signature and call:

```go
func clusterRows(ctx context.Context, db string, minSessions, maxIncidents, staleDays int) ([]clusterRow, error) {
	out, err := queryJSON(ctx, db, clusterSQL(minSessions, maxIncidents, staleDays))
```

Change `ClusterDB` signature, the `clusterRows` call, and the `Cluster{...}` literal:

```go
func ClusterDB(ctx context.Context, db string, minSessions, maxIncidents, staleDays int) (clusters []Cluster, dropped int, err error) {
	rows, err := clusterRows(ctx, db, minSessions, maxIncidents, staleDays)
```

and inside the loop add to the `Cluster{...}` literal (after `TotalIncidents: r.TotalIncidents,`):

```go
			LastSeen:         r.LastSeen,
			RecentSessions:   r.RecentSessions,
```

- [ ] **Step 6: Fix the existing callers' arity in the test file**

In `cluster_test.go`, the existing tests call `clusterRows(ctx, db, 3, 0)` and `ClusterDB(ctx, db, 3, 0)`. Add a `staleDays` arg of `3650` (≈10y, so existing fixtures stay "live"): `clusterRows(context.Background(), db, 3, 0, 3650)` and `ClusterDB(context.Background(), db, 3, 0, 3650)`. There are 3 such calls (`TestClusterExplodesAndGates`, `TestClusterDBBundlesArtifactContent`, `TestClusterCanonicalizesWorktreePaths`).

- [ ] **Step 7: Run the analyst tests**

Run: `nix develop -c go test ./internal/analyst/ -run 'TestCluster'`
Expected: PASS (recency test + the three updated tests).

- [ ] **Step 8: Commit**

```bash
git add internal/analyst/cluster.go internal/analyst/cluster_test.go
git commit -m "feat(analyst): add last_seen + recent_sessions to clustering"
```

---

## Task 2: `RankClusters` — recency-weighted fleet selection

Replace `TopClusters` with `RankClusters`: default fleet = top-`n` clusters with `recent_sessions > 0`, ranked recent-first; backlog (recent_sessions==0) is excluded but counted; `includeStale` ranks all by lifetime breadth.

**Files:**
- Modify: `internal/analyst/cluster.go`
- Test: `internal/analyst/cluster_test.go`

- [ ] **Step 1: Write the failing test**

Add to `cluster_test.go`:

```go
func TestRankClusters(t *testing.T) {
	zombie := Cluster{ClusterID: "z", DistinctSessions: 80, RecentSessions: 1, LastSeen: "2026-06-20T00:00:00Z"}
	active := Cluster{ClusterID: "a", DistinctSessions: 6, RecentSessions: 6, LastSeen: "2026-06-20T00:00:00Z"}
	backlog := Cluster{ClusterID: "b", DistinctSessions: 50, RecentSessions: 0, LastSeen: "2026-05-01T00:00:00Z"}

	fleet, droppedBacklog, droppedTop := RankClusters([]Cluster{zombie, active, backlog}, 8, false)
	if len(fleet) != 2 {
		t.Fatalf("fleet = %d clusters, want 2 (backlog excluded)", len(fleet))
	}
	if fleet[0].ClusterID != "a" {
		t.Errorf("active (6 recent) should outrank zombie (1 recent); got %q first", fleet[0].ClusterID)
	}
	if droppedBacklog != 1 || droppedTop != 0 {
		t.Errorf("droppedBacklog=%d droppedTop=%d, want 1/0", droppedBacklog, droppedTop)
	}

	// includeStale ranks all by lifetime breadth — backlog re-enters, ranked by distinct_sessions.
	all, db2, dt2 := RankClusters([]Cluster{zombie, active, backlog}, 8, true)
	if len(all) != 3 || db2 != 0 || dt2 != 0 {
		t.Fatalf("includeStale: len=%d droppedBacklog=%d droppedTop=%d, want 3/0/0", len(all), db2, dt2)
	}
	if all[0].ClusterID != "z" {
		t.Errorf("backlog mode ranks by lifetime: zombie (80) should lead, got %q", all[0].ClusterID)
	}

	// the cap drops lowest-ranked live clusters and reports the count.
	capped, _, dt3 := RankClusters([]Cluster{zombie, active}, 1, false)
	if len(capped) != 1 || dt3 != 1 || capped[0].ClusterID != "a" {
		t.Errorf("cap: len=%d droppedTop=%d first=%q, want 1/1/a", len(capped), dt3, capped[0].ClusterID)
	}
}
```

- [ ] **Step 2: Run it — expect compile failure**

Run: `nix develop -c go test ./internal/analyst/ -run TestRankClusters`
Expected: FAIL — `RankClusters` undefined.

- [ ] **Step 3: Replace `TopClusters` with `RankClusters`**

In `cluster.go`, delete the `TopClusters` function and add:

```go
// RankClusters selects the diagnosis fleet. By default the fleet is the top n
// clusters with in-window activity (recent_sessions > 0), ranked by recent
// intensity, then lifetime breadth, then recency. Backlog clusters (no in-window
// activity) are excluded from the default fleet and reported via droppedBacklog —
// never deleted; pass includeStale to rank every cluster by lifetime breadth (the
// historical-backlog mode). n <= 0 keeps all selected clusters. droppedTop is how
// many in-fleet candidates the cap dropped.
func RankClusters(clusters []Cluster, n int, includeStale bool) (fleet []Cluster, droppedBacklog, droppedTop int) {
	ranked := make([]Cluster, len(clusters))
	copy(ranked, clusters)
	if includeStale {
		sort.SliceStable(ranked, func(i, j int) bool { return lessBacklog(ranked[i], ranked[j]) })
		fleet, droppedTop = cut(ranked, n)
		return fleet, 0, droppedTop
	}
	var live, backlog []Cluster
	for _, c := range ranked {
		if c.RecentSessions > 0 {
			live = append(live, c)
		} else {
			backlog = append(backlog, c)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return lessLive(live[i], live[j]) })
	fleet, droppedTop = cut(live, n)
	return fleet, len(backlog), droppedTop
}

func cut(ranked []Cluster, n int) (kept []Cluster, dropped int) {
	if n <= 0 || len(ranked) <= n {
		return ranked, 0
	}
	return ranked[:n], len(ranked) - n
}

// lessLive ranks by recent intensity first; lessBacklog by lifetime breadth. Both
// fall back to last_seen (ISO ts, descending) then cluster_id for determinism.
func lessLive(a, b Cluster) bool {
	if a.RecentSessions != b.RecentSessions {
		return a.RecentSessions > b.RecentSessions
	}
	return lessBacklog(a, b)
}

func lessBacklog(a, b Cluster) bool {
	if a.DistinctSessions != b.DistinctSessions {
		return a.DistinctSessions > b.DistinctSessions
	}
	if a.LastSeen != b.LastSeen {
		return a.LastSeen > b.LastSeen
	}
	return a.ClusterID < b.ClusterID
}
```

- [ ] **Step 4: Remove the old `TopClusters` test**

In `cluster_test.go`, delete `TestTopClusters` if present (search for it). The new `TestRankClusters` replaces it.

- [ ] **Step 5: Run the tests**

Run: `nix develop -c go test ./internal/analyst/ -run 'TestRankClusters|TestCluster'`
Expected: PASS. (`cmd/analyst` will not compile yet — fixed in Task 4. Run only the package tests for now.)

- [ ] **Step 6: Commit**

```bash
git add internal/analyst/cluster.go internal/analyst/cluster_test.go
git commit -m "feat(analyst): RankClusters — recency-weighted fleet, backlog mode"
```

---

## Task 3: Dual-layout canonicalization + `--artifact-prefix` filter

The clustering regex already gained the sibling-worktree case in Task 1. Add the Go-side prefix filter that mirrors it, so `repo` narrowing matches canonicalized artifacts from either worktree layout.

**Files:**
- Modify: `internal/analyst/cluster.go`
- Test: `internal/analyst/cluster_test.go`

- [ ] **Step 1: Write the failing test**

Add to `cluster_test.go`:

```go
func TestFilterByPrefix(t *testing.T) {
	clusters := []Cluster{
		{ClusterID: "1", Artifact: "/home/u/repo/CLAUDE.md"},
		{ClusterID: "2", Artifact: "/home/u/other/CLAUDE.md"},
		{ClusterID: "3", Artifact: "/home/u/repo-tools/CLAUDE.md"}, // sibling-name false-match guard
	}
	// Launched from the main checkout.
	got := FilterByPrefix(clusters, "/home/u/repo")
	if len(got) != 1 || got[0].ClusterID != "1" {
		t.Fatalf("main checkout: got %+v, want only cluster 1 (no /repo-tools bleed)", got)
	}
	// Launched from an in-repo worktree → canonicalizes to the same main root.
	got = FilterByPrefix(clusters, "/home/u/repo/.worktrees/feat-x")
	if len(got) != 1 || got[0].ClusterID != "1" {
		t.Errorf(".worktrees layout: got %+v, want cluster 1", got)
	}
	// Launched from a sibling worktree (worktrunk default) → same main root.
	got = FilterByPrefix(clusters, "/home/u/repo-worktrees/feat-x")
	if len(got) != 1 || got[0].ClusterID != "1" {
		t.Errorf("sibling layout: got %+v, want cluster 1", got)
	}
	// Empty prefix is a no-op (wide default).
	if len(FilterByPrefix(clusters, "")) != 3 {
		t.Errorf("empty prefix should keep all")
	}
}
```

- [ ] **Step 2: Run it — expect compile failure**

Run: `nix develop -c go test ./internal/analyst/ -run TestFilterByPrefix`
Expected: FAIL — `FilterByPrefix` undefined.

- [ ] **Step 3: Implement the filter**

Add `"regexp"` and `"strings"` to the `cluster.go` import block. Add:

```go
// Worktree-path canonicalization mirrored from clusterSQL's regexes — KEEP IN SYNC.
// A repo root under either an in-repo (<repo>/.worktrees/<name>/) or sibling
// (<repo>-worktrees/<name>/, worktrunk's default) worktree maps to the main root.
var (
	inRepoWorktreeRe  = regexp.MustCompile(`/\.worktrees/[^/]+/`)
	siblingWorktreeRe = regexp.MustCompile(`([^/]+)-worktrees/[^/]+/`)
)

// canonicalizeRepoPrefix turns a repo root (possibly a worktree root) into the
// canonical main-repo prefix that clusterSQL stores artifacts under, with a
// trailing slash so it can't match a sibling repo ("/x/repo" vs "/x/repo-tools").
func canonicalizeRepoPrefix(repoRoot string) string {
	p := repoRoot
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	p = inRepoWorktreeRe.ReplaceAllString(p, "/")
	p = siblingWorktreeRe.ReplaceAllString(p, "$1/")
	return p
}

// FilterByPrefix keeps clusters whose canonical artifact lives under repoRoot.
// repoRoot == "" is a no-op (the wide default).
func FilterByPrefix(clusters []Cluster, repoRoot string) []Cluster {
	if repoRoot == "" {
		return clusters
	}
	prefix := canonicalizeRepoPrefix(repoRoot)
	out := make([]Cluster, 0, len(clusters))
	for _, c := range clusters {
		if strings.HasPrefix(c.Artifact, prefix) {
			out = append(out, c)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the test**

Run: `nix develop -c go test ./internal/analyst/ -run TestFilterByPrefix`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/analyst/cluster.go internal/analyst/cluster_test.go
git commit -m "feat(analyst): --artifact-prefix filter with dual-layout worktree canonicalization"
```

---

## Task 4: Wire analyst flags + pipeline + stderr summary

Expose `--stale-after-days`, `--include-stale`, `--artifact-prefix`; thread `staleDays` into `ClusterDB`; apply the pipeline order (prefix → reason-log → rank/top); print suppressed counts.

**Files:**
- Modify: `cmd/analyst/main.go`

- [ ] **Step 1: Add the flags**

In `runCluster` (`cmd/analyst/main.go`), after the `top` flag line, add:

```go
	staleDays := fs.Int("stale-after-days", 14, "a cluster is backlog if it has no incidents in the most recent N active corpus days")
	includeStale := fs.Bool("include-stale", false, "rank the historical backlog by lifetime signal instead of excluding it from the fleet")
	artifactPrefix := fs.String("artifact-prefix", "", "keep only clusters whose canonical artifact lives under this repo root (worktree roots canonicalized)")
```

- [ ] **Step 2: Thread staleDays + apply the prefix filter + new ranking**

Replace the body from the `ClusterDB` call through the `TopClusters` call (currently `cmd/analyst/main.go:43-65`) with:

```go
	clusters, dropped, err := analyst.ClusterDB(context.Background(), *db, *minSessions, *maxIncidents, *staleDays)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	if dropped > 0 {
		fmt.Fprintf(os.Stderr, "dropped %d cluster(s) whose canonical artifact no longer exists\n", dropped)
	}
	if *artifactPrefix != "" {
		before := len(clusters)
		clusters = analyst.FilterByPrefix(clusters, *artifactPrefix)
		if d := before - len(clusters); d > 0 {
			fmt.Fprintf(os.Stderr, "--artifact-prefix: kept %d, dropped %d cluster(s) outside %s\n", len(clusters), d, *artifactPrefix)
		}
	}
	entries, err := analyst.ReadEntries(*reasonLog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	clusters, skipped := analyst.FilterRejected(clusters, entries)
	for _, c := range skipped {
		fmt.Fprintf(os.Stderr, "skip %s: a prior proposal was closed/rejected (reason-log)\n", c.ClusterID)
	}
	fleet, droppedBacklog, droppedTop := analyst.RankClusters(clusters, *top, *includeStale)
	if droppedBacklog > 0 {
		fmt.Fprintf(os.Stderr, "recency: %d backlog cluster(s) excluded — no incidents in the last %d active days; --include-stale to rank them\n", droppedBacklog, *staleDays)
	}
	if droppedTop > 0 {
		cutoff := fleet[len(fleet)-1]
		fmt.Fprintf(os.Stderr, "--top %d: dropped %d lower-signal cluster(s); cutoff at %d recent / %d lifetime sessions\n",
			*top, droppedTop, cutoff.RecentSessions, cutoff.DistinctSessions)
	}
	clusters = fleet
```

(The `WriteClusters` call and the final `Printf` below it stay unchanged.)

- [ ] **Step 3: Add recency to the index entry**

In `internal/analyst/cluster.go`, add to `ClusterIndexEntry` (after `TotalIncidents`):

```go
	LastSeen         string `json:"last_seen"`
	RecentSessions   int    `json:"recent_sessions"`
```

and in `WriteClusters`, add to the `ClusterIndexEntry{...}` literal (after `TotalIncidents: c.TotalIncidents,`):

```go
			LastSeen:         c.LastSeen,
			RecentSessions:   c.RecentSessions,
```

- [ ] **Step 4: Build + full analyst suite**

Run: `nix develop -c go build ./... && nix develop -c go test ./internal/analyst/ ./cmd/...`
Expected: PASS / builds clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/analyst/main.go internal/analyst/cluster.go
git commit -m "feat(analyst): wire --artifact-prefix/--stale-after-days/--include-stale + recency pipeline"
```

---

## Task 5: Per-target-repo open-PR dedup (fail-open)

`applier prepare` must check open PRs in each proposal's *target* repo, not the launch repo, and never block on a repo it can't query.

**Files:**
- Modify: `internal/applier/prepare.go`, `cmd/applier/main.go`
- Test: `internal/applier/dedup_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/applier/dedup_test.go`:

```go
func TestPreparePerRepoDedup(t *testing.T) {
	repoA := initRepo(t, "https://github.com/noamsto/app-a.git")
	repoB := initRepo(t, "https://github.com/noamsto/app-b.git")
	fileA := filepath.Join(repoA, "CLAUDE.md")
	fileB := filepath.Join(repoB, "CLAUDE.md")
	proposals := `[
	  {"id":"p-a","implicated_artifact":"` + fileA + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"},
	  {"id":"p-b","implicated_artifact":"` + fileB + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}

	// Learn each entry's branch name (artifact-derived) with dedup off.
	base, err := Prepare(pf, "", DedupConfig{}, false)
	if err != nil {
		t.Fatal(err)
	}
	branchByRepo := map[string]string{}
	for _, e := range base {
		branchByRepo[e.RepoRoot] = e.BranchName
	}

	// Each repo reports an open PR on its OWN proposal's branch.
	cfg := DedupConfig{OpenPRsForRepo: func(root string) ([]PullRequest, error) {
		return []PullRequest{{Number: 1, HeadRefName: branchByRepo[root], URL: "u/" + root}}, nil
	}}
	plan, err := Prepare(pf, "", cfg, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range plan {
		if e.Status != StatusDuplicate {
			t.Errorf("%s: status %q, want duplicate (its own target repo has the open PR)", e.ProposalID, e.Status)
		}
	}
}

func TestPrepareDedupFailsOpen(t *testing.T) {
	repo := initRepo(t, "https://github.com/noamsto/app-a.git")
	file := filepath.Join(repo, "CLAUDE.md")
	proposals := `[
	  {"id":"p-a","implicated_artifact":"` + file + `#x","fix_type":"strengthen",
	   "evidence":["s1:1"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}
	]`
	pf := filepath.Join(t.TempDir(), "proposals.json")
	if err := os.WriteFile(pf, []byte(proposals), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := DedupConfig{OpenPRsForRepo: func(root string) ([]PullRequest, error) {
		return nil, fmt.Errorf("no gh remote")
	}}
	plan, err := Prepare(pf, "", cfg, false)
	if err != nil {
		t.Fatalf("dedup must fail open, not error: %v", err)
	}
	if plan[0].Status != StatusReady {
		t.Errorf("status = %q, want ready (a repo we can't query never blocks)", plan[0].Status)
	}
}
```

Add `"fmt"` to the `dedup_test.go` imports if not present.

- [ ] **Step 2: Run — expect compile failure**

Run: `nix develop -c go test ./internal/applier/ -run TestPreparePerRepoDedup`
Expected: FAIL — `DedupConfig` has no `OpenPRsForRepo`.

- [ ] **Step 3: Change `DedupConfig` and `Prepare`**

In `internal/applier/prepare.go`, replace the `DedupConfig` struct:

```go
// DedupConfig supplies the pending-work dedup gate its two sources: per-repo open
// PRs and the prior reason-log history. Both are optional — a zero config disables
// dedup. OpenPRsForRepo is injected so tests run offline; it is called once per
// distinct target repo and is expected to fail open (return nil, nil) rather than
// erroring on a repo it cannot query.
type DedupConfig struct {
	OpenPRsForRepo func(repoRoot string) ([]PullRequest, error)
	ReasonLogDir   string
}
```

In `Prepare`, delete the up-front `var openPRs []PullRequest { ... }` block (currently lines ~87-92) and replace with a lazy per-repo cache (place it where the deleted block was):

```go
	prByRepo := map[string][]PullRequest{}
	openPRsFor := func(root string) []PullRequest {
		if cfg.OpenPRsForRepo == nil || root == "" {
			return nil
		}
		if prs, ok := prByRepo[root]; ok {
			return prs
		}
		prs, err := cfg.OpenPRsForRepo(root)
		if err != nil {
			prs = nil // fail open: a repo we cannot query never blocks; reason-log dedup still applies
		}
		prByRepo[root] = prs
		return prs
	}
```

In the dedup loop, change the `dedupGate` call to source PRs per target repo:

```go
		if supersedes := dedupGate(byProp[e.ProposalID], e.Target(), openPRsFor(e.RepoRoot), prior); supersedes != "" {
```

- [ ] **Step 4: Rewire the cmd layer**

In `cmd/applier/main.go` `runPrepare`, delete the `repo := fs.String("repo", ...)` flag line, and replace the `cfg` construction:

```go
	cfg := applier.DedupConfig{}
	if !*noDedup {
		cfg = applier.DedupConfig{
			OpenPRsForRepo: func(root string) ([]applier.PullRequest, error) {
				prs, err := applier.GhOpenPRs(applier.Run, root)()
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: open-PR dedup skipped for %s: %v\n", root, err)
					return nil, nil
				}
				return prs, nil
			},
			ReasonLogDir: *reasonLog,
		}
	}
```

- [ ] **Step 5: Run the applier suite**

Run: `nix develop -c go test ./internal/applier/ ./cmd/...`
Expected: PASS (existing dedup/prepare tests use zero `DedupConfig{}` and still compile).

- [ ] **Step 6: Commit**

```bash
git add internal/applier/prepare.go internal/applier/dedup_test.go cmd/applier/main.go
git commit -m "feat(applier): dedup open PRs per target repo, fail-open on unqueryable repos"
```

---

## Task 6: `commands/mine.md` — wide default, arg grammar, checkpoint

Make wide the default; parse the arg grammar; pass `--top 8` and (for `repo`) `--artifact-prefix`; make the checkpoint unconditional.

**Files:**
- Modify: `commands/mine.md`

- [ ] **Step 1: Replace the scope + steps section**

Replace the `**Scope (default: the repo you're launched in).** …` block and the numbered steps `1.`–`4.` (currently `commands/mine.md:18-50`) with:

````markdown
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
   to stderr. `--min-sessions` defaults to 5.
4. **CHECKPOINT (always).** Print one row per fleet cluster from the index:
   `signal_type · artifact basename · repo · recent_sessions · distinct_sessions · last_seen`.
   Below it echo the stderr suppressed summary. Then print the cost estimate:
   *"N clusters → N Oracle + N Skeptic (propose), then ≤N Editor groups + reviewers
   (apply); rough total ≈ N × ~120k = … total tokens (scales with artifact size)."*
   Then **STOP and ask** (proceed / change `top N` / `repo` / `stale` / cancel) —
   **unless `$ARGUMENTS` contained `yes`**, in which case continue without blocking.
````

- [ ] **Step 2: Update the front-matter description**

Replace the `description:` line at the top of `commands/mine.md` with:

```markdown
description: Mine Claude Code session history for recurring glitches — extractor → incidents.db, analyst → clusters.json. Mines the whole corpus by default, recency-capped to the top 8; pass `repo` to narrow, `top N` to recap, `stale` for backlog.
```

- [ ] **Step 3: Commit**

```bash
git add commands/mine.md
git commit -m "feat(mine): wide default + recency-capped checkpoint, repo/top/stale/yes grammar"
```

---

## Task 7: `commands/propose.md` — per-run proposals dir

Use a unique per-run subdir; print the explicit `apply` command (no shared mutable pointer).

**Files:**
- Modify: `commands/propose.md`

- [ ] **Step 1: Replace the input-dir setup**

Find the step that does `mkdir -p /tmp/agent-smith-proposals-in` (currently `commands/propose.md:23`). Replace it with:

```markdown
Create a unique per-run input dir so concurrent runs and prior runs can't bleed:
`RUNID="$(date +%Y%m%dT%H%M%S)-$$"; export PROPOSALS_DIR="/tmp/agent-smith-$RUNID"; mkdir -p "$PROPOSALS_DIR"`.
Use `$PROPOSALS_DIR` everywhere this skill previously used `/tmp/agent-smith-proposals-in`
(Oracle/Skeptic outputs `p-*.json` / `v-*.json`, and the `analyst assemble --proposals-dir "$PROPOSALS_DIR"` call).
```

- [ ] **Step 2: Print the apply command at the end**

At the end of `propose.md` (after assembly), add:

```markdown
Finally, print the exact follow-up so `apply` targets this run's dir, not a stale one:
`echo "next: /agent-smith:apply  (proposals dir: $PROPOSALS_DIR)"`. The apply phase
defaults to the newest `/tmp/agent-smith-*` dir but pass this one explicitly when
multiple runs overlap.
```

- [ ] **Step 3: Commit**

```bash
git add commands/propose.md
git commit -m "feat(propose): per-run proposals dir, print apply handoff"
```

---

## Task 8: `commands/apply.md` — per-run dir pickup, cleanup, per-target dedup

**Files:**
- Modify: `commands/apply.md`

- [ ] **Step 1: Resolve the proposals dir**

Near the top of `apply.md`, before it reads proposals, add:

```markdown
Resolve the proposals dir: if invoked with an explicit dir, use it; else pick the
newest `/tmp/agent-smith-*` dir (`ls -dt /tmp/agent-smith-*/ | head -1`) and **warn
if more than one exists in the last hour** (`find /tmp -maxdepth 1 -name 'agent-smith-*' -mmin -60`)
so overlapping runs surface instead of silently crossing streams. Bind it as `$PROPOSALS_DIR`.
```

- [ ] **Step 2: Drop the per-launch-repo dedup flag**

Find the `applier prepare … --repo .` invocation and **remove the `--repo .` flag** — dedup is now per-target-repo inside `prepare`. Leave the rest of the invocation unchanged.

- [ ] **Step 3: Clean up on success**

At the end of `apply.md`, after PRs are opened, add:

```markdown
On success, remove this run's dir: `rm -rf "$PROPOSALS_DIR"`. Also sweep crashed runs:
`find /tmp -maxdepth 1 -name 'agent-smith-*' -mmin +1440 -exec rm -rf {} +`.
```

- [ ] **Step 4: Commit**

```bash
git add commands/apply.md
git commit -m "feat(apply): per-run dir pickup + cleanup; per-target-repo dedup"
```

---

## Task 9: `commands/run.md` — halt at checkpoint, forward args

**Files:**
- Modify: `commands/run.md`

- [ ] **Step 1: Replace the phase-1 line and the autonomy framing**

Replace the `**agent-smith:mine** (pass `$ARGUMENTS` through …)` bullet and the autonomy sentence with:

```markdown
1. **agent-smith:mine** — forward `$ARGUMENTS` verbatim (`repo` / `top N` / `stale` /
   `yes`). **mine always halts at its checkpoint** (it prints the fleet table + cost,
   then blocks) **unless `$ARGUMENTS` contains `yes`**. So a bare `/agent-smith:run`
   stops for your confirmation after mining; `/agent-smith:run yes` runs hands-off
   (scheduled/cron use) — the `--top` cap is the cost bound either way.
```

- [ ] **Step 2: Update the description**

Replace the `description:` line with:

```markdown
description: Run the whole agent-smith loop — mine (wide, recency-capped) → propose → apply (draft PRs). Halts at the post-mine checkpoint unless you pass `yes`. Args `repo`/`top N`/`stale`/`yes` forward to mine.
```

- [ ] **Step 3: Commit**

```bash
git add commands/run.md
git commit -m "feat(run): halt at mine checkpoint unless 'yes'; forward scope args"
```

---

## Task 10: Plugin version bump

**Files:**
- Modify: `.claude-plugin/plugin.json`

- [ ] **Step 1: Bump the version**

Read `.claude-plugin/plugin.json`; increment the `version` field's minor component (e.g. `0.3.0` → `0.4.0`). The plugin updater skips content re-sync without a bump.

- [ ] **Step 2: Commit**

```bash
git add .claude-plugin/plugin.json
git commit -m "chore(plugin): bump version for safe wide-mine"
```

---

## Task 11: Acceptance fixture + checklist

A frozen fixture DB so the success criteria are reproducible (the live corpus rots).

**Files:**
- Create: `fixtures/safe-wide-mine.sql`

- [ ] **Step 1: Write the fixture builder**

Create `fixtures/safe-wide-mine.sql` encoding the success-criteria scenarios: a global `~/.claude/CLAUDE.md` cluster, a launch-repo cluster, a zombie (high lifetime / 1 recent), an active cluster (high recent), and a pure backlog cluster. Header comment shows how to build it:

```sql
-- Build:  duckdb fixtures/safe-wide-mine.db < fixtures/safe-wide-mine.sql
-- Frozen acceptance fixture for the safe wide-mine success criteria.
CREATE TABLE incidents (
  incident_id VARCHAR PRIMARY KEY, session_id VARCHAR, project VARCHAR, ts VARCHAR,
  signal_type VARCHAR, implicated_artifact VARCHAR, candidates JSON, "window" JSON,
  confidence VARCHAR, detail JSON);
-- (rows: global cluster across 3 sessions in-window; launch-repo cluster in-window;
--  zombie: 1 in-window + many out-of-window sessions; backlog: all out-of-window)
```

(Fill the INSERTs to satisfy criteria 1–6; keep `last_seen` dates relative to a fixed `2026-06-20` frontier.)

- [ ] **Step 2: Run the acceptance checklist (manual)**

Build the fixture, then verify against the spec's success criteria:

```bash
nix develop -c duckdb fixtures/safe-wide-mine.db < fixtures/safe-wide-mine.sql
nix develop -c go run ./cmd/analyst cluster --db fixtures/safe-wide-mine.db --out /tmp/wm/clusters.json --top 8
```
Expected: ≤8 clusters; fleet sorted by `recent_sessions`; zombie below the active cluster; backlog excluded with a stderr note. Then `--include-stale` includes the backlog; `--artifact-prefix <launch-repo>` excludes the global cluster.

- [ ] **Step 3: Full suite + commit**

```bash
nix develop -c go test ./...
git add fixtures/safe-wide-mine.sql
git commit -m "test: frozen acceptance fixture for safe wide-mine"
```

---

## Self-review

**Spec coverage** (each spec component → task):
- C1 wide default → Task 6. C2 recency score + backlog → Tasks 1, 2, 4. C3 `--artifact-prefix` + dual-layout canonicalization → Tasks 1 (SQL), 3 (Go), 4 (wiring). C4 cross-repo dedup (fail-open) → Task 5. C5 per-run dir (no pointer) + cleanup → Tasks 7, 8. C6 checkpoint always + honest cost + run forwards `yes` → Tasks 6, 9. Migration/version → Task 10. Success criteria/fixture → Task 11.
- **Deferred (fast-follow, per the PR-slicing decision):** merged-fix dedup (`reconcile` `merged_at` + suppress merged-and-quiet). Recency score (Task 2) is the interim guard. **Not in this plan by design.**

**Placeholder scan:** Task 11's INSERTs are described, not fully written — acceptable because the fixture is data, not logic, and the shape + constraints are specified; the implementer fills rows to satisfy stated criteria. All code tasks carry complete code.

**Type consistency:** `RankClusters(clusters, n, includeStale) (fleet, droppedBacklog, droppedTop)` is consistent across Tasks 2 and 4. `clusterRows`/`ClusterDB` gain the same trailing `staleDays int` arg (Tasks 1, 4 callers). `DedupConfig.OpenPRsForRepo func(string) ([]PullRequest, error)` matches between Task 5's prepare.go and cmd wiring. `LastSeen string` / `RecentSessions int` field names identical across `clusterRow`, `Cluster`, `ClusterIndexEntry`.

---

## Execution handoff

(Filled by the writing-plans skill's handoff prompt below.)
