package analyst

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClusterMarksLikelyResolved(t *testing.T) {
	// A cluster whose newest incident predates the live window while the corpus
	// keeps producing incidents is a fossil: marked, and out of the default fleet.
	dir := t.TempDir()
	live := filepath.Join(dir, "LIVE.md")
	fossil := filepath.Join(dir, "OLD.md")
	for _, p := range []string{live, fossil} {
		if err := os.WriteFile(p, []byte("# rule\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ins := `INSERT INTO incidents VALUES
	 (md5('l1'),'sl1','/p','2026-06-18T10:00:00Z','retry','` + live + `','["` + live + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('l2'),'sl2','/p','2026-06-19T10:00:00Z','retry','` + live + `','["` + live + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('l3'),'sl3','/p','2026-06-20T10:00:00Z','retry','` + live + `','["` + live + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('f1'),'sf1','/p','2026-04-01T10:00:00Z','retry','` + fossil + `','["` + fossil + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('f2'),'sf2','/p','2026-04-02T10:00:00Z','retry','` + fossil + `','["` + fossil + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('f3'),'sf3','/p','2026-04-03T10:00:00Z','retry','` + fossil + `','["` + fossil + `"]'::JSON,'[]'::JSON,'high','{}'::JSON);`
	db := makeIncidentsDB(t, ins)

	clusters, _, err := ClusterDB(context.Background(), db, 3, 0, 3)
	if err != nil {
		t.Fatalf("ClusterDB: %v", err)
	}
	byArtifact := map[string]Cluster{}
	for _, c := range clusters {
		byArtifact[c.Artifact] = c
	}
	if !byArtifact[fossil].LikelyResolved {
		t.Errorf("fossil cluster not marked likely_resolved: %+v", byArtifact[fossil])
	}
	if byArtifact[live].LikelyResolved {
		t.Errorf("live cluster wrongly marked likely_resolved")
	}

	fleet, droppedBacklog, _, _ := RankClusters(clusters, 0, false)
	if len(fleet) != 1 || fleet[0].Artifact != live || droppedBacklog != 1 {
		t.Errorf("fleet=%d droppedBacklog=%d, want only the live cluster / 1", len(fleet), droppedBacklog)
	}

	// --include-stale is the recovery path: the fossil comes back, still marked.
	all, _, _, _ := RankClusters(clusters, 0, true)
	if len(all) != 2 {
		t.Errorf("--include-stale fleet = %d, want 2", len(all))
	}
}

func TestRedirectImport(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string // "" = not a redirect-only file
	}{
		{"bare import", "@AGENTS.md\n", "AGENTS.md"},
		{"see form", "See @AGENTS.md\n", "AGENTS.md"},
		{"heading plus import", "# CLAUDE.md\n\nSee @../shared/AGENTS.md.\n", "../shared/AGENTS.md"},
		{"home import", "@~/.claude/AGENTS.md\n", "~/.claude/AGENTS.md"},
		{"substantive content alongside", "See @AGENTS.md\n\n## Extra rule\n\nAlways run gofmt.\n", ""},
		{"import inside prose", "This repo follows @AGENTS.md for everything else.\n", ""},
		{"two imports", "@AGENTS.md\n@OTHER.md\n", ""},
		{"no import", "# rules\n\nBe careful.\n", ""},
	}
	for _, tc := range cases {
		got, ok := redirectImport(tc.content)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: redirectImport = %q, want not-a-redirect", tc.name, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Errorf("%s: redirectImport = %q,%v; want %q,true", tc.name, got, ok, tc.want)
		}
	}
}

func TestClusterReattributesPointerArtifact(t *testing.T) {
	// A CLAUDE.md that is only "See @AGENTS.md" can never be usefully edited, so
	// the cluster moves to the import target, resolved against the pointer's dir.
	dir := t.TempDir()
	pointer := filepath.Join(dir, "CLAUDE.md")
	target := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(pointer, []byte("# CLAUDE.md\n\nSee @AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# Conventions\n\nUse early returns.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO incidents
	SELECT md5('i' || s), 's' || s, '/p', '2026-05-01T10:00:00Z', 'inefficiency', '` + pointer + `',
	       '["` + pointer + `"]'::JSON, '[]'::JSON, 'high', '{}'::JSON
	FROM range(1,4) AS t(s);`
	db := makeIncidentsDB(t, ins)

	clusters, _, err := ClusterDB(context.Background(), db, 3, 0, 3650)
	if err != nil {
		t.Fatalf("ClusterDB: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	c := clusters[0]
	if c.Artifact != target {
		t.Errorf("artifact = %q, want the import target %q", c.Artifact, target)
	}
	if c.ClusterID != "inefficiency::"+target {
		t.Errorf("cluster_id = %q, want it keyed on the target", c.ClusterID)
	}
	if c.RedirectFrom != pointer {
		t.Errorf("redirect_from = %q, want %q", c.RedirectFrom, pointer)
	}
	if c.ArtifactContent == nil || !strings.Contains(*c.ArtifactContent, "early returns") {
		t.Errorf("artifact_content is not the target's: %v", c.ArtifactContent)
	}
	if c.UnresolvedImport != "" {
		t.Errorf("unresolved_import = %q, want empty", c.UnresolvedImport)
	}
}

func TestClusterFlagsUnresolvablePointerArtifact(t *testing.T) {
	dir := t.TempDir()
	pointer := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(pointer, []byte("See @AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ins := `INSERT INTO incidents
	SELECT md5('i' || s), 's' || s, '/p', '2026-05-01T10:00:00Z', 'inefficiency', '` + pointer + `',
	       '["` + pointer + `"]'::JSON, '[]'::JSON, 'high', '{}'::JSON
	FROM range(1,4) AS t(s);`
	db := makeIncidentsDB(t, ins)

	clusters, _, err := ClusterDB(context.Background(), db, 3, 0, 3650)
	if err != nil {
		t.Fatalf("ClusterDB: %v", err)
	}
	if len(clusters) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(clusters))
	}
	if clusters[0].UnresolvedImport != "AGENTS.md" {
		t.Errorf("unresolved_import = %q, want AGENTS.md", clusters[0].UnresolvedImport)
	}
	if clusters[0].Artifact != pointer {
		t.Errorf("artifact = %q, want the pointer kept so the run can report it", clusters[0].Artifact)
	}
	fleet, _, droppedUnresolved, _ := RankClusters(clusters, 0, false)
	if len(fleet) != 0 || droppedUnresolved != 1 {
		t.Errorf("fleet=%d droppedUnresolved=%d, want 0/1", len(fleet), droppedUnresolved)
	}
}
