package analyst

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClusterSamplesRecentIncidentsFirst(t *testing.T) {
	// Clusters are ranked newest-first, so the evidence handed to the Oracle must
	// be newest-first too. 3 sessions x 4 incidents of equal confidence spread over
	// 2026-04-12..2026-06-20; the corpus frontier is 06-20 and staleDays=2 puts
	// live_cutoff at 06-18. With a cap of 6 (2 picks per session) every sampled ts
	// must sit inside that window — oldest-first sampling returns April/May instead.
	ins := `INSERT INTO incidents
	SELECT md5('i' || s || '_' || d), 's' || s, '/p', d || 'T10:00:00Z',
	       'inefficiency', '/g/CLAUDE.md',
	       '["/g/CLAUDE.md"]'::JSON, '[]'::JSON, 'high', '{}'::JSON
	FROM range(1,4) AS t1(s),
	     (SELECT unnest(['2026-04-12','2026-05-01','2026-06-18','2026-06-20']) AS d) AS t2;`
	db := makeIncidentsDB(t, ins)

	const liveCutoff = "2026-06-18"
	rows, err := clusterRows(context.Background(), db, 3, 6, 2)
	if err != nil {
		t.Fatalf("clusterRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 cluster, got %d", len(rows))
	}
	r := rows[0]
	if r.RecentSessions != 3 {
		t.Fatalf("recent_sessions = %d, want 3 — the fixture must be live for this assertion to bite", r.RecentSessions)
	}
	var members []struct {
		SessionID string `json:"session_id"`
		TS        string `json:"ts"`
	}
	if err := json.Unmarshal(r.Incidents, &members); err != nil {
		t.Fatalf("unmarshal incidents: %v", err)
	}
	if len(members) != 6 {
		t.Fatalf("sampled %d incidents, want 6 (the cap)", len(members))
	}
	for _, m := range members {
		if m.TS < liveCutoff {
			t.Errorf("sampled incident from %s at %s predates the recency window (%s): the Oracle sees stale evidence",
				m.SessionID, m.TS, liveCutoff)
		}
	}
	perSession := map[string]int{}
	for _, m := range members {
		perSession[m.SessionID]++
	}
	if len(perSession) != 3 {
		t.Errorf("covered %d sessions, want 3 — recency must not break session stratification", len(perSession))
	}
}
