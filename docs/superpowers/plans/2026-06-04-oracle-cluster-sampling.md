# Oracle Cluster Sampling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `analyst cluster` emit Oracle-ingestible clusters by capping each cluster's incidents to a session-stratified sample while reporting accurate totals and never truncating `artifact_content`.

**Architecture:** Sampling is done in the clustering SQL (DuckDB window functions); Go stays a thin orchestrator that threads one new param and one new struct field. `incidents.db` remains the full archival record; `clusters.json` becomes the capped, Oracle-ready view.

**Tech Stack:** Go (stdlib only), DuckDB CLI via `queryJSON`. Spec: `docs/superpowers/specs/2026-06-04-oracle-cluster-sampling-design.md`.

---

## File Structure

- `internal/analyst/cluster.go` — `clusterSQL`/`clusterRows`/`ClusterDB` gain a `maxIncidents` param; `Cluster`/`clusterRow` gain `TotalIncidents`; SQL gains `ranked`/`sampled` CTEs + `total_incidents`.
- `internal/analyst/cluster_test.go` — update existing call sites for the new signature; add stratified-cap and uncapped tests.
- `cmd/analyst/main.go` — `--max-incidents-per-cluster` flag (default 24, 0 = uncapped).
- `internal/analyst/oracle.md` — document `total_incidents` and the sampled nature of `incidents[]`.
- `fixtures/analyst/RUNBOOK.md` — note the new flag.

Only one non-test caller of the changing functions exists: `cmd/analyst/main.go:36`.

---

### Task 1: Thread `maxIncidents` + add `TotalIncidents` (no sampling yet)

This task changes signatures and adds `total_incidents` to the SQL/struct **without** sampling logic, so the existing tests keep passing once their call sites are updated. Sampling arrives in Task 2.

**Files:**
- Modify: `internal/analyst/cluster.go`
- Modify: `internal/analyst/cluster_test.go`
- Modify: `cmd/analyst/main.go`

- [ ] **Step 1: Update existing test call sites and add a `TotalIncidents` assertion (failing)**

In `internal/analyst/cluster_test.go`, add `"encoding/json"` to the imports (it is not yet imported). Change the two call sites and assert the new field:

In `TestClusterExplodesAndGates`, replace:

```go
	rows, err := clusterRows(context.Background(), db, 3)
```

with:

```go
	rows, err := clusterRows(context.Background(), db, 3, 0)
```

and after the `DistinctSessions` check add:

```go
	if r.TotalIncidents != 3 {
		t.Errorf("expected total_incidents 3, got %d", r.TotalIncidents)
	}
```

In `TestClusterDBBundlesArtifactContent`, replace:

```go
	clusters, err := ClusterDB(context.Background(), db, 3)
```

with:

```go
	clusters, err := ClusterDB(context.Background(), db, 3, 0)
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nix develop -c go test ./internal/analyst/ -run TestCluster`
Expected: FAIL — compile error (`clusterRows`/`ClusterDB` take 3 args, not 4; `r.TotalIncidents` undefined).

- [ ] **Step 3: Add `TotalIncidents` to the structs and thread `maxIncidents` through the SQL/functions**

In `internal/analyst/cluster.go`, add `TotalIncidents` to both structs (after `DistinctSessions`):

```go
	DistinctSessions int             `json:"distinct_sessions"`
	TotalIncidents   int             `json:"total_incidents"`
```

(Add the identical pair of lines in `Cluster` and in `clusterRow`.)

Replace `clusterSQL` with the param + `total_incidents` projection (still no sampling — selects every incident). `maxIncidents` is accepted but not yet used; an unused *parameter* is legal Go (unlike an unused local), so the build passes. The sampling that consumes it arrives in Task 2:

```go
func clusterSQL(minSessions, maxIncidents int) string {
	_ = maxIncidents // used in Task 2 (session-stratified sampling)
	return fmt.Sprintf(`
WITH exploded AS (
  SELECT incident_id, session_id, ts, confidence, detail, "window", signal_type,
         unnest(CAST(candidates AS VARCHAR[])) AS artifact
  FROM incidents
),
gated AS (
  SELECT artifact, signal_type,
         count(DISTINCT session_id) AS distinct_sessions,
         count(*) AS total_incidents
  FROM exploded
  GROUP BY artifact, signal_type
  HAVING count(DISTINCT session_id) >= %d
)
SELECT e.artifact,
       e.signal_type,
       g.distinct_sessions,
       g.total_incidents,
       to_json(list(struct_pack(
         incident_id := e.incident_id, session_id := e.session_id, ts := e.ts,
         confidence := e.confidence, detail := e.detail, "window" := e."window"))) AS incidents
FROM exploded e
JOIN gated g USING (artifact, signal_type)
GROUP BY e.artifact, e.signal_type, g.distinct_sessions, g.total_incidents
ORDER BY g.distinct_sessions DESC, e.artifact, e.signal_type;`, minSessions)
}
```

Update `clusterRows` and `ClusterDB` signatures and the `TotalIncidents` mapping:

```go
func clusterRows(ctx context.Context, db string, minSessions, maxIncidents int) ([]clusterRow, error) {
	out, err := queryJSON(ctx, db, clusterSQL(minSessions, maxIncidents))
```

```go
func ClusterDB(ctx context.Context, db string, minSessions, maxIncidents int) ([]Cluster, error) {
	rows, err := clusterRows(ctx, db, minSessions, maxIncidents)
```

and in the `Cluster{...}` literal add:

```go
			DistinctSessions: r.DistinctSessions,
			TotalIncidents:   r.TotalIncidents,
```

- [ ] **Step 4: Update the CLI caller so the build passes**

In `cmd/analyst/main.go`, inside `runCluster`, add the flag after `minSessions`:

```go
	maxIncidents := fs.Int("max-incidents-per-cluster", 24, "cap incidents per cluster fed to the Oracle (session-stratified sample); 0 = uncapped")
```

and update the call:

```go
	clusters, err := analyst.ClusterDB(context.Background(), *db, *minSessions, *maxIncidents)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `nix develop -c go build ./... && nix develop -c go test ./internal/analyst/ -run TestCluster`
Expected: PASS (both cluster tests green; `total_incidents` populated).

- [ ] **Step 6: Commit**

```bash
git add internal/analyst/cluster.go internal/analyst/cluster_test.go cmd/analyst/main.go
git commit -m "feat(analyst): thread max-incidents param + report total_incidents"
```

---

### Task 2: Session-stratified sampling in the SQL

**Files:**
- Modify: `internal/analyst/cluster.go`
- Modify: `internal/analyst/cluster_test.go`

- [ ] **Step 1: Write the failing stratified-sample test**

Add to `internal/analyst/cluster_test.go`:

```go
func TestClusterSamplesStratifiedBySession(t *testing.T) {
	// 5 sessions x 3 incidents (15 total) on /g/CLAUDE.md. Each session has one
	// 'high' (i=0) and two 'low'. A cap of 5 must pick exactly one per session and,
	// within a session, prefer the 'high' incident. Counts reflect the full 15/5.
	ins := `INSERT INTO incidents
	SELECT md5('i' || s || '_' || i), 's' || s, '/p', '2026-05-01T10:00:00Z',
	       'inefficiency', '/g/CLAUDE.md',
	       '["/g/CLAUDE.md"]'::JSON, '[{"turn":1}]'::JSON,
	       CASE WHEN i = 0 THEN 'high' ELSE 'low' END, '{}'::JSON
	FROM range(1,6) AS t1(s), range(0,3) AS t2(i);`
	db := makeIncidentsDB(t, ins)

	rows, err := clusterRows(context.Background(), db, 3, 5)
	if err != nil {
		t.Fatalf("clusterRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(rows))
	}
	r := rows[0]
	if r.DistinctSessions != 5 {
		t.Errorf("distinct_sessions = %d, want 5", r.DistinctSessions)
	}
	if r.TotalIncidents != 15 {
		t.Errorf("total_incidents = %d, want 15", r.TotalIncidents)
	}
	var members []struct {
		SessionID  string `json:"session_id"`
		Confidence string `json:"confidence"`
	}
	if err := json.Unmarshal(r.Incidents, &members); err != nil {
		t.Fatalf("unmarshal incidents: %v", err)
	}
	if len(members) != 5 {
		t.Fatalf("sampled %d incidents, want 5 (the cap)", len(members))
	}
	seen := map[string]bool{}
	for _, m := range members {
		if seen[m.SessionID] {
			t.Errorf("session %s appears twice; sampling is not stratified", m.SessionID)
		}
		seen[m.SessionID] = true
		if m.Confidence != "high" {
			t.Errorf("picked a %s incident for %s; want the high-confidence one", m.Confidence, m.SessionID)
		}
	}
	if len(seen) != 5 {
		t.Errorf("covered %d distinct sessions, want 5", len(seen))
	}
}

func TestClusterUncappedKeepsAllIncidents(t *testing.T) {
	// 3 sessions x 2 incidents = 6 total. Uncapped (0) keeps all 6.
	ins := `INSERT INTO incidents
	SELECT md5('i' || s || '_' || i), 's' || s, '/p', '2026-05-01T10:00:00Z',
	       'inefficiency', '/g/CLAUDE.md',
	       '["/g/CLAUDE.md"]'::JSON, '[]'::JSON, 'medium', '{}'::JSON
	FROM range(1,4) AS t1(s), range(0,2) AS t2(i);`
	db := makeIncidentsDB(t, ins)

	rows, err := clusterRows(context.Background(), db, 3, 0)
	if err != nil {
		t.Fatalf("clusterRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(rows))
	}
	if rows[0].TotalIncidents != 6 {
		t.Errorf("total_incidents = %d, want 6", rows[0].TotalIncidents)
	}
	var members []json.RawMessage
	if err := json.Unmarshal(rows[0].Incidents, &members); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(members) != 6 {
		t.Errorf("uncapped kept %d incidents, want all 6", len(members))
	}
}
```

- [ ] **Step 2: Run to verify the new tests fail**

Run: `nix develop -c go test ./internal/analyst/ -run 'TestClusterSamplesStratifiedBySession|TestClusterUncappedKeepsAllIncidents'`
Expected: FAIL — `TestClusterSamplesStratifiedBySession` gets 15 members (no cap applied yet), not 5.

- [ ] **Step 3: Add the `ranked`/`sampled` CTEs and the `pick <= capN` filter**

In `internal/analyst/cluster.go`, replace the **whole** `clusterSQL` function so it samples. The `_ = maxIncidents` line from Task 1 is replaced by the real `capN` guard, and the SQL gains the `ranked`/`sampled` CTEs and the `WHERE s.pick <= %d` filter (note the second `%d` and `capN` as the trailing `Sprintf` arg):

```go
func clusterSQL(minSessions, maxIncidents int) string {
	capN := maxIncidents
	if capN <= 0 {
		capN = 1<<31 - 1 // uncapped
	}
	return fmt.Sprintf(`
WITH exploded AS (
  SELECT incident_id, session_id, ts, confidence, detail, "window", signal_type,
         unnest(CAST(candidates AS VARCHAR[])) AS artifact
  FROM incidents
),
gated AS (
  SELECT artifact, signal_type,
         count(DISTINCT session_id) AS distinct_sessions,
         count(*) AS total_incidents
  FROM exploded
  GROUP BY artifact, signal_type
  HAVING count(DISTINCT session_id) >= %d
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
       to_json(list(struct_pack(
         incident_id := s.incident_id, session_id := s.session_id, ts := s.ts,
         confidence := s.confidence, detail := s.detail, "window" := s."window")
         ORDER BY s.pick)) AS incidents
FROM sampled s
JOIN gated g USING (artifact, signal_type)
WHERE s.pick <= %d
GROUP BY s.artifact, s.signal_type, g.distinct_sessions, g.total_incidents
ORDER BY g.distinct_sessions DESC, s.artifact, s.signal_type;`, minSessions, capN)
}
```

(`ranked.rn_in_session` ranks within a session by confidence; `sampled.pick` round-robins across sessions, so the first `capN` picks spread across distinct sessions before deepening. `CASE` maps the `VARCHAR` confidence to a numeric priority since alphabetical order would rank `low` above `medium`.)

- [ ] **Step 4: Run the full analyst suite to verify all pass**

Run: `nix develop -c go test ./internal/analyst/`
Expected: PASS — including the two new tests and the unchanged `TestClusterExplodesAndGates` (3 incidents, cap 0) / `TestClusterDBBundlesArtifactContent`.

- [ ] **Step 5: Commit**

```bash
git add internal/analyst/cluster.go internal/analyst/cluster_test.go
git commit -m "feat(analyst): session-stratified incident sampling for Oracle ingestion"
```

---

### Task 3: Tell the Oracle the input is a sample

**Files:**
- Modify: `internal/analyst/oracle.md`

- [ ] **Step 1: Document `total_incidents` and the sampled `incidents[]` in the Input section**

In `internal/analyst/oracle.md`, in the `## Input` list, after the `distinct_sessions` bullet, add a `total_incidents` bullet and rewrite the `incidents[]` bullet to state it is a sample. Replace these two lines:

```markdown
- `distinct_sessions` — how many distinct sessions exhibited this (≥3).
- `incidents[]` — the evidence: each has `session_id`, `ts`, `confidence`, `detail`, and `window` (a transcript slice). Reason ONLY from these windows and `artifact_content`. Do not ask for or assume access to anything else.
```

with:

```markdown
- `distinct_sessions` — how many distinct sessions exhibited this (≥3).
- `total_incidents` — how many times this glitch recurred in total across those sessions.
- `incidents[]` — a **representative, session-stratified sample** of `total_incidents` occurrences (at most one per distinct session before deepening), each with `session_id`, `ts`, `confidence`, `detail`, and `window` (a transcript slice). It is a sample, not the full set: the absence of a specific example is NOT evidence it didn't happen — reason from the recurring pattern and the true counts above. Reason ONLY from these windows and `artifact_content`. Do not ask for or assume access to anything else.
```

- [ ] **Step 2: Verify the prompt still has no other claim about incident completeness**

Run: `nix develop -c grep -n "all incidents\|every incident\|complete\|total_incidents\|sample" internal/analyst/oracle.md`
Expected: matches only the lines just added (no contradictory "all/every/complete incidents" language elsewhere).

- [ ] **Step 3: Commit**

```bash
git add internal/analyst/oracle.md
git commit -m "docs(analyst): Oracle prompt — incidents[] is a session-stratified sample"
```

---

### Task 4: Note the flag in the runbook

**Files:**
- Modify: `fixtures/analyst/RUNBOOK.md`

- [ ] **Step 1: Add the flag note after the cluster step**

In `fixtures/analyst/RUNBOOK.md`, immediately after the fenced `analyst cluster` command block (the one writing `/tmp/clusters.json`), add:

```markdown
> The golden-eval cluster has 3 incidents (one per session), so the default
> `--max-incidents-per-cluster 24` is a no-op here. On the real corpus that flag
> caps each cluster to a session-stratified sample (≤24 incidents) so the Oracle
> can ingest high-signal clusters; `total_incidents` still reports the true count.
> Pass `--max-incidents-per-cluster 0` to disable the cap.
```

- [ ] **Step 2: Commit**

```bash
git add fixtures/analyst/RUNBOOK.md
git commit -m "docs(analyst): runbook note on --max-incidents-per-cluster"
```

---

### Task 5: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Build and run the entire suite**

Run: `nix develop -c go build ./... && nix develop -c go test ./...`
Expected: PASS across extractor, analyst, and applier.

- [ ] **Step 2: Smoke-test the CLI flag end to end against the golden-eval fixture**

Run the deterministic prep from `fixtures/analyst/RUNBOOK.md`, then confirm the new field and that the cap is reported:

```bash
FIX="$(pwd)/fixtures/analyst/CLAUDE.md"
nix develop -c go run ./cmd/extractor --corpus 'fixtures/analyst/*.jsonl' \
  --global-claude-md "$FIX" --signals inefficiency --out /tmp/sampling-eval.db
nix develop -c go run ./cmd/analyst cluster --db /tmp/sampling-eval.db \
  --out /tmp/sampling-clusters.json --min-sessions 3 --max-incidents-per-cluster 24
nix develop -c jq '.[] | {cluster_id, distinct_sessions, total_incidents,
  sampled: (.incidents | length)}' /tmp/sampling-clusters.json
```

Expected: one cluster on the fixture `CLAUDE.md`, `distinct_sessions: 3`, `total_incidents: 3`, `sampled: 3` (cap not hit). Cleanup: `gtrash put /tmp/sampling-eval.db /tmp/sampling-clusters.json`.

- [ ] **Step 3: Final commit if any verification fixups were needed**

```bash
git add -A
git commit -m "chore(analyst): verification fixups for cluster sampling"
```

(Skip if the working tree is clean after verification.)
