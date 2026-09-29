package freshness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// writeReasonLogEntry writes one reason-log entry through the real writer (so
// the parser under test is exercised against its actual on-disk format), then
// patches the outcome marker to the state the test needs.
func writeReasonLogEntry(t *testing.T, dir, id, artifact, signal, outcome string, evidence []string) {
	t.Helper()
	prop := analyst.Proposal{
		ID:                 id,
		ImplicatedArtifact: artifact,
		SignalType:         signal,
		FixType:            "fix-stale",
		Confidence:         "high",
		Evidence:           evidence,
		Diagnosis:          "d",
		ProposedChange:     "c",
		ReasonLog:          "r",
	}
	if _, err := analyst.WriteReasonLogs([]analyst.Proposal{prop}, dir, "2026-01-01"); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	var path string
	for _, p := range paths {
		data, err := os.ReadFile(p) //nolint:gosec // path is under the test temp dir
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(string(data), "# "+id+"\n") {
			path = p
			break
		}
	}
	if path == "" {
		t.Fatalf("no reason-log file written for %q in %s", id, dir)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatal(err)
	}
	content, ok := analyst.SetOutcome(string(data), outcome)
	if !ok {
		t.Fatalf("%s: no outcome marker to set", path)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func decodeEvidence(t *testing.T, raw json.RawMessage) []evidence {
	t.Helper()
	var ev []evidence
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("decode evidence: %v", err)
	}
	return ev
}

func TestLoadAdjudicationsEmptyDir(t *testing.T) {
	stale, errs := LoadAdjudications("")
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if stale == nil || len(stale) != 0 {
		t.Fatalf("stale = %#v, want an empty non-nil map", stale)
	}
}

func TestLoadAdjudicationsVerdicts(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "adj-1.json"), `[
		{"id": "no-verdict"},
		{"id": "dropped", "verdict": "drop"},
		{"id": "wrong-case", "verdict": "Stale"},
		{"id": "kept", "verdict": "stale"}
	]`)
	writeFile(t, filepath.Join(dir, "adj-bad.json"), `not json`)
	writeFile(t, filepath.Join(dir, "adj-fenced.json"), "```json\n[{\"id\": \"fenced-kept\", \"verdict\": \"stale\"}]\n```")

	stale, errs := LoadAdjudications(dir)
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly 1 (for adj-bad.json)", errs)
	}
	want := map[string]bool{"kept": true, "fenced-kept": true}
	if !reflect.DeepEqual(stale, want) {
		t.Fatalf("stale = %v, want %v", stale, want)
	}
}

func TestLoadAdjudicationsBadDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "adj-1.json")
	writeFile(t, file, `[]`)
	for _, d := range []string{filepath.Join(dir, "missing"), file} {
		stale, errs := LoadAdjudications(d)
		if len(errs) != 1 || len(stale) != 0 || stale == nil {
			t.Errorf("LoadAdjudications(%q) = %v, %v; want empty map and one error", d, stale, errs)
		}
	}
}

func TestLoadAdjudicationsGlobMetacharDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "adj[1]")
	writeFile(t, filepath.Join(dir, "adj-1.json"), `[{"id": "kept", "verdict": "stale"}]`)
	stale, errs := LoadAdjudications(dir)
	if len(errs) != 0 || !stale["kept"] {
		t.Fatalf("stale = %v, errs = %v; want kept read from a dir whose name has glob metacharacters", stale, errs)
	}
}

func TestClustersSuppressionLeadingTokenOnly(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "body\n")

	r := Report{Dead: []Ref{{ID: "d1", Artifact: artifact, Path: "old/a.go", Line: 3}}}

	rlDir := t.TempDir()
	writeReasonLogEntry(t, rlDir, "p1", artifact, SignalType, analyst.OutcomeClosed,
		[]string{"`old/a.go` (line 3) → `x.go`"})
	entries, err := analyst.ReadEntries(rlDir)
	if err != nil {
		t.Fatal(err)
	}

	clusters, suppressed, errs := Clusters(r, map[string]bool{}, entries)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(clusters) != 0 {
		t.Fatalf("clusters = %v, want none — the only ref was suppressed", clusters)
	}
	if len(suppressed) != 1 || suppressed[0].Path != "old/a.go" {
		t.Fatalf("suppressed = %v, want [old/a.go]", suppressed)
	}
}

func TestClustersSuppressionIgnoresLaterToken(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "body\n")

	// The bullet names new/a.go as the repoint TARGET, not the leading token —
	// it must not suppress a ref whose path is new/a.go.
	r := Report{Dead: []Ref{{ID: "d1", Artifact: artifact, Path: "new/a.go", Line: 3}}}

	rlDir := t.TempDir()
	writeReasonLogEntry(t, rlDir, "p1", artifact, SignalType, analyst.OutcomeClosed,
		[]string{"`old/a.go` (line 3) → `new/a.go`"})
	entries, err := analyst.ReadEntries(rlDir)
	if err != nil {
		t.Fatal(err)
	}

	clusters, suppressed, errs := Clusters(r, map[string]bool{}, entries)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none", suppressed)
	}
	if len(clusters) != 1 || len(decodeEvidence(t, clusters[0].Evidence)) != 1 {
		t.Fatalf("clusters = %+v, want one cluster with one ref", clusters)
	}
}

func TestClustersOpenOutcomeDoesNotSuppress(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "body\n")

	r := Report{Dead: []Ref{{ID: "d1", Artifact: artifact, Path: "old/a.go", Line: 3}}}

	rlDir := t.TempDir()
	writeReasonLogEntry(t, rlDir, "p1", artifact, SignalType, analyst.OutcomeOpen,
		[]string{"`old/a.go` (line 3) → `x.go`"})
	entries, err := analyst.ReadEntries(rlDir)
	if err != nil {
		t.Fatal(err)
	}

	clusters, suppressed, errs := Clusters(r, map[string]bool{}, entries)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none — an open entry doesn't suppress", suppressed)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %v, want 1", clusters)
	}
}

func TestClustersDifferentSignalDoesNotSuppress(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "body\n")

	r := Report{Dead: []Ref{{ID: "d1", Artifact: artifact, Path: "old/a.go", Line: 3}}}

	rlDir := t.TempDir()
	writeReasonLogEntry(t, rlDir, "p1", artifact, "some-other-signal", analyst.OutcomeClosed,
		[]string{"`old/a.go` (line 3) → `x.go`"})
	entries, err := analyst.ReadEntries(rlDir)
	if err != nil {
		t.Fatal(err)
	}

	clusters, suppressed, errs := Clusters(r, map[string]bool{}, entries)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none — a different signal doesn't suppress", suppressed)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %v, want 1", clusters)
	}
}

func TestClustersPartialSuppressionSameArtifact(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "body\n")

	r := Report{Dead: []Ref{
		{ID: "d1", Artifact: artifact, Path: "old/a.go", Line: 3},
		{ID: "d2", Artifact: artifact, Path: "old/b.go", Line: 9},
	}}

	rlDir := t.TempDir()
	writeReasonLogEntry(t, rlDir, "p1", artifact, SignalType, analyst.OutcomeClosed,
		[]string{"`old/a.go` (line 3) → `x.go`"})
	entries, err := analyst.ReadEntries(rlDir)
	if err != nil {
		t.Fatal(err)
	}

	clusters, suppressed, errs := Clusters(r, map[string]bool{}, entries)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(suppressed) != 1 || suppressed[0].Path != "old/a.go" {
		t.Fatalf("suppressed = %v, want [old/a.go]", suppressed)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %v, want 1", clusters)
	}
	ev := decodeEvidence(t, clusters[0].Evidence)
	if len(ev) != 1 || ev[0].Path != "old/b.go" {
		t.Fatalf("evidence = %+v, want just old/b.go", ev)
	}
}

func TestClustersAmbiguousNeedsAdjudication(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "body\n")

	r := Report{AmbiguousMissing: []Ref{
		{ID: "amb-undecided", Artifact: artifact, Path: "cmd/tool", Line: 5},
		{ID: "amb-stale", Artifact: artifact, Path: "cmd/other", Line: 6},
	}}
	stale := map[string]bool{"amb-stale": true}

	clusters, suppressed, errs := Clusters(r, stale, nil)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none", suppressed)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %v, want 1", clusters)
	}
	ev := decodeEvidence(t, clusters[0].Evidence)
	if len(ev) != 1 || ev[0].Path != "cmd/other" {
		t.Fatalf("evidence = %+v, want just the adjudicated-stale ref", ev)
	}
}

func TestClustersUnreadableArtifactDropsGroup(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "missing.md") // never written

	r := Report{Dead: []Ref{{ID: "d1", Artifact: artifact, Path: "old/a.go", Line: 3}}}

	clusters, suppressed, errs := Clusters(r, map[string]bool{}, nil)
	if len(clusters) != 0 {
		t.Fatalf("clusters = %v, want none — the artifact can't be read", clusters)
	}
	if len(suppressed) != 0 {
		t.Fatalf("suppressed = %v, want none", suppressed)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly 1", errs)
	}
}

func TestClustersSchema(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "content\n")

	r := Report{Dead: []Ref{{
		ID: "d1", Artifact: artifact, Path: "old/a.go", Line: 3,
		RuleExcerpt: "see `old/a.go`", ResolvedTo: filepath.Join(dir, "old/a.go"),
	}}}

	clusters, _, errs := Clusters(r, map[string]bool{}, nil)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %d, want 1", len(clusters))
	}

	indexPath := filepath.Join(t.TempDir(), "clusters.json")
	if err := analyst.MergeClusters(clusters, indexPath, SignalType); err != nil {
		t.Fatal(err)
	}

	indexData, err := os.ReadFile(indexPath) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatal(err)
	}
	var index []analyst.ClusterIndexEntry
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 {
		t.Fatalf("index entries = %d, want 1", len(index))
	}
	if index[0].SignalType != SignalType {
		t.Errorf("index SignalType = %q, want %q", index[0].SignalType, SignalType)
	}

	clusterData, err := os.ReadFile(filepath.Join(filepath.Dir(indexPath), index[0].File))
	if err != nil {
		t.Fatalf("read %s: %v", index[0].File, err)
	}
	var c analyst.Cluster
	if err := json.Unmarshal(clusterData, &c); err != nil {
		t.Fatal(err)
	}

	if c.SignalType != SignalType {
		t.Errorf("SignalType = %q, want %q", c.SignalType, SignalType)
	}
	if !strings.HasPrefix(c.ClusterID, SignalType+"::") {
		t.Errorf("ClusterID = %q, want prefix %q", c.ClusterID, SignalType+"::")
	}
	if c.ArtifactContent == nil {
		t.Error("ArtifactContent is nil, want the truncated artifact")
	}
	if string(c.Incidents) != "[]" {
		t.Errorf("Incidents = %s, want []", c.Incidents)
	}

	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(c.Evidence, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 {
		t.Fatalf("evidence entries = %d, want 1", len(raw))
	}
	wantKeys := []string{"path", "line", "rule_excerpt", "resolved_to", "same_name"}
	if len(raw[0]) != len(wantKeys) {
		t.Fatalf("evidence keys = %v, want exactly %v", raw[0], wantKeys)
	}
	for _, k := range wantKeys {
		if _, ok := raw[0][k]; !ok {
			t.Errorf("evidence missing key %q", k)
		}
	}
}

func TestStaleRefClusterPassesCiteCheckUnchanged(t *testing.T) {
	dir := realTempDir(t)
	artifact := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, artifact, "content\n")

	r := Report{Dead: []Ref{{
		ID: "d1", Artifact: artifact, Path: "old/a.go", Line: 3,
		RuleExcerpt: "see `old/a.go`", ResolvedTo: filepath.Join(dir, "old/a.go"),
	}}}

	clusters, _, errs := Clusters(r, map[string]bool{}, nil)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(clusters) != 1 {
		t.Fatalf("clusters = %d, want 1", len(clusters))
	}

	indexPath := filepath.Join(t.TempDir(), "clusters.json")
	if err := analyst.MergeClusters(clusters, indexPath, SignalType); err != nil {
		t.Fatal(err)
	}

	indexData, err := os.ReadFile(indexPath) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatal(err)
	}
	var index []analyst.ClusterIndexEntry
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 {
		t.Fatalf("index entries = %d, want 1", len(index))
	}
	clusterFile := filepath.Join(filepath.Dir(indexPath), index[0].File)

	proposalPath := filepath.Join(t.TempDir(), "p-1.json")
	content := `{"id":"glitch-stale","implicated_artifact":"` + artifact + `",
	  "signal_type":"stale-ref","fix_type":"fix-stale","confidence":"high",
	  "evidence":["` + "`old/a.go` (line 3) → `new/a.go`" + `"],
	  "citations":[],
	  "diagnosis":"d","proposed_change":"c","reason_log":"r"}`
	writeFile(t, proposalPath, content)

	reasonLogDir := filepath.Join(t.TempDir(), "reason-log")
	result, err := analyst.ApplyCiteCheck(proposalPath, clusterFile, reasonLogDir, "2026-09-28")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q, want ok", result.Status)
	}
	after, err := os.ReadFile(proposalPath) //nolint:gosec // path is under the test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != content {
		t.Errorf("proposal file was modified:\nbefore: %s\nafter:  %s", content, after)
	}
	if _, err := os.Stat(reasonLogDir); !os.IsNotExist(err) {
		t.Errorf("reasonLogDir = %v exists, want no reason-log written", err)
	}
}
