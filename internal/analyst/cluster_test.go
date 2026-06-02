package analyst

import (
	"context"
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

	rows, err := clusterRows(context.Background(), db, 3)
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

	clusters, err := ClusterDB(context.Background(), db, 3)
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
