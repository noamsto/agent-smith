package analyst

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeIncidentsDB builds a minimal incidents.db with controlled rows for testing
// the clustering query. Each call writes a fresh db in a temp dir.
func makeIncidentsDB(t *testing.T, insertSQL string) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "incidents.db")
	ddl := `CREATE TABLE incidents (
	  incident_id VARCHAR PRIMARY KEY, session_id VARCHAR, project VARCHAR, ts VARCHAR,
	  signal_type VARCHAR, implicated_artifact VARCHAR, candidates JSON, "window" JSON,
	  confidence VARCHAR, detail JSON);`
	if _, err := runDuckDB(context.Background(), db, ddl+insertSQL); err != nil {
		t.Fatalf("build incidents.db: %v", err)
	}
	return db
}

func TestClusterExplodesAndGates(t *testing.T) {
	// 3 inefficiency incidents in 3 distinct sessions, all sharing candidate
	// '/g/CLAUDE.md'; each also has a distinct project candidate. Plus a 2-session
	// tool_error group on '/g/CLAUDE.md' that must NOT pass the >=3 gate.
	ins := `INSERT INTO incidents VALUES
	 (md5('i1'),'s1','/p1','2026-05-01T10:00:00Z','inefficiency','/p1/CLAUDE.md',
	   '["/g/CLAUDE.md","/p1/CLAUDE.md"]'::JSON,'[]'::JSON,'high','{"file_path":"a.go"}'::JSON),
	 (md5('i2'),'s2','/p2','2026-05-01T11:00:00Z','inefficiency','/p2/CLAUDE.md',
	   '["/g/CLAUDE.md","/p2/CLAUDE.md"]'::JSON,'[]'::JSON,'high','{"file_path":"b.go"}'::JSON),
	 (md5('i3'),'s3','/p3','2026-05-01T12:00:00Z','inefficiency','/p3/CLAUDE.md',
	   '["/g/CLAUDE.md","/p3/CLAUDE.md"]'::JSON,'[]'::JSON,'medium','{"file_path":"c.go"}'::JSON),
	 (md5('e1'),'s1','/p1','2026-05-01T10:05:00Z','tool_error','/g/CLAUDE.md',
	   '["/g/CLAUDE.md"]'::JSON,'[]'::JSON,'medium','{}'::JSON),
	 (md5('e2'),'s2','/p2','2026-05-01T11:05:00Z','tool_error','/g/CLAUDE.md',
	   '["/g/CLAUDE.md"]'::JSON,'[]'::JSON,'medium','{}'::JSON);`
	db := makeIncidentsDB(t, ins)

	rows, err := clusterRows(context.Background(), db, 3, 0, 3650)
	if err != nil {
		t.Fatalf("clusterRows: %v", err)
	}
	// Only the inefficiency group on /g/CLAUDE.md spans >=3 distinct sessions.
	// Each project candidate has 1 session; tool_error has 2 → all excluded.
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 cluster, got %d: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Artifact != "/g/CLAUDE.md" || r.SignalType != "inefficiency" {
		t.Errorf("wrong cluster key: %s / %s", r.Artifact, r.SignalType)
	}
	if r.DistinctSessions != 3 {
		t.Errorf("expected 3 distinct sessions, got %d", r.DistinctSessions)
	}
	if r.TotalIncidents != 3 {
		t.Errorf("expected total_incidents 3, got %d", r.TotalIncidents)
	}
	if string(r.Incidents) == "" || string(r.Incidents) == "null" {
		t.Errorf("expected member incidents JSON, got %q", r.Incidents)
	}
}

func TestClusterDBBundlesArtifactContent(t *testing.T) {
	// Real artifact file on disk for the cluster's artifact path.
	dir := t.TempDir()
	artifact := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(artifact, []byte("# Reading Code (skeleton-first)\nDon't read whole files.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "gone", "CLAUDE.md")

	ins := `INSERT INTO incidents VALUES
	 (md5('i1'),'s1','/p','2026-05-01T10:00:00Z','inefficiency','` + artifact + `',
	   '["` + artifact + `","` + missing + `"]'::JSON,'[{"turn":1}]'::JSON,'high','{"file_path":"a.go"}'::JSON),
	 (md5('i2'),'s2','/p','2026-05-01T11:00:00Z','inefficiency','` + artifact + `',
	   '["` + artifact + `","` + missing + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('i3'),'s3','/p','2026-05-01T12:00:00Z','inefficiency','` + artifact + `',
	   '["` + artifact + `","` + missing + `"]'::JSON,'[]'::JSON,'high','{}'::JSON);`
	db := makeIncidentsDB(t, ins)

	clusters, dropped, err := ClusterDB(context.Background(), db, 3, 0, 3650)
	if err != nil {
		t.Fatalf("ClusterDB: %v", err)
	}
	var got *Cluster
	for i := range clusters {
		if clusters[i].Artifact == artifact {
			got = &clusters[i]
		}
	}
	if got == nil {
		t.Fatalf("expected a cluster for %s; got %+v", artifact, clusters)
	}
	if got.ClusterID != "inefficiency::"+artifact {
		t.Errorf("cluster_id = %q", got.ClusterID)
	}
	if !got.ArtifactExists || got.ArtifactContent == nil ||
		!strings.Contains(*got.ArtifactContent, "skeleton-first") {
		t.Errorf("expected bundled artifact content with the rule, got exists=%v content=%v",
			got.ArtifactExists, got.ArtifactContent)
	}
	// The missing-file candidate also forms a >=3 cluster, but its canonical
	// artifact is gone, so it is dropped rather than emitted with empty content.
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1 (the missing-artifact cluster)", dropped)
	}
	for i := range clusters {
		if clusters[i].Artifact == missing {
			t.Errorf("missing artifact should be dropped, not emitted: %+v", clusters[i])
		}
	}
}

func TestClusterCanonicalizesWorktreePaths(t *testing.T) {
	// A repo with a real root CLAUDE.md and a separate repo whose root file is
	// gone. Incidents reference the artifacts only through .worktrees/<name>/
	// copies (worktrunk's layout). Canonicalization must (1) merge the three
	// worktree-copy incidents onto the single existing root file, and (2) drop the
	// cluster whose canonical root file no longer exists.
	repo := t.TempDir()
	root := filepath.Join(repo, "CLAUDE.md")
	if err := os.WriteFile(root, []byte("# rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wtA := filepath.Join(repo, ".worktrees", "feat-a", "CLAUDE.md")
	wtB := filepath.Join(repo, ".worktrees", "fix-b", "CLAUDE.md")
	gone := filepath.Join(t.TempDir(), "CLAUDE.md") // canonical file never created
	goneWt := filepath.Join(filepath.Dir(gone), ".worktrees", "feat-c", "CLAUDE.md")

	ins := `INSERT INTO incidents VALUES
	 (md5('i1'),'s1','/p','2026-05-01T10:00:00Z','inefficiency','` + wtA + `',
	   '["` + wtA + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('i2'),'s2','/p','2026-05-01T11:00:00Z','inefficiency','` + wtB + `',
	   '["` + wtB + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('i3'),'s3','/p','2026-05-01T12:00:00Z','inefficiency','` + root + `',
	   '["` + root + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('g1'),'s1','/p','2026-05-01T10:00:00Z','tool_error','` + goneWt + `',
	   '["` + goneWt + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('g2'),'s2','/p','2026-05-01T11:00:00Z','tool_error','` + goneWt + `',
	   '["` + goneWt + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('g3'),'s3','/p','2026-05-01T12:00:00Z','tool_error','` + goneWt + `',
	   '["` + goneWt + `"]'::JSON,'[]'::JSON,'high','{}'::JSON);`
	db := makeIncidentsDB(t, ins)

	clusters, dropped, err := ClusterDB(context.Background(), db, 3, 0, 3650)
	if err != nil {
		t.Fatalf("ClusterDB: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster (worktree copies merged onto the root), got %d: %+v", len(clusters), clusters)
	}
	c := clusters[0]
	if c.Artifact != root {
		t.Errorf("artifact = %q, want canonical root %q", c.Artifact, root)
	}
	if c.DistinctSessions != 3 || c.TotalIncidents != 3 {
		t.Errorf("merge undercounted: sessions=%d incidents=%d, want 3/3", c.DistinctSessions, c.TotalIncidents)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1 (the missing-canonical worktree cluster)", dropped)
	}
}

func TestClusterDBCapsBloat(t *testing.T) {
	dir := t.TempDir()
	artifact := filepath.Join(dir, "CLAUDE.md")
	bigContent := strings.Repeat("x", maxArtifactContentBytes+5000)
	if err := os.WriteFile(artifact, []byte(bigContent), 0o644); err != nil {
		t.Fatal(err)
	}
	bigExcerpt := strings.Repeat("y", maxWindowExcerptBytes+2000)
	win := `[{"turn":1,"type":"assistant","excerpt":"` + bigExcerpt + `"}]`

	ins := `INSERT INTO incidents VALUES
	 (md5('i1'),'s1','/p','2026-05-01T10:00:00Z','inefficiency','` + artifact + `',
	   '["` + artifact + `"]'::JSON,'` + win + `'::JSON,'high','{}'::JSON),
	 (md5('i2'),'s2','/p','2026-05-01T11:00:00Z','inefficiency','` + artifact + `',
	   '["` + artifact + `"]'::JSON,'` + win + `'::JSON,'high','{}'::JSON),
	 (md5('i3'),'s3','/p','2026-05-01T12:00:00Z','inefficiency','` + artifact + `',
	   '["` + artifact + `"]'::JSON,'` + win + `'::JSON,'high','{}'::JSON);`
	db := makeIncidentsDB(t, ins)

	clusters, _, err := ClusterDB(context.Background(), db, 3, 0, 3650)
	if err != nil {
		t.Fatalf("ClusterDB: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	c := clusters[0]
	if c.ArtifactContent == nil || len(*c.ArtifactContent) > maxArtifactContentBytes+len(truncMarker) {
		t.Errorf("artifact_content not capped: len=%v", len(*c.ArtifactContent))
	}
	if !strings.HasSuffix(*c.ArtifactContent, truncMarker) {
		t.Errorf("capped artifact_content missing truncation marker")
	}
	var incidents []struct {
		Window []struct {
			Excerpt string `json:"excerpt"`
		} `json:"window"`
	}
	if err := json.Unmarshal(c.Incidents, &incidents); err != nil {
		t.Fatalf("unmarshal incidents: %v", err)
	}
	for _, inc := range incidents {
		for _, w := range inc.Window {
			if len(w.Excerpt) > maxWindowExcerptBytes+len(truncMarker) {
				t.Errorf("window excerpt not capped: len=%d", len(w.Excerpt))
			}
			if !strings.HasSuffix(w.Excerpt, truncMarker) {
				t.Errorf("capped excerpt missing truncation marker")
			}
		}
	}
}

func TestWriteClustersPerFileAndIndex(t *testing.T) {
	clusters := []Cluster{
		{
			ClusterID: "inefficiency::/g/CLAUDE.md", SignalType: "inefficiency",
			Artifact: "/g/CLAUDE.md", ArtifactExists: true,
			DistinctSessions: 3, TotalIncidents: 9,
			Incidents: json.RawMessage(`[{"turn":1},{"turn":2}]`),
		},
		{
			ClusterID: "tool_error::/p/CLAUDE.md", SignalType: "tool_error",
			Artifact: "/p/CLAUDE.md", ArtifactExists: false,
			DistinctSessions: 4, TotalIncidents: 4,
			Incidents: json.RawMessage(`[]`),
		},
	}
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "clusters.json")
	if err := WriteClusters(clusters, indexPath); err != nil {
		t.Fatal(err)
	}

	idxData, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes := idxData; strings.Count(string(bytes), "\n") < 2 {
		t.Errorf("index is not pretty-printed:\n%s", bytes)
	}
	var index []ClusterIndexEntry
	if err := json.Unmarshal(idxData, &index); err != nil {
		t.Fatalf("index round-trip: %v", err)
	}
	if len(index) != 2 {
		t.Fatalf("expected 2 index entries, got %d", len(index))
	}
	if index[0].SampledIncidents != 2 || index[1].SampledIncidents != 0 {
		t.Errorf("sampled_incidents = %d, %d", index[0].SampledIncidents, index[1].SampledIncidents)
	}

	for _, e := range index {
		full := filepath.Join(dir, e.File)
		data, err := os.ReadFile(full)
		if err != nil {
			t.Fatalf("per-cluster file %s missing: %v", e.File, err)
		}
		if strings.Count(string(data), "\n") < 2 {
			t.Errorf("per-cluster file %s not pretty-printed", e.File)
		}
		var c Cluster
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatalf("per-cluster %s round-trip: %v", e.File, err)
		}
		if c.ClusterID != e.ClusterID {
			t.Errorf("file/index mismatch: %q vs %q", c.ClusterID, e.ClusterID)
		}
	}
}

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

	rows, err := clusterRows(context.Background(), db, 3, 5, 3650)
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

func TestClusterSamplingRoundRobinDeepens(t *testing.T) {
	// 5 sessions x 3 incidents (15 total), cap=7. Round-robin must give every
	// session its best incident first (5 picks), then deepen — so no session may
	// be sampled 3 times while another is sampled once. A naive "ORDER BY
	// confidence DESC LIMIT 7" could pile both extra picks onto one session;
	// round-robin caps every session at 2 until all sessions have 2.
	ins := `INSERT INTO incidents
	SELECT md5('i' || s || '_' || i), 's' || s, '/p', '2026-05-01T10:00:00Z',
	       'inefficiency', '/g/CLAUDE.md',
	       '["/g/CLAUDE.md"]'::JSON, '[{"turn":1}]'::JSON,
	       CASE WHEN i = 0 THEN 'high' ELSE 'low' END, '{}'::JSON
	FROM range(1,6) AS t1(s), range(0,3) AS t2(i);`
	db := makeIncidentsDB(t, ins)

	rows, err := clusterRows(context.Background(), db, 3, 7, 3650)
	if err != nil {
		t.Fatalf("clusterRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(rows))
	}
	var members []struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(rows[0].Incidents, &members); err != nil {
		t.Fatalf("unmarshal incidents: %v", err)
	}
	if len(members) != 7 {
		t.Fatalf("sampled %d incidents, want 7 (the cap)", len(members))
	}
	perSession := map[string]int{}
	for _, m := range members {
		perSession[m.SessionID]++
	}
	if len(perSession) != 5 {
		t.Errorf("covered %d sessions, want all 5 before deepening", len(perSession))
	}
	for sid, n := range perSession {
		if n > 2 {
			t.Errorf("session %s sampled %d times; round-robin must spread to all sessions before taking a 3rd from any", sid, n)
		}
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

	rows, err := clusterRows(context.Background(), db, 3, 0, 3650)
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


func TestClusterRecencyColumns(t *testing.T) {
	// Corpus newest active day = 2026-06-20. With staleDays=3 the live window's 3
	// active days are 06-20/06-19/06-18, so live_cutoff = 2026-06-18 (left edge
	// inclusive). Cluster X has a 4th session on 06-17 — one active day before the
	// cutoff — which must NOT count as recent, proving the left edge is exclusive of
	// 06-17 yet inclusive of 06-18 (a '>' typo would drop 06-18 and yield 2).
	// Cluster Y: 3 sessions, all in May (before the window) → recent_sessions=0.
	ins := `INSERT INTO incidents VALUES
	 (md5('x0'),'sx0','/p','2026-06-17T23:59:59Z','retry','/g/X.md','["/g/X.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
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
	// 4 X sessions span 06-17..06-20, but only the 3 in-window (06-18+) are recent.
	if got["/g/X.md"].RecentSessions != 3 {
		t.Errorf("X recent_sessions = %d, want 3 (06-17 excluded, 06-18 included)", got["/g/X.md"].RecentSessions)
	}
	if got["/g/Y.md"].RecentSessions != 0 {
		t.Errorf("Y recent_sessions = %d, want 0", got["/g/Y.md"].RecentSessions)
	}
	if got["/g/X.md"].LastSeen != "2026-06-20T10:00:00Z" {
		t.Errorf("X last_seen = %q, want 2026-06-20T10:00:00Z", got["/g/X.md"].LastSeen)
	}

	// Degenerate window (staleDays=0): no active days are live, so the cutoff sentinel
	// sits above every real date and nothing counts as recent — everything is backlog.
	zero, err := clusterRows(context.Background(), db, 3, 0, 0)
	if err != nil {
		t.Fatalf("clusterRows(staleDays=0): %v", err)
	}
	for _, r := range zero {
		if r.RecentSessions != 0 {
			t.Errorf("%s recent_sessions = %d with staleDays=0, want 0", r.Artifact, r.RecentSessions)
		}
	}
}

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

	// n <= 0 keeps every selected (live) cluster — the uncapped invariant.
	for _, n := range []int{0, -1} {
		keepAll, db, dt := RankClusters([]Cluster{zombie, active, backlog}, n, false)
		if len(keepAll) != 2 || db != 1 || dt != 0 {
			t.Errorf("n=%d: len=%d droppedBacklog=%d droppedTop=%d, want 2/1/0", n, len(keepAll), db, dt)
		}
	}

	// includeStale ranks all by lifetime breadth — backlog re-enters, ranked by distinct_sessions.
	all, db2, dt2 := RankClusters([]Cluster{zombie, active, backlog}, 8, true)
	if len(all) != 3 || db2 != 0 || dt2 != 0 {
		t.Fatalf("includeStale: len=%d droppedBacklog=%d droppedTop=%d, want 3/0/0", len(all), db2, dt2)
	}
	if all[0].ClusterID != "z" || all[1].ClusterID != "b" || all[2].ClusterID != "a" {
		t.Errorf("backlog mode ranks by lifetime (80/50/6): want z,b,a; got %q,%q,%q", all[0].ClusterID, all[1].ClusterID, all[2].ClusterID)
	}

	// the cap drops lowest-ranked live clusters and reports the count.
	capped, _, dt3 := RankClusters([]Cluster{zombie, active}, 1, false)
	if len(capped) != 1 || dt3 != 1 || capped[0].ClusterID != "a" {
		t.Errorf("cap: len=%d droppedTop=%d first=%q, want 1/1/a", len(capped), dt3, capped[0].ClusterID)
	}

	// tie on recent + lifetime falls back to last_seen (descending), so the fresher one leads.
	older := Cluster{ClusterID: "old", DistinctSessions: 5, RecentSessions: 5, LastSeen: "2026-06-01T00:00:00Z"}
	newer := Cluster{ClusterID: "new", DistinctSessions: 5, RecentSessions: 5, LastSeen: "2026-06-15T00:00:00Z"}
	tied, _, _ := RankClusters([]Cluster{older, newer}, 8, false)
	if tied[0].ClusterID != "new" {
		t.Errorf("last_seen tiebreak: fresher cluster should lead, got %q first", tied[0].ClusterID)
	}
}

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
