package analyst

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestArtifactsCanonicalizesAndDedupes(t *testing.T) {
	// /r/CLAUDE.md appears both bare and via an in-repo worktree copy; /s-worktrees/y
	// is the sibling-worktree layout for a second repo. All four rows must collapse
	// to the two canonical roots, deduplicated.
	ins := `INSERT INTO incidents VALUES
	 (md5('i1'),'s1','/r','2026-05-01T10:00:00Z','x','/r/CLAUDE.md',
	   '["/r/CLAUDE.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('i2'),'s2','/r','2026-05-01T11:00:00Z','x','/r/CLAUDE.md',
	   '["/r/.worktrees/x/CLAUDE.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('i3'),'s3','/s','2026-05-01T12:00:00Z','x','/s/CLAUDE.md',
	   '["/s-worktrees/y/CLAUDE.md"]'::JSON,'[]'::JSON,'high','{}'::JSON),
	 (md5('i4'),'s4','/r','2026-05-01T13:00:00Z','x','/r/CLAUDE.md',
	   '["/r/CLAUDE.md"]'::JSON,'[]'::JSON,'high','{}'::JSON);`
	db := makeIncidentsDB(t, ins)

	got, err := Artifacts(context.Background(), db)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	want := []string{"/r/CLAUDE.md", "/s/CLAUDE.md"}
	if !slices.Equal(got, want) {
		t.Errorf("Artifacts = %v, want %v", got, want)
	}
}

func TestArtifactsEmptyCorpus(t *testing.T) {
	db := makeIncidentsDB(t, "")
	got, err := Artifacts(context.Background(), db)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	if got != nil {
		t.Errorf("Artifacts on empty corpus = %v, want nil", got)
	}
}

func TestMergeClustersReplacesSignalType(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "clusters.json")

	trackA := Cluster{
		ClusterID: "inefficiency::/g/CLAUDE.md", SignalType: "inefficiency",
		Artifact: "/g/CLAUDE.md", ArtifactExists: true,
		DistinctSessions: 3, TotalIncidents: 5,
		Incidents: json.RawMessage(`[{"turn":1}]`),
	}
	if err := WriteClusters([]Cluster{trackA}, indexPath); err != nil {
		t.Fatalf("WriteClusters: %v", err)
	}
	trackAFile := filepath.Join(dir, "clusters", clusterFileName(trackA.ClusterID))
	trackABytes, err := os.ReadFile(trackAFile) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatalf("read track A file: %v", err)
	}

	oldStale := Cluster{
		ClusterID: "stale-ref::/old/FILE.md", SignalType: "stale-ref",
		Artifact: "/old/FILE.md", Evidence: json.RawMessage(`["old evidence"]`),
	}
	if err := MergeClusters([]Cluster{oldStale}, indexPath, "stale-ref"); err != nil {
		t.Fatalf("MergeClusters (old): %v", err)
	}
	oldStaleFile := filepath.Join(dir, "clusters", clusterFileName(oldStale.ClusterID))
	if _, err := os.Stat(oldStaleFile); err != nil {
		t.Fatalf("old stale-ref file missing after first merge: %v", err)
	}

	newStale := Cluster{
		ClusterID: "stale-ref::/new/FILE.md", SignalType: "stale-ref",
		Artifact: "/new/FILE.md", Evidence: json.RawMessage(`["new evidence 1","new evidence 2"]`),
	}
	if err := MergeClusters([]Cluster{newStale}, indexPath, "stale-ref"); err != nil {
		t.Fatalf("MergeClusters (new): %v", err)
	}

	// Track A's entry and file are untouched by the merge.
	gotTrackABytes, err := os.ReadFile(trackAFile) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatalf("track A file gone after merge: %v", err)
	}
	if string(gotTrackABytes) != string(trackABytes) {
		t.Errorf("track A file changed by MergeClusters")
	}

	// The old stale-ref file is gone; the new one exists.
	if _, err := os.Stat(oldStaleFile); !os.IsNotExist(err) {
		t.Errorf("old stale-ref file should be removed, stat err = %v", err)
	}
	newStaleFile := filepath.Join(dir, "clusters", clusterFileName(newStale.ClusterID))
	newStaleData, err := os.ReadFile(newStaleFile) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatalf("new stale-ref file missing: %v", err)
	}
	var decoded Cluster
	if err := json.Unmarshal(newStaleData, &decoded); err != nil {
		t.Fatalf("new stale-ref file round-trip: %v", err)
	}
	var evidence []string
	if err := json.Unmarshal(decoded.Evidence, &evidence); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	if !slices.Equal(evidence, []string{"new evidence 1", "new evidence 2"}) {
		t.Errorf("evidence = %v", evidence)
	}

	// Index holds the kept Track A entry first, then the new stale-ref entry.
	idxData, err := os.ReadFile(indexPath) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	var index []ClusterIndexEntry
	if err := json.Unmarshal(idxData, &index); err != nil {
		t.Fatalf("index round-trip: %v", err)
	}
	if len(index) != 2 || index[0].ClusterID != trackA.ClusterID || index[1].ClusterID != newStale.ClusterID {
		t.Fatalf("index = %+v", index)
	}

	// A later `analyst cluster` run (WriteClusters, Track A only) removes the
	// stale-ref cluster it doesn't know about, entry and file both.
	if err := WriteClusters([]Cluster{trackA}, indexPath); err != nil {
		t.Fatalf("WriteClusters (prune): %v", err)
	}
	if _, err := os.Stat(newStaleFile); !os.IsNotExist(err) {
		t.Errorf("stale-ref file should be pruned by WriteClusters, stat err = %v", err)
	}
	idxData, err = os.ReadFile(indexPath) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatalf("read index after prune: %v", err)
	}
	index = nil
	if err := json.Unmarshal(idxData, &index); err != nil {
		t.Fatalf("index round-trip after prune: %v", err)
	}
	if len(index) != 1 || index[0].ClusterID != trackA.ClusterID {
		t.Fatalf("index after prune = %+v", index)
	}
}

func TestMergeClustersCreatesMissingIndex(t *testing.T) {
	dir := t.TempDir()
	indexPath := filepath.Join(dir, "clusters.json")
	c := Cluster{ClusterID: "stale-ref::/x/FILE.md", SignalType: "stale-ref", Artifact: "/x/FILE.md"}

	if err := MergeClusters([]Cluster{c}, indexPath, "stale-ref"); err != nil {
		t.Fatalf("MergeClusters: %v", err)
	}
	data, err := os.ReadFile(indexPath) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatalf("index not created: %v", err)
	}
	var index []ClusterIndexEntry
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("index round-trip: %v", err)
	}
	if len(index) != 1 || index[0].ClusterID != c.ClusterID {
		t.Fatalf("index = %+v", index)
	}
}

func TestParseEntryEvidence(t *testing.T) {
	content := `# some-id

**Artifact:** /r/CLAUDE.md
**Signal:** stale-ref
**Fix type:** edit  **Confidence:** high  **Date:** 2026-09-28

## Diagnosis

some diagnosis text

## Evidence

- first piece of evidence
- second piece of evidence

## Proposed change

` + "```" + `
diff content
` + "```" + `

## Expected effect

blah
`
	e := parseEntry(content, "irrelevant.md")
	want := []string{"first piece of evidence", "second piece of evidence"}
	if !slices.Equal(e.Evidence, want) {
		t.Errorf("Evidence = %v, want %v", e.Evidence, want)
	}
}

func TestParseEntryNoEvidenceSection(t *testing.T) {
	content := "# some-id\n\n**Artifact:** /r/CLAUDE.md  \n\n## Diagnosis\n\nno evidence section here\n"
	e := parseEntry(content, "irrelevant.md")
	if e.Evidence != nil {
		t.Errorf("Evidence = %v, want nil", e.Evidence)
	}
}
