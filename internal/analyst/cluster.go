package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	LastSeen         string          `json:"last_seen"`
	RecentSessions   int             `json:"recent_sessions"`
	LikelyResolved   bool            `json:"likely_resolved"`    // no in-window activity: the behavior stopped occurring
	RedirectFrom     string          `json:"redirect_from"`      // pointer artifact this cluster was re-attributed away from
	UnresolvedImport string          `json:"unresolved_import"`  // pointer artifact's @import that could not be resolved
	Incidents        json.RawMessage `json:"incidents"`          // JSON array of member incidents
	Evidence         json.RawMessage `json:"evidence,omitempty"` // per-reference list for a stale-ref cluster
}

// clusterRow is the raw SQL projection before Go reads artifact files.
type clusterRow struct {
	Artifact         string          `json:"artifact"`
	SignalType       string          `json:"signal_type"`
	DistinctSessions int             `json:"distinct_sessions"`
	TotalIncidents   int             `json:"total_incidents"`
	LastSeen         string          `json:"last_seen"`
	RecentSessions   int             `json:"recent_sessions"`
	Incidents        json.RawMessage `json:"incidents"`
}

// clusterSQL explodes each incident across its candidate artifacts, groups by
// (artifact, signal_type), keeps groups with >= minSessions distinct sessions, and
// aggregates the member incidents into a JSON array per cluster. It also computes
// last_seen and recent_sessions: distinct sessions whose incident date falls within
// the most recent staleDays *active* corpus days (dates with >=1 incident). All
// recency comparisons are string ops on ISO-8601 ts (sortable as text).
// Incidents are sampled session-stratified up to maxIncidents; maxIncidents <= 0 = uncapped.
// Within a session the sample is newest-first, so the Oracle diagnoses the behavior
// the cluster's recency ranking is actually claiming is live.
func clusterSQL(minSessions, maxIncidents, staleDays int) string {
	capN := maxIncidents
	if capN <= 0 {
		capN = math.MaxInt32 // uncapped
	}
	return fmt.Sprintf(`
WITH active_days AS (
  SELECT DISTINCT substr(ts, 1, 10) AS d FROM incidents
),
cutoff AS (
  -- empty window (staleDays=0) or empty corpus → a sentinel above any real date, so nothing counts as recent
  SELECT coalesce(min(d), '9999-12-31') AS live_cutoff FROM (SELECT d FROM active_days ORDER BY d DESC LIMIT %d)
),
exploded AS (
  SELECT incident_id, session_id, ts, confidence, detail, "window", signal_type,
         -- canonicalize worktree copies to the main repo root: in-repo
         -- (<repo>/.worktrees/<name>/) then sibling (<repo>-worktrees/<name>/) layout.
         %s AS artifact
  FROM incidents
),
gated AS (
  SELECT e.artifact, e.signal_type,
         count(DISTINCT e.session_id) AS distinct_sessions,
         count(DISTINCT e.incident_id) AS total_incidents,
         max(e.ts) AS last_seen,
         count(DISTINCT e.session_id) FILTER (WHERE substr(e.ts, 1, 10) >= c.live_cutoff) AS recent_sessions
  FROM exploded e CROSS JOIN cutoff c
  GROUP BY e.artifact, e.signal_type
  HAVING count(DISTINCT e.session_id) >= %d
),
ranked AS (
  SELECT e.*,
         row_number() OVER (
           PARTITION BY e.artifact, e.signal_type, e.session_id
           ORDER BY (CASE e.confidence WHEN 'high' THEN 3 WHEN 'medium' THEN 2 ELSE 1 END) DESC,
                    e.ts DESC, e.incident_id
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
                    ts DESC, incident_id
         ) AS pick
  FROM ranked
)
SELECT s.artifact,
       s.signal_type,
       g.distinct_sessions,
       g.total_incidents,
       g.last_seen,
       g.recent_sessions,
       to_json(list(struct_pack(
         incident_id := s.incident_id, session_id := s.session_id, ts := s.ts,
         confidence := s.confidence, detail := s.detail, "window" := s."window")
         ORDER BY s.pick)) AS incidents
FROM sampled s
JOIN gated g USING (artifact, signal_type)
WHERE s.pick <= %d
GROUP BY s.artifact, s.signal_type, g.distinct_sessions, g.total_incidents, g.last_seen, g.recent_sessions
ORDER BY g.recent_sessions DESC, g.distinct_sessions DESC, s.artifact, s.signal_type;`,
		staleDays, canonicalArtifactExpr("unnest(CAST(candidates AS VARCHAR[]))"), minSessions, capN)
}

// canonicalArtifactExpr collapses worktree copies in col to the main repo root:
// in-repo (<repo>/.worktrees/<name>/) then sibling (<repo>-worktrees/<name>/)
// layout. Shared by clusterSQL and Artifacts so the clustering query and the
// freshness audit set agree on what "the same artifact" means.
func canonicalArtifactExpr(col string) string {
	return fmt.Sprintf(`regexp_replace(regexp_replace(%s, '/\.worktrees/[^/]+/', '/'), '([^/]+)-worktrees/[^/]+/', '\1/')`, col)
}

// Artifacts returns the distinct canonical candidate artifacts across every
// incident in db — the freshness audit set.
func Artifacts(ctx context.Context, db string) ([]string, error) {
	sql := fmt.Sprintf(`SELECT DISTINCT artifact FROM (
  SELECT %s AS artifact FROM incidents
) ORDER BY artifact`, canonicalArtifactExpr("unnest(CAST(candidates AS VARCHAR[]))"))
	out, err := queryJSON(ctx, db, sql)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, nil
	}
	var rows []struct {
		Artifact string `json:"artifact"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, fmt.Errorf("decode artifact rows: %w\noutput: %s", err, out)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	artifacts := make([]string, len(rows))
	for i, r := range rows {
		artifacts[i] = r.Artifact
	}
	return artifacts, nil
}

// clusterRows runs the clustering query against db and returns the raw rows.
func clusterRows(ctx context.Context, db string, minSessions, maxIncidents, staleDays int) ([]clusterRow, error) {
	out, err := queryJSON(ctx, db, clusterSQL(minSessions, maxIncidents, staleDays))
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
// A pointer artifact — one whose whole content is a redirect like "See @AGENTS.md" —
// is re-attributed to its import target, since the pointer itself can never be
// usefully edited; an unresolvable target is marked instead.
func ClusterDB(ctx context.Context, db string, minSessions, maxIncidents, staleDays int) (clusters []Cluster, dropped int, err error) {
	rows, err := clusterRows(ctx, db, minSessions, maxIncidents, staleDays)
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
		artifact, content := r.Artifact, string(data)
		var redirectFrom, unresolvedImport string
		if imp, ok := redirectImport(content); ok {
			target := resolveImport(r.Artifact, imp)
			if tdata, terr := os.ReadFile(target); terr == nil {
				redirectFrom, artifact, content = r.Artifact, target, string(tdata)
			} else {
				unresolvedImport = imp
			}
		}
		s := truncate(content, maxArtifactContentBytes)
		clusters = append(clusters, Cluster{
			ClusterID:        r.SignalType + "::" + artifact,
			SignalType:       r.SignalType,
			Artifact:         artifact,
			ArtifactContent:  &s,
			ArtifactExists:   true,
			DistinctSessions: r.DistinctSessions,
			TotalIncidents:   r.TotalIncidents,
			LastSeen:         r.LastSeen,
			RecentSessions:   r.RecentSessions,
			LikelyResolved:   r.RecentSessions == 0,
			RedirectFrom:     redirectFrom,
			UnresolvedImport: unresolvedImport,
			Incidents:        capWindows(r.Incidents),
		})
	}
	return clusters, dropped, nil
}

// RankClusters selects the diagnosis fleet. By default the fleet is the top n
// clusters with in-window activity (recent_sessions > 0), ranked by recent
// intensity, then lifetime breadth, then recency. Backlog clusters (no in-window
// activity) are excluded from the default fleet and reported via droppedBacklog —
// never deleted; pass includeStale to rank every cluster by lifetime breadth (the
// historical-backlog mode). Clusters still pointing at an unresolvable pointer
// artifact are excluded from both modes and reported via droppedUnresolved: there
// is no file the Oracle could act on. n <= 0 keeps all selected clusters.
// droppedTop is how many in-fleet candidates the cap dropped.
func RankClusters(clusters []Cluster, n int, includeStale bool) (fleet []Cluster, droppedBacklog, droppedUnresolved, droppedTop int) {
	ranked := make([]Cluster, 0, len(clusters))
	for _, c := range clusters {
		if c.UnresolvedImport != "" {
			droppedUnresolved++
			continue
		}
		ranked = append(ranked, c)
	}
	if includeStale {
		sort.SliceStable(ranked, func(i, j int) bool { return lessBacklog(ranked[i], ranked[j]) })
		fleet, droppedTop = cut(ranked, n)
		return fleet, 0, droppedUnresolved, droppedTop
	}
	var live, backlog []Cluster
	for _, c := range ranked {
		if c.RecentSessions > 0 {
			live = append(live, c)
		} else {
			backlog = append(backlog, c)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return lessLive(live[i], live[j]) })
	fleet, droppedTop = cut(live, n)
	return fleet, len(backlog), droppedUnresolved, droppedTop
}

func cut(ranked []Cluster, n int) (kept []Cluster, dropped int) {
	if n <= 0 || len(ranked) <= n {
		return ranked, 0
	}
	return ranked[:n], len(ranked) - n
}

// lessLive ranks by recent intensity first; lessBacklog by lifetime breadth. Both
// fall back to last_seen (ISO ts, descending) then cluster_id for determinism.
func lessLive(a, b Cluster) bool {
	if a.RecentSessions != b.RecentSessions {
		return a.RecentSessions > b.RecentSessions
	}
	return lessBacklog(a, b)
}

func lessBacklog(a, b Cluster) bool {
	if a.DistinctSessions != b.DistinctSessions {
		return a.DistinctSessions > b.DistinctSessions
	}
	if a.LastSeen != b.LastSeen {
		return a.LastSeen > b.LastSeen
	}
	return a.ClusterID < b.ClusterID
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

// TruncateArtifact caps s to the same content budget ClusterDB applies to a
// cluster's own artifact content.
func TruncateArtifact(s string) string {
	return truncate(s, maxArtifactContentBytes)
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
	LastSeen         string `json:"last_seen"`
	RecentSessions   int    `json:"recent_sessions"`
	LikelyResolved   bool   `json:"likely_resolved"`
	RedirectFrom     string `json:"redirect_from"`
	UnresolvedImport string `json:"unresolved_import"`
	SampledIncidents int    `json:"sampled_incidents"`
	File             string `json:"file"` // path to the per-cluster JSON, relative to the index
}

// clusterFileName returns the per-cluster JSON filename for a cluster id: a
// slugified prefix (or, if that's empty, the fnv hash alone) plus the hash as a
// collision-proofing suffix.
func clusterFileName(id string) string {
	name := slugify(id)
	if name == "" {
		name = fmt.Sprintf("%08x", fnv32a(id))
	}
	return fmt.Sprintf("%s-%08x.json", name, fnv32a(id))
}

// indexEntry projects a Cluster into its index row, rel being the per-cluster
// file's path relative to the index.
func indexEntry(c Cluster, rel string) ClusterIndexEntry {
	return ClusterIndexEntry{
		ClusterID:        c.ClusterID,
		SignalType:       c.SignalType,
		Artifact:         c.Artifact,
		ArtifactExists:   c.ArtifactExists,
		DistinctSessions: c.DistinctSessions,
		TotalIncidents:   c.TotalIncidents,
		LastSeen:         c.LastSeen,
		RecentSessions:   c.RecentSessions,
		LikelyResolved:   c.LikelyResolved,
		RedirectFrom:     c.RedirectFrom,
		UnresolvedImport: c.UnresolvedImport,
		SampledIncidents: countIncidents(c.Incidents),
		File:             rel,
	}
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

	written := make(map[string]bool, len(clusters))
	index := make([]ClusterIndexEntry, 0, len(clusters))
	for _, c := range clusters {
		name := clusterFileName(c.ClusterID)
		file := filepath.Join(clustersDir, name)

		data, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(file, append(data, '\n'), 0o644); err != nil {
			return err
		}
		written[name] = true

		rel, err := filepath.Rel(dir, file)
		if err != nil {
			rel = file
		}
		index = append(index, indexEntry(c, rel))
	}

	if err := pruneStaleClusters(clustersDir, written); err != nil {
		return err
	}

	idx, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath, append(idx, '\n'), 0o644)
}

// MergeClusters replaces one signal type's clusters in an existing index without
// the pruning WriteClusters does, so a second producer (the freshness audit) can
// add its clusters beside Track A's. Entries for other signal types, and their
// files, are left untouched.
func MergeClusters(clusters []Cluster, indexPath, signalType string) error {
	dir := filepath.Dir(indexPath)
	clustersDir := filepath.Join(dir, "clusters")

	var index []ClusterIndexEntry
	switch data, err := os.ReadFile(indexPath); {
	case errors.Is(err, os.ErrNotExist):
		// no index yet
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(data, &index); err != nil {
			return err
		}
	}

	kept := make([]ClusterIndexEntry, 0, len(index))
	for _, e := range index {
		if e.SignalType != signalType {
			kept = append(kept, e)
			continue
		}
		file := filepath.Join(dir, e.File)
		if filepath.Dir(file) == clustersDir {
			if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	index = kept

	if err := os.MkdirAll(clustersDir, 0o755); err != nil {
		return err
	}
	for _, c := range clusters {
		file := filepath.Join(clustersDir, clusterFileName(c.ClusterID))

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
		index = append(index, indexEntry(c, rel))
	}

	idx, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(indexPath, append(idx, '\n'), 0o644)
}

// pruneStaleClusters deletes per-cluster files this run did not write. A cluster
// that has gone quiet, been suppressed, or fallen below --top otherwise leaves
// its file behind, and anything that globs clusters/ instead of reading the
// index feeds a stale cluster — with stale evidence — straight to the Oracle.
func pruneStaleClusters(clustersDir string, written map[string]bool) error {
	entries, err := os.ReadDir(clustersDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || written[name] || !strings.HasSuffix(name, ".json") {
			continue
		}
		if err := os.Remove(filepath.Join(clustersDir, name)); err != nil {
			return fmt.Errorf("prune stale cluster %s: %w", name, err)
		}
	}
	return nil
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

// redirectLineRe matches a whole line that is nothing but an @import, optionally
// introduced by a pointer verb: "See @AGENTS.md", "@~/.claude/AGENTS.md".
var redirectLineRe = regexp.MustCompile(`(?i)^(?:see|read|follow|use|refer to)?\s*@(\S+?)[.,;:]*$`)

// redirectImport returns the import path an artifact redirects to when the file
// has no substantive content of its own — headings, blank lines and HTML comments
// aside, exactly one line, and that line is the import. Anything else (a second
// import, prose alongside the import) means the file is worth editing in place.
func redirectImport(content string) (string, bool) {
	target := ""
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "<!--") {
			continue
		}
		m := redirectLineRe.FindStringSubmatch(t)
		if m == nil || target != "" {
			return "", false
		}
		target = m[1]
	}
	return target, target != ""
}

// resolveImport resolves an @import against the importing file's directory,
// expanding a leading ~. An unexpandable ~ yields "" so the caller treats the
// import as unresolved rather than probing a bogus relative path.
func resolveImport(importer, imp string) string {
	if strings.HasPrefix(imp, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, imp[2:])
	}
	if filepath.IsAbs(imp) {
		return imp
	}
	return filepath.Join(filepath.Dir(importer), imp)
}

// Worktree-path canonicalization mirrored from clusterSQL's regexes — KEEP IN SYNC.
// A repo root under either an in-repo (<repo>/.worktrees/<name>/) or sibling
// (<repo>-worktrees/<name>/, worktrunk's default) worktree maps to the main root.
var (
	inRepoWorktreeRe  = regexp.MustCompile(`/\.worktrees/[^/]+/`)
	siblingWorktreeRe = regexp.MustCompile(`([^/]+)-worktrees/[^/]+/`)
)

// CanonicalizeRepoPrefix turns a repo root (possibly a worktree root) into the
// canonical main-repo prefix that clusterSQL stores artifacts under, with a
// trailing slash so it can't match a sibling repo ("/x/repo" vs "/x/repo-tools").
func CanonicalizeRepoPrefix(repoRoot string) string {
	p := repoRoot
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	// ReplaceAllString replaces every match while DuckDB's regexp_replace replaces only
	// the first; for realistic paths (git disallows worktree-in-worktree) this is equivalent.
	p = inRepoWorktreeRe.ReplaceAllString(p, "/")
	p = siblingWorktreeRe.ReplaceAllString(p, "$1/")
	return p
}

// FilterByPrefix keeps clusters whose canonical artifact lives under repoRoot.
// repoRoot == "" is a no-op (the wide default).
func FilterByPrefix(clusters []Cluster, repoRoot string) []Cluster {
	if repoRoot == "" {
		return clusters
	}
	prefix := CanonicalizeRepoPrefix(repoRoot)
	out := make([]Cluster, 0, len(clusters))
	for _, c := range clusters {
		if strings.HasPrefix(c.Artifact, prefix) {
			out = append(out, c)
		}
	}
	return out
}
