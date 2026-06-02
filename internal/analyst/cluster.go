package analyst

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	Incidents        json.RawMessage `json:"incidents"` // JSON array of member incidents
}

// clusterRow is the raw SQL projection before Go reads artifact files.
type clusterRow struct {
	Artifact         string          `json:"artifact"`
	SignalType       string          `json:"signal_type"`
	DistinctSessions int             `json:"distinct_sessions"`
	Incidents        json.RawMessage `json:"incidents"`
}

// clusterSQL explodes each incident across its candidate artifacts, groups by
// (artifact, signal_type), keeps groups with >= minSessions distinct sessions,
// and aggregates the member incidents into a JSON array per cluster.
func clusterSQL(minSessions int) string {
	return fmt.Sprintf(`
WITH exploded AS (
  SELECT incident_id, session_id, ts, confidence, detail, "window", signal_type,
         unnest(CAST(candidates AS VARCHAR[])) AS artifact
  FROM incidents
),
gated AS (
  SELECT artifact, signal_type
  FROM exploded
  GROUP BY artifact, signal_type
  HAVING count(DISTINCT session_id) >= %d
)
SELECT e.artifact,
       e.signal_type,
       count(DISTINCT e.session_id) AS distinct_sessions,
       to_json(list(struct_pack(
         incident_id := e.incident_id, session_id := e.session_id, ts := e.ts,
         confidence := e.confidence, detail := e.detail, "window" := e."window"))) AS incidents
FROM exploded e
JOIN gated g USING (artifact, signal_type)
GROUP BY e.artifact, e.signal_type
ORDER BY distinct_sessions DESC, e.artifact, e.signal_type;`, minSessions)
}

// clusterRows runs the clustering query against db and returns the raw rows.
func clusterRows(ctx context.Context, db string, minSessions int) ([]clusterRow, error) {
	out, err := queryJSON(ctx, db, clusterSQL(minSessions))
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

// ClusterDB runs the clustering query, then reads each implicated artifact's
// current content from disk, returning fully-populated clusters.
func ClusterDB(ctx context.Context, db string, minSessions int) ([]Cluster, error) {
	rows, err := clusterRows(ctx, db, minSessions)
	if err != nil {
		return nil, err
	}
	clusters := make([]Cluster, 0, len(rows))
	for _, r := range rows {
		c := Cluster{
			ClusterID:        r.SignalType + "::" + r.Artifact,
			SignalType:       r.SignalType,
			Artifact:         r.Artifact,
			DistinctSessions: r.DistinctSessions,
			Incidents:        r.Incidents,
		}
		if data, err := os.ReadFile(r.Artifact); err == nil {
			s := string(data)
			c.ArtifactContent = &s
			c.ArtifactExists = true
		}
		clusters = append(clusters, c)
	}
	return clusters, nil
}

// WriteClusters marshals clusters to outPath as indented JSON.
func WriteClusters(clusters []Cluster, outPath string) error {
	data, err := json.MarshalIndent(clusters, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, append(data, '\n'), 0o644)
}
