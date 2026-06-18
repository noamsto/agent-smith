package analyst

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestClusterPipelineEndToEnd drives the full analyst selection pipeline
// (ClusterDB → FilterByPrefix → RankClusters) against a constructed incidents.db
// and asserts the safe-wide-mine acceptance properties end-to-end.
//
// Corpus frontier: 2026-06-20. With staleDays=7 the active days are:
//   05-01, 05-02, 05-03, 06-14, 06-15, 06-16, 06-17, 06-18, 06-19, 06-20
//
// The 7 most recent active days are 06-14..06-20, so live_cutoff = 2026-06-14.
// May incidents fall outside the window (< 06-14) and do NOT count as recent.
//
// Four clusters:
//
//	global  — /globalDir/CLAUDE.md, signal inefficiency, 5 distinct sessions,
//	           all in-window (06-16..06-20), RecentSessions=5.
//	repo    — /repoDir/CLAUDE.md,   signal retry,        3 distinct sessions,
//	           all in-window (06-15, 06-17, 06-20), RecentSessions=3.
//	zombie  — /globalDir/BIG.md,    signal tool_error,   6 distinct sessions,
//	           5 in May + 1 on 06-14, RecentSessions=1.
//	backlog — /globalDir/OLD.md,    signal user_correction, 3 distinct sessions,
//	           all in May, RecentSessions=0.
func TestClusterPipelineEndToEnd(t *testing.T) {
	globalDir := t.TempDir()
	repoDir := t.TempDir()

	// Create artifact files on disk (ClusterDB drops clusters with missing files).
	globalArtifact := filepath.Join(globalDir, "CLAUDE.md")
	repoArtifact := filepath.Join(repoDir, "CLAUDE.md")
	zombieArtifact := filepath.Join(globalDir, "BIG.md")
	backlogArtifact := filepath.Join(globalDir, "OLD.md")

	for _, p := range []string{globalArtifact, repoArtifact, zombieArtifact, backlogArtifact} {
		if err := os.WriteFile(p, []byte("# placeholder\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Build the incidents table. Each cluster must have ≥3 distinct session_ids.
	//
	// Active days contributed by each cluster:
	//   global:  06-16, 06-17, 06-18, 06-19, 06-20  (5 days)
	//   repo:    06-15, 06-17, 06-20                 (3 days; 06-17 shared with global)
	//   zombie:  05-01..05-05, 06-14                 (6 days)
	//   backlog: 05-06, 05-07, 05-08                 (3 days)
	//
	// Distinct active days: 05-01..05-08, 06-14..06-20 → 15 days total.
	// Top 7 = 06-14, 06-15, 06-16, 06-17, 06-18, 06-19, 06-20 → live_cutoff = 2026-06-14.
	g := globalArtifact
	r := repoArtifact
	z := zombieArtifact
	b := backlogArtifact

	ins := `INSERT INTO incidents VALUES
  -- global cluster: 5 sessions, all in-window (06-16..06-20)
  (md5('glo1'),'sg1','/p','2026-06-16T10:00:00Z','inefficiency','` + g + `','["` + g + `"]'::JSON,'[]'::JSON,'high','{"f":"a.go"}'::JSON),
  (md5('glo2'),'sg2','/p','2026-06-17T10:00:00Z','inefficiency','` + g + `','["` + g + `"]'::JSON,'[]'::JSON,'high','{"f":"b.go"}'::JSON),
  (md5('glo3'),'sg3','/p','2026-06-18T10:00:00Z','inefficiency','` + g + `','["` + g + `"]'::JSON,'[]'::JSON,'high','{"f":"c.go"}'::JSON),
  (md5('glo4'),'sg4','/p','2026-06-19T10:00:00Z','inefficiency','` + g + `','["` + g + `"]'::JSON,'[]'::JSON,'high','{"f":"d.go"}'::JSON),
  (md5('glo5'),'sg5','/p','2026-06-20T10:00:00Z','inefficiency','` + g + `','["` + g + `"]'::JSON,'[]'::JSON,'high','{"f":"e.go"}'::JSON),
  -- repo cluster: 3 sessions, all in-window (06-15, 06-17, 06-20)
  (md5('rep1'),'sr1','/q','2026-06-15T10:00:00Z','retry','` + r + `','["` + r + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('rep2'),'sr2','/q','2026-06-17T11:00:00Z','retry','` + r + `','["` + r + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('rep3'),'sr3','/q','2026-06-20T11:00:00Z','retry','` + r + `','["` + r + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  -- zombie cluster: 6 sessions, only 1 in-window (06-14), rest in May
  (md5('zmb1'),'sz1','/p','2026-05-01T10:00:00Z','tool_error','` + z + `','["` + z + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('zmb2'),'sz2','/p','2026-05-02T10:00:00Z','tool_error','` + z + `','["` + z + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('zmb3'),'sz3','/p','2026-05-03T10:00:00Z','tool_error','` + z + `','["` + z + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('zmb4'),'sz4','/p','2026-05-04T10:00:00Z','tool_error','` + z + `','["` + z + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('zmb5'),'sz5','/p','2026-05-05T10:00:00Z','tool_error','` + z + `','["` + z + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('zmb6'),'sz6','/p','2026-06-14T10:00:00Z','tool_error','` + z + `','["` + z + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  -- backlog cluster: 3 sessions, all in May (no recent activity)
  (md5('bkl1'),'sb1','/p','2026-05-06T10:00:00Z','user_correction','` + b + `','["` + b + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('bkl2'),'sb2','/p','2026-05-07T10:00:00Z','user_correction','` + b + `','["` + b + `"]'::JSON,'[]'::JSON,'high','{}'::JSON),
  (md5('bkl3'),'sb3','/p','2026-05-08T10:00:00Z','user_correction','` + b + `','["` + b + `"]'::JSON,'[]'::JSON,'high','{}'::JSON);`

	db := makeIncidentsDB(t, ins)

	// --- Phase 1: ClusterDB ---
	clusters, dropped, err := ClusterDB(context.Background(), db, 3, 0, 7)
	if err != nil {
		t.Fatalf("ClusterDB: %v", err)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0 (all artifact files exist)", dropped)
	}
	if len(clusters) != 4 {
		t.Fatalf("ClusterDB returned %d clusters, want 4", len(clusters))
	}

	cByBase := func(name string) *Cluster {
		for i := range clusters {
			if filepath.Base(clusters[i].Artifact) == name {
				return &clusters[i]
			}
		}
		return nil
	}

	// Verify recency columns are what the fixture is built to produce.
	if c := cByBase("BIG.md"); c == nil {
		t.Fatal("zombie cluster (BIG.md) not found")
	} else {
		if c.RecentSessions != 1 {
			t.Errorf("zombie RecentSessions = %d, want 1 (only 06-14 is in-window)", c.RecentSessions)
		}
		if c.DistinctSessions < 6 {
			t.Errorf("zombie DistinctSessions = %d, want >=6", c.DistinctSessions)
		}
	}
	if c := cByBase("OLD.md"); c == nil {
		t.Fatal("backlog cluster (OLD.md) not found")
	} else if c.RecentSessions != 0 {
		t.Errorf("backlog RecentSessions = %d, want 0 (all May)", c.RecentSessions)
	}

	// --- Phase 2: RankClusters (live mode, excludes backlog) ---
	fleet, droppedBacklog, droppedTop := RankClusters(clusters, 8, false)
	if droppedBacklog != 1 {
		t.Errorf("droppedBacklog = %d, want 1 (OLD.md excluded)", droppedBacklog)
	}
	if droppedTop != 0 {
		t.Errorf("droppedTop = %d, want 0 (n=8 is larger than live fleet)", droppedTop)
	}

	inFleet := func(name string) bool {
		for i := range fleet {
			if filepath.Base(fleet[i].Artifact) == name {
				return true
			}
		}
		return false
	}
	if inFleet("OLD.md") {
		t.Error("backlog cluster (OLD.md) must NOT appear in live fleet")
	}

	// Repo (3 recent) must outrank zombie (1 recent) in the fleet ordering.
	repoIdx, zombieIdx := -1, -1
	for i := range fleet {
		switch filepath.Base(fleet[i].Artifact) {
		case "CLAUDE.md":
			// distinguish globalDir from repoDir by full path
			if fleet[i].Artifact == repoArtifact {
				repoIdx = i
			}
		case "BIG.md":
			zombieIdx = i
		}
	}
	if repoIdx < 0 {
		t.Fatal("repo cluster not found in fleet")
	}
	if zombieIdx < 0 {
		t.Fatal("zombie cluster not found in fleet")
	}
	if repoIdx >= zombieIdx {
		t.Errorf("repo cluster (idx=%d, 3 recent) should rank ABOVE zombie (idx=%d, 1 recent)",
			repoIdx, zombieIdx)
	}

	// --- Phase 3: RankClusters (backlog mode, includes everything) ---
	allFleet, droppedBacklog2, _ := RankClusters(clusters, 8, true)
	if droppedBacklog2 != 0 {
		t.Errorf("backlog mode droppedBacklog = %d, want 0 (all included)", droppedBacklog2)
	}
	if len(allFleet) != 4 {
		t.Errorf("backlog mode fleet len = %d, want 4 (all clusters)", len(allFleet))
	}

	// --- Phase 4: FilterByPrefix (narrow to repoDir) ---
	narrow := FilterByPrefix(clusters, repoDir)
	if len(narrow) != 1 {
		t.Fatalf("FilterByPrefix(%q) returned %d clusters, want 1", repoDir, len(narrow))
	}
	if narrow[0].Artifact != repoArtifact {
		t.Errorf("FilterByPrefix kept %q, want %q", narrow[0].Artifact, repoArtifact)
	}

	// Wide mine (empty prefix) keeps all four.
	wide := FilterByPrefix(clusters, "")
	if len(wide) != 4 {
		t.Errorf("wide mine (empty prefix) returned %d clusters, want 4", len(wide))
	}
}
