package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// Size caps keep a single pretty-printed cluster file comfortably under the
// Oracle's Read token budget. The transcript windows are the dominant bloat
// (dozens of incidents x several turns each), so the writer bounds the incident
// count, the turns per window, and each excerpt; the artifact file is capped
// whole. Truncations leave a visible marker so the Oracle knows it is reasoning
// from a sample, not the full text.
const (
	maxArtifactContentBytes = 12 * 1024
	maxWindowExcerptBytes   = 1500
	maxWindowTurns          = 4  // keep the last N turns of each window — the glitch surfaces at the tail
	maxIncidentsPerFile     = 25 // cap incidents written per cluster file; the Oracle reasons from a sample
	truncMarker             = "\n…[truncated by analyst]…"
)

// Cluster is one actionable group: incidents sharing a candidate artifact and a
// signal_type, spanning >= MinSessions distinct sessions.
type Cluster struct {
	ClusterID        string          `json:"cluster_id"`
	SignalType       string          `json:"signal_type"`
	Artifact         string          `json:"artifact"`
	ArtifactContent  *string         `json:"artifact_content"` // nil if the file is missing
	ArtifactExists   bool            `json:"artifact_exists"`
	DistinctSessions int             `json:"distinct_sessions"`
	TotalIncidents   int             `json:"total_incidents"`
	Incidents        json.RawMessage `json:"incidents"` // JSON array of member incidents
}

// clusterRow is the raw SQL projection before Go reads artifact files.
type clusterRow struct {
	Artifact         string          `json:"artifact"`
	SignalType       string          `json:"signal_type"`
	DistinctSessions int             `json:"distinct_sessions"`
	TotalIncidents   int             `json:"total_incidents"`
	Incidents        json.RawMessage `json:"incidents"`
}

// clusterSQL explodes each incident across its candidate artifacts, groups by
// (artifact, signal_type), keeps groups with >= minSessions distinct sessions,
// and aggregates the member incidents into a JSON array per cluster.
// Incidents are sampled using session-stratified round-robin up to maxIncidents;
// maxIncidents <= 0 means uncapped.
func clusterSQL(minSessions, maxIncidents int) string {
	capN := maxIncidents
	if capN <= 0 {
		capN = math.MaxInt32 // uncapped
	}
	return fmt.Sprintf(`
WITH exploded AS (
  SELECT incident_id, session_id, ts, confidence, detail, "window", signal_type,
         -- canonicalize: a candidate under a git worktree (<repo>/.worktrees/<name>/…,
         -- worktrunk's layout) maps back to the repo-root artifact, so worktree copies
         -- cluster with the canonical file instead of fragmenting.
         regexp_replace(unnest(CAST(candidates AS VARCHAR[])), '/\.worktrees/[^/]+/', '/') AS artifact
  FROM incidents
),
gated AS (
  SELECT artifact, signal_type,
         count(DISTINCT session_id) AS distinct_sessions,
         count(DISTINCT incident_id) AS total_incidents
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

// clusterRows runs the clustering query against db and returns the raw rows.
func clusterRows(ctx context.Context, db string, minSessions, maxIncidents int) ([]clusterRow, error) {
	out, err := queryJSON(ctx, db, clusterSQL(minSessions, maxIncidents))
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	var rows []clusterRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("decode cluster rows: %w\noutput: %s", err, out)
	}
	return rows, nil
}

// ClusterDB runs the clustering query (artifacts already canonicalized to repo
// roots), then reads each artifact's current content from disk. Clusters whose
// canonical artifact no longer exists — a deleted worktree, a removed file — are
// dropped; dropped is how many were dropped, for the caller to surface.
func ClusterDB(ctx context.Context, db string, minSessions, maxIncidents int) (clusters []Cluster, dropped int, err error) {
	rows, err := clusterRows(ctx, db, minSessions, maxIncidents)
	if err != nil {
		return nil, 0, err
	}
	clusters = make([]Cluster, 0, len(rows))
	for _, r := range rows {
		data, err := os.ReadFile(r.Artifact)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				dropped++
				continue
			}
			return nil, 0, fmt.Errorf("read artifact %s: %w", r.Artifact, err)
		}
		s := truncate(string(data), maxArtifactContentBytes)
		clusters = append(clusters, Cluster{
			ClusterID:        r.SignalType + "::" + r.Artifact,
			SignalType:       r.SignalType,
			Artifact:         r.Artifact,
			ArtifactContent:  &s,
			ArtifactExists:   true,
			DistinctSessions: r.DistinctSessions,
			TotalIncidents:   r.TotalIncidents,
			Incidents:        capWindows(r.Incidents),
		})
	}
	return clusters, dropped, nil
}

// TopClusters ranks clusters by signal strength — distinct_sessions, then
// total_incidents, with cluster_id as a deterministic tiebreak — and keeps the
// top n. n <= 0 keeps all. It returns the kept clusters and the dropped count
// so callers can surface the truncation rather than letting it stay silent.
func TopClusters(clusters []Cluster, n int) (kept []Cluster, dropped int) {
	if n <= 0 || len(clusters) <= n {
		return clusters, 0
	}
	ranked := make([]Cluster, len(clusters))
	copy(ranked, clusters)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.DistinctSessions != b.DistinctSessions {
			return a.DistinctSessions > b.DistinctSessions
		}
		if a.TotalIncidents != b.TotalIncidents {
			return a.TotalIncidents > b.TotalIncidents
		}
		return a.ClusterID < b.ClusterID
	})
	return ranked[:n], len(ranked) - n
}

// truncate returns s unchanged if it fits within max bytes, otherwise the first
// max bytes plus a visible marker. The cut is byte-aligned; an excerpt is plain
// text, so a split rune at the boundary is acceptable.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + truncMarker
}

// capWindows trims the evidence so a single cluster file stays within the
// Oracle's Read budget: it keeps the strongest incidents, the last turns of each
// window, and bounds every excerpt. Non-window fields pass through untouched. On
// any decode failure it returns the incidents unchanged — capping is best-effort.
func capWindows(raw json.RawMessage) json.RawMessage {
	var incidents []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &incidents); err != nil {
		return raw
	}
	if len(incidents) > maxIncidentsPerFile {
		// Incidents arrive sorted best-first (session-stratified), so the head is
		// the strongest representative sample.
		incidents = incidents[:maxIncidentsPerFile]
	}
	for _, inc := range incidents {
		var window []map[string]json.RawMessage
		if err := json.Unmarshal(inc["window"], &window); err != nil {
			continue
		}
		if len(window) > maxWindowTurns {
			window = window[len(window)-maxWindowTurns:]
		}
		for _, turn := range window {
			var excerpt string
			if err := json.Unmarshal(turn["excerpt"], &excerpt); err != nil {
				continue
			}
			capped, _ := json.Marshal(truncate(excerpt, maxWindowExcerptBytes))
			turn["excerpt"] = capped
		}
		w, _ := json.Marshal(window)
		inc["window"] = w
	}
	out, _ := json.Marshal(incidents)
	return out
}

// ClusterIndexEntry is one row of the index: enough for the orchestrator to
// decide what to dispatch and where each cluster's full JSON lives.
type ClusterIndexEntry struct {
	ClusterID        string `json:"cluster_id"`
	SignalType       string `json:"signal_type"`
	Artifact         string `json:"artifact"`
	ArtifactExists   bool   `json:"artifact_exists"`
	DistinctSessions int    `json:"distinct_sessions"`
	TotalIncidents   int    `json:"total_incidents"`
	SampledIncidents int    `json:"sampled_incidents"`
	File             string `json:"file"` // path to the per-cluster JSON, relative to the index
}

// WriteClusters writes one pretty-printed file per cluster under <dir>/clusters/
// and an index array at <dir>/clusters.json. The Oracle reads only its own
// cluster file, so a single giant minified file can no longer blow the Read cap.
// indexPath is the index file; per-cluster files live in a sibling clusters/ dir.
func WriteClusters(clusters []Cluster, indexPath string) error {
	dir := filepath.Dir(indexPath)
	clustersDir := filepath.Join(dir, "clusters")
	if err := os.MkdirAll(clustersDir, 0o755); err != nil {
		return err
	}

	index := make([]ClusterIndexEntry, 0, len(clusters))
	for _, c := range clusters {
		name := slugify(c.ClusterID)
		if name == "" {
			name = fmt.Sprintf("%08x", fnv32a(c.ClusterID))
		}
		name = fmt.Sprintf("%s-%08x.json", name, fnv32a(c.ClusterID))
		file := filepath.Join(clustersDir, name)

		data, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(file, append(data, '\n'), 0o644); err != nil {
			return err
		}

		rel, err := filepath.Rel(dir, file)
		if err != nil {
			rel = file
		}
		index = append(index, ClusterIndexEntry{
			ClusterID:        c.ClusterID,
			SignalType:       c.SignalType,
			Artifact:         c.Artifact,
			ArtifactExists:   c.ArtifactExists,
			DistinctSessions: c.DistinctSessions,
			TotalIncidents:   c.TotalIncidents,
			SampledIncidents: countIncidents(c.Incidents),
			File:             rel,
		})
	}

	idx, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath, append(idx, '\n'), 0o644)
}

// countIncidents returns the number of sampled incidents in a cluster's
// incidents array, or 0 if it cannot be decoded.
func countIncidents(raw json.RawMessage) int {
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return 0
	}
	return len(arr)
}
