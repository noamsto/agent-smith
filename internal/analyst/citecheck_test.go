package analyst

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureIncidents backs most TestCheckCitations cases: two "real" sessions
// (the #83 shape) plus a handful of purpose-built sessions for edge cases.
const fixtureIncidents = `[
  {
    "session_id": "362498af-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    "detail": {"tool": "Bash"},
    "window": [
      {"turn": 228, "type": "user", "excerpt": "please run deslop"},
      {"turn": 229, "type": "assistant", "excerpt": "{\"type\":\"tool_use\",\"name\":\"Skill\",\"input\":{\"skill\":\"deslop\"}}"},
      {"turn": 230, "type": "user", "excerpt": "ok"},
      {"turn": 231, "type": "attachment", "excerpt": ""}
    ]
  },
  {
    "session_id": "97b5c094-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
    "detail": {"tool": "Bash"},
    "window": [
      {"turn": 395, "type": "user", "excerpt": "push please"},
      {"turn": 396, "type": "assistant", "excerpt": "running"},
      {"turn": 397, "type": "user", "excerpt": "ALLOW_PUSH_WITHOUT_DESLOP=1 git push"},
      {"turn": 398, "type": "attachment", "excerpt": ""}
    ]
  },
  {
    "session_id": "abc12345-cccc-cccc-cccc-cccccccccccc",
    "detail": {},
    "window": [
      {"turn": 10, "type": "user", "excerpt": "a"},
      {"turn": 12, "type": "user", "excerpt": "b"}
    ]
  },
  {
    "session_id": "36249800-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    "detail": {},
    "window": [{"turn": 1, "type": "user", "excerpt": "x"}]
  },
  {
    "session_id": "36249800-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
    "detail": {},
    "window": [{"turn": 1, "type": "user", "excerpt": "y"}]
  },
  {
    "session_id": "abc99999-dddd-dddd-dddd-dddddddddddd",
    "detail": {"error": "boom: no such file"},
    "window": [{"turn": 500, "type": "tool_result", "excerpt": ""}]
  },
  {
    "session_id": "abc88888-eeee-eeee-eeee-eeeeeeeeeeee",
    "detail": {"input": "foo & bar"},
    "window": [{"turn": 600, "type": "tool_result", "excerpt": ""}]
  },
  {
    "session_id": "abc77777-ffff-ffff-ffff-ffffffffffff",
    "detail": {},
    "window": [{"turn": 700, "type": "assistant", "excerpt": "line one line two"}]
  }
]`

func fixtureCluster() Cluster {
	return Cluster{SignalType: "tool_error", Incidents: json.RawMessage(fixtureIncidents)}
}

func strEntry(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestParseRefTrailingComma(t *testing.T) {
	r, ok := parseRef("abc12345:229,")
	if !ok {
		t.Fatal("expected trailing comma to parse")
	}
	if r.token != "abc12345" || r.a != 229 || r.b != 229 || r.raw != "abc12345:229" {
		t.Errorf("parsed = %+v", r)
	}
}

func TestParseRefRejectsFileLine(t *testing.T) {
	if _, ok := parseRef("CLAUDE.md:42"); ok {
		t.Error("CLAUDE.md:42 must not parse as a ref")
	}
	if _, ok := parseRef("abcd:3"); ok {
		t.Error("a 4-char session token must not parse as a ref (min 8)")
	}
}

func TestCheckCitations(t *testing.T) {
	cluster := fixtureCluster()

	tests := []struct {
		name       string
		prop       citeProposal
		cluster    Cluster
		wantStatus string
		want       []string // substrings expected in Failures (joined) or Notes
	}{
		{
			name: "valid quoted citations",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("362498af:228-231"), strEntry("97b5c094:395-398")},
				Citations: []Citation{
					{Window: "362498af:229", Quote: `"skill":"deslop"`},
					{Window: "97b5c094:397", Quote: "ALLOW_PUSH_WITHOUT_DESLOP=1 git push"},
				},
			},
			cluster:    cluster,
			wantStatus: "ok",
		},
		{
			name:       "absent window rejects",
			prop:       citeProposal{Confidence: "high", Evidence: []json.RawMessage{strEntry("deadbeef:1-2")}},
			cluster:    cluster,
			wantStatus: "rejected",
			want:       []string{"deadbeef:1-2", "no session"},
		},
		{
			name:       "turns past window rejects",
			prop:       citeProposal{Confidence: "high", Evidence: []json.RawMessage{strEntry("362498af:228-240")}},
			cluster:    cluster,
			wantStatus: "rejected",
			want:       []string{"362498af:228-240", "not in window"},
		},
		{
			name: "83 shape: quote absent from window rejects",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("362498af:228-231")},
				Citations:  []Citation{{Window: "362498af:228-231", Quote: "claude-deslop-guard: push blocked"}},
			},
			cluster:    cluster,
			wantStatus: "rejected",
			want:       []string{"362498af:228-231", "quote not found"},
		},
		{
			name: "quote with literal quote characters matches raw excerpt",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("362498af:229")},
				Citations:  []Citation{{Window: "362498af:229", Quote: `"name":"Skill"`}},
			},
			cluster:    cluster,
			wantStatus: "ok",
		},
		{
			name: "unquoted citation at high demotes",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("362498af:229")},
				Citations:  []Citation{{Window: "362498af:229"}},
			},
			cluster:    cluster,
			wantStatus: "demoted",
			want:       []string{"362498af:229", "no verified quote"},
		},
		{
			name: "short quote counts as unquoted, not a failure",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("362498af:229")},
				Citations:  []Citation{{Window: "362498af:229", Quote: "hi"}},
			},
			cluster:    cluster,
			wantStatus: "demoted",
			want:       []string{"no verified quote"},
		},
		{
			name:       "no citations at medium is unchanged",
			prop:       citeProposal{Confidence: "medium", Evidence: []json.RawMessage{strEntry("362498af:229")}},
			cluster:    cluster,
			wantStatus: "ok",
		},
		{
			name: "stale-ref cluster with object evidence is skipped",
			prop: citeProposal{
				Confidence: "high", FixType: "fix-stale",
				Evidence: []json.RawMessage{json.RawMessage(`{"path":"x","line":1}`)},
			},
			cluster:    Cluster{SignalType: "stale-ref", Incidents: json.RawMessage(fixtureIncidents)},
			wantStatus: "ok",
		},
		{
			name:       "fix_type skip is untouched",
			prop:       citeProposal{Confidence: "high", FixType: "skip", Evidence: []json.RawMessage{strEntry("deadbeef:1")}},
			cluster:    cluster,
			wantStatus: "ok",
		},
		{
			name:       "ambiguous prefix rejects",
			prop:       citeProposal{Confidence: "high", Evidence: []json.RawMessage{strEntry("36249800:1")}},
			cluster:    cluster,
			wantStatus: "rejected",
			want:       []string{"36249800:1", "ambiguous"},
		},
		{
			name:       "annotated ref to an absent window rejects",
			prop:       citeProposal{Confidence: "high", Evidence: []json.RawMessage{strEntry("deadbeef:5 (some note)")}},
			cluster:    cluster,
			wantStatus: "rejected",
			want:       []string{"deadbeef:5", "no session"},
		},
		{
			name: "annotated ref with a verified quote is ok",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("362498af:229 (skill call)")},
				Citations:  []Citation{{Window: "362498af:229", Quote: `"skill":"deslop"`}},
			},
			cluster:    cluster,
			wantStatus: "ok",
		},
		{
			name:       "gapped window cited across the gap rejects",
			prop:       citeProposal{Confidence: "high", Evidence: []json.RawMessage{strEntry("abc12345:10-12")}},
			cluster:    cluster,
			wantStatus: "rejected",
			want:       []string{"abc12345:10-12", "turn 11 not in window"},
		},
		{
			name: "file:line and short prefix are not citations; demotes with no other citations",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("CLAUDE.md:42"), strEntry("abcd:3")},
			},
			cluster:    cluster,
			wantStatus: "demoted",
			want:       []string{"no citations"},
		},
		{
			name: "quote present only in detail.error is ok",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("abc99999:500")},
				Citations:  []Citation{{Window: "abc99999:500", Quote: "boom: no such file"}},
			},
			cluster:    cluster,
			wantStatus: "ok",
		},
		{
			name: "quote with escaped \\u0026 matches literal & in detail",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("abc88888:600")},
				Citations:  []Citation{{Window: "abc88888:600", Quote: "foo \\u0026 bar"}},
			},
			cluster:    cluster,
			wantStatus: "ok",
		},
		{
			name: "quote with escaped \\n matches space-collapsed excerpt",
			prop: citeProposal{
				Confidence: "high",
				Evidence:   []json.RawMessage{strEntry("abc77777:700")},
				Citations:  []Citation{{Window: "abc77777:700", Quote: "line one\\nline two"}},
			},
			cluster:    cluster,
			wantStatus: "ok",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := checkCitations(tc.prop, tc.cluster)
			if got.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (failures=%v notes=%q)", got.Status, tc.wantStatus, got.Failures, got.Notes)
			}
			haystack := strings.Join(got.Failures, "; ") + got.Notes
			if !containsAll(haystack, tc.want...) {
				t.Errorf("result %+v missing expected substrings %v", got, tc.want)
			}
		})
	}
}

func writeCiteFixtures(t *testing.T, dir string) (proposalPath, clusterPath string) {
	t.Helper()
	clusterPath = filepath.Join(dir, "c.json")
	writeJSON(t, clusterPath, `{"signal_type":"tool_error","incidents":`+fixtureIncidents+`}`)
	proposalPath = filepath.Join(dir, "p-1.json")
	writeJSON(t, proposalPath, `{"id":"glitch-83","implicated_artifact":"/g/CLAUDE.md#citations",
	  "signal_type":"tool_error","fix_type":"strengthen",
	  "evidence":["362498af:228-231"],
	  "citations":[{"window":"362498af:228-231","quote":"claude-deslop-guard: push blocked"}],
	  "diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"expected fewer glitches"}`)
	return proposalPath, clusterPath
}

func TestApplyCiteCheckRejects(t *testing.T) {
	dir := t.TempDir()
	proposalPath, clusterPath := writeCiteFixtures(t, dir)
	rlDir := filepath.Join(dir, "reason-log")

	result, err := ApplyCiteCheck(proposalPath, clusterPath, rlDir, "2026-09-28")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "rejected" {
		t.Fatalf("status = %q", result.Status)
	}
	if _, err := os.Stat(proposalPath); !os.IsNotExist(err) {
		t.Errorf("expected %s to be gone, stat err = %v", proposalPath, err)
	}
	rejectedPath := proposalPath + ".cite-rejected"
	if _, err := os.Stat(rejectedPath); err != nil {
		t.Fatalf("expected %s to exist: %v", rejectedPath, err)
	}

	entries, err := os.ReadDir(rlDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("reason-log dir = %v, err = %v", entries, err)
	}
	body, err := os.ReadFile(filepath.Join(rlDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !containsAll(content, "citation check failed", "362498af:228-231", "<!-- outcome: uncited -->") {
		t.Errorf("reason-log entry:\n%s", content)
	}

	if props, _ := LoadProposals(dir); len(props) != 0 {
		t.Errorf("LoadProposals should see 0 proposals after reject, got %d", len(props))
	}

	// A second reject run on the same date must not error and must keep the
	// existing entry (O_EXCL).
	proposalPath2, _ := writeCiteFixtures(t, dir)
	if _, err := ApplyCiteCheck(proposalPath2, clusterPath, rlDir, "2026-09-28"); err != nil {
		t.Fatal(err)
	}
	entries2, err := os.ReadDir(rlDir)
	if err != nil || len(entries2) != 1 {
		t.Fatalf("expected the existing entry to be kept, got %v, err %v", entries2, err)
	}
}

func TestApplyCiteCheckDemotes(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "c.json")
	writeJSON(t, clusterPath, `{"signal_type":"tool_error","incidents":`+fixtureIncidents+`}`)
	proposalPath := filepath.Join(dir, "p-1.json")
	writeJSON(t, proposalPath, `{"id":"glitch-83","implicated_artifact":"/g/CLAUDE.md#citations",
	  "signal_type":"tool_error","fix_type":"strengthen",
	  "evidence":["362498af:229"],
	  "diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"expected fewer glitches",
	  "extra":"keep me"}`)

	result, err := ApplyCiteCheck(proposalPath, clusterPath, filepath.Join(dir, "reason-log"), "2026-09-28")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "demoted" {
		t.Fatalf("status = %q, notes = %q", result.Status, result.Notes)
	}
	data, err := os.ReadFile(proposalPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var confidence, reasonLog, extra string
	json.Unmarshal(m["confidence"], &confidence)
	json.Unmarshal(m["reason_log"], &reasonLog)
	json.Unmarshal(m["extra"], &extra)
	if confidence != "medium" {
		t.Errorf("confidence = %q", confidence)
	}
	if !strings.HasPrefix(reasonLog, "citation check: confidence capped high→medium") {
		t.Errorf("reason_log = %q", reasonLog)
	}
	if !strings.Contains(reasonLog, "expected fewer glitches") {
		t.Errorf("reason_log dropped the original text: %q", reasonLog)
	}
	if extra != "keep me" {
		t.Errorf("extra field not preserved: %q", extra)
	}
}

func TestApplyCiteCheckFencedProposalDemotes(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "c.json")
	writeJSON(t, clusterPath, `{"signal_type":"tool_error","incidents":`+fixtureIncidents+`}`)
	proposalPath := filepath.Join(dir, "p-1.json")
	writeJSON(t, proposalPath, "```json\n"+`{"id":"glitch-83","implicated_artifact":"/g/CLAUDE.md#citations",
	  "signal_type":"tool_error","fix_type":"strengthen",
	  "evidence":["362498af:229"],
	  "diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"expected fewer glitches"}`+"\n```\n")

	result, err := ApplyCiteCheck(proposalPath, clusterPath, filepath.Join(dir, "reason-log"), "2026-09-28")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "demoted" {
		t.Fatalf("status = %q", result.Status)
	}
}

func TestApplyCiteCheckStaleRefSkipsByteIdentical(t *testing.T) {
	dir := t.TempDir()
	clusterPath := filepath.Join(dir, "c.json")
	writeJSON(t, clusterPath, `{"signal_type":"stale-ref","incidents":[]}`)
	proposalPath := filepath.Join(dir, "p-1.json")
	content := `{"id":"glitch-stale","implicated_artifact":"/g/CLAUDE.md#imports",
	  "signal_type":"stale-ref","fix_type":"fix-stale",
	  "evidence":[{"path":"x","line":1}],
	  "diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}`
	writeJSON(t, proposalPath, content)

	result, err := ApplyCiteCheck(proposalPath, clusterPath, filepath.Join(dir, "reason-log"), "2026-09-28")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "ok" {
		t.Fatalf("status = %q", result.Status)
	}
	after, err := os.ReadFile(proposalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != content {
		t.Errorf("proposal file was modified:\nbefore: %s\nafter:  %s", content, after)
	}
}
