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

	rows, err := clusterRows(context.Background(), db, 3, 0)
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

	clusters, err := ClusterDB(context.Background(), db, 3, 0)
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
	// The missing-file candidate also forms a >=3 cluster but with no content.
	for i := range clusters {
		if clusters[i].Artifact == missing && (clusters[i].ArtifactExists || clusters[i].ArtifactContent != nil) {
			t.Errorf("missing artifact should have exists=false, content=nil")
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

	rows, err := clusterRows(context.Background(), db, 3, 7)
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
