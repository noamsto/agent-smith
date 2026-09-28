package analyst

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Citation pairs a cited window with a verbatim quote drawn from it, backing
// an Oracle proposal's evidence refs with something checkCitations can verify
// against the cluster.
type Citation struct {
	Window string `json:"window"`
	Quote  string `json:"quote"`
}

// Result is the outcome of checking one proposal's citations against its cluster.
type Result struct {
	ID       string
	Status   string // "ok", "demoted", or "rejected"
	Failures []string
	Notes    string // demote reason, e.g. "<ref>: no verified quote; …"
}

// citeProposal is a local decode of the proposal shape cite-check cares about.
// Evidence is left as raw messages so non-string entries (e.g. #45's
// {path,line} stale-ref evidence) are ignored rather than rejected, and
// Proposal itself stays untouched.
type citeProposal struct {
	ID                 string            `json:"id"`
	ImplicatedArtifact string            `json:"implicated_artifact"`
	SignalType         string            `json:"signal_type"`
	FixType            string            `json:"fix_type"`
	Confidence         string            `json:"confidence"`
	ReasonLog          string            `json:"reason_log"`
	Evidence           []json.RawMessage `json:"evidence"`
	Citations          []Citation        `json:"citations"`
}

type citeIncident struct {
	SessionID string          `json:"session_id"`
	Detail    json.RawMessage `json:"detail"`
	Window    []citeWindow    `json:"window"`
}

type citeWindow struct {
	Turn    int    `json:"turn"`
	Excerpt string `json:"excerpt"`
}

// refRe matches the leading "<session-id-or-prefix>:<turn>[-<turn>]" token of an
// evidence string or citations[].window entry; anything after it is free text.
// The hex-only session class keeps "CLAUDE.md:42"-style strings from counting.
var refRe = regexp.MustCompile(`(?i)^([0-9a-f][0-9a-f-]{7,}):(\d+)(?:-(\d+))?(?:[\s,;)]|$)`)

// ref is a parsed, not-yet-resolved citation reference.
type ref struct {
	token string // session id or prefix, as written
	a, b  int
	raw   string // canonical "<token>:<a>[-<b>]", for messages
}

func parseRef(s string) (ref, bool) {
	m := refRe.FindStringSubmatch(s)
	if m == nil {
		return ref{}, false
	}
	a, err := strconv.Atoi(m[2])
	if err != nil {
		return ref{}, false
	}
	b := a
	raw := m[1] + ":" + m[2]
	if m[3] != "" {
		b, err = strconv.Atoi(m[3])
		if err != nil {
			return ref{}, false
		}
		raw += "-" + m[3]
	}
	return ref{token: m[1], a: a, b: b, raw: raw}, true
}

// resolvedRef is a ref that has been matched to exactly one session and whose
// full turn range is covered by that session's incident windows.
type resolvedRef struct {
	raw       string
	sessionID string
	a, b      int
	incidents []citeIncident
}

// resolveRef finds the incidents whose session_id the ref's token uniquely
// prefix-matches, and checks every turn in [a,b] is a window turn of one of
// them (a gapped window fails this).
func resolveRef(r ref, incidents []citeIncident) (resolvedRef, error) {
	if r.b < r.a {
		return resolvedRef{}, fmt.Errorf("%s: invalid range (end before start)", r.raw)
	}
	token := strings.ToLower(r.token)
	seen := map[string]bool{}
	var matches []string
	for _, inc := range incidents {
		full := strings.ToLower(inc.SessionID)
		if strings.HasPrefix(full, token) && !seen[full] {
			seen[full] = true
			matches = append(matches, inc.SessionID)
		}
	}
	switch len(matches) {
	case 0:
		return resolvedRef{}, fmt.Errorf("%s: no session matching %q", r.raw, r.token)
	case 1:
		// resolved
	default:
		return resolvedRef{}, fmt.Errorf("%s: ambiguous session %q (%d matches)", r.raw, r.token, len(matches))
	}
	sessionID := matches[0]

	var sessionIncidents []citeIncident
	turns := map[int]bool{}
	for _, inc := range incidents {
		if !strings.EqualFold(inc.SessionID, sessionID) {
			continue
		}
		sessionIncidents = append(sessionIncidents, inc)
		for _, w := range inc.Window {
			turns[w.Turn] = true
		}
	}
	for t := r.a; t <= r.b; t++ {
		if !turns[t] {
			return resolvedRef{}, fmt.Errorf("%s: turn %d not in window", r.raw, t)
		}
	}
	return resolvedRef{raw: r.raw, sessionID: sessionID, a: r.a, b: r.b, incidents: sessionIncidents}, nil
}

var (
	unicodeEscapeRe = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
	slashLetterRe   = regexp.MustCompile(`\\+[nrt]`)
)

// normalizeQuote makes a quote and an excerpt/detail string comparable
// regardless of how each side happened to escape whitespace: decode \uXXXX
// sequences, collapse \n/\r/\t escapes (however many backslashes) to a space,
// drop any remaining backslashes, then collapse and trim whitespace.
func normalizeQuote(s string) string {
	s = unicodeEscapeRe.ReplaceAllStringFunc(s, func(m string) string {
		v, err := strconv.ParseInt(m[2:], 16, 32)
		if err != nil {
			return m
		}
		return string(rune(v))
	})
	s = slashLetterRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(s, `\`, "")
	return strings.Join(strings.Fields(s), " ")
}

// stringLeaves recursively collects every string value in an arbitrary JSON
// value (detail is an object with arbitrary, possibly nested, string values).
func stringLeaves(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}

// quoteFound checks a normalized quote against the excerpts of turns in
// [rr.a,rr.b] and, for any incident whose window overlaps that range, every
// string value of its detail.
func quoteFound(norm string, rr resolvedRef) bool {
	for _, inc := range rr.incidents {
		overlap := false
		for _, w := range inc.Window {
			if w.Turn < rr.a || w.Turn > rr.b {
				continue
			}
			overlap = true
			if strings.Contains(normalizeQuote(w.Excerpt), norm) {
				return true
			}
		}
		if !overlap {
			continue
		}
		for _, s := range stringLeaves(inc.Detail) {
			if strings.Contains(normalizeQuote(s), norm) {
				return true
			}
		}
	}
	return false
}

// evidenceRefs decodes the string-valued entries of evidence into refs;
// non-string entries (object-shaped stale-ref evidence) and strings that don't
// match the ref grammar are silently not citations.
func evidenceRefs(evidence []json.RawMessage) []ref {
	var out []ref
	for _, raw := range evidence {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		if r, ok := parseRef(s); ok {
			out = append(out, r)
		}
	}
	return out
}

// checkCitations verifies prop's evidence refs and citations against cluster
// and reports whether the proposal should pass,
// be demoted (unverifiable but not disprovable), or be rejected (a citation
// that is provably wrong).
func checkCitations(prop citeProposal, cluster Cluster) Result {
	res := Result{ID: prop.ID}
	if cluster.SignalType == "stale-ref" {
		res.Status = "ok"
		return res
	}
	if prop.FixType == "skip" {
		res.Status = "ok"
		return res
	}

	var incidents []citeIncident
	if err := json.Unmarshal(cluster.Incidents, &incidents); err != nil {
		res.Status = "rejected"
		res.Failures = []string{fmt.Sprintf("cluster incidents: %v", err)}
		return res
	}

	refs := evidenceRefs(prop.Evidence)

	var failures []string
	var resolvedEvidence []resolvedRef
	for _, r := range refs {
		rr, err := resolveRef(r, incidents)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		resolvedEvidence = append(resolvedEvidence, rr)
	}

	type verified struct {
		resolvedRef
		ok bool
	}
	var citations []verified
	for _, c := range prop.Citations {
		r, ok := parseRef(c.Window)
		if !ok {
			failures = append(failures, fmt.Sprintf("%s: not a valid citation window ref", c.Window))
			continue
		}
		rr, err := resolveRef(r, incidents)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		v := verified{resolvedRef: rr}
		if norm := normalizeQuote(c.Quote); len(norm) >= 8 {
			if quoteFound(norm, rr) {
				v.ok = true
			} else {
				failures = append(failures, fmt.Sprintf("%s: quote not found", c.Window))
			}
		}
		citations = append(citations, v)
	}

	if len(failures) > 0 {
		res.Status = "rejected"
		res.Failures = failures
		return res
	}

	if prop.Confidence != "high" {
		res.Status = "ok"
		return res
	}

	var notes []string
	seenNotes := map[string]bool{}
	addNote := func(n string) {
		if !seenNotes[n] {
			seenNotes[n] = true
			notes = append(notes, n)
		}
	}
	for _, r := range resolvedEvidence {
		verifiedForRef := false
		for _, c := range citations {
			if c.ok && strings.EqualFold(c.sessionID, r.sessionID) && r.a <= c.b && c.a <= r.b {
				verifiedForRef = true
				break
			}
		}
		if !verifiedForRef {
			addNote(fmt.Sprintf("%s: no verified quote", r.raw))
		}
	}
	for _, c := range citations {
		if !c.ok {
			addNote(fmt.Sprintf("%s: no verified quote", c.raw))
		}
	}
	if len(resolvedEvidence) == 0 && len(citations) == 0 {
		addNote("no citations")
	}
	if len(notes) == 0 {
		res.Status = "ok"
		return res
	}
	res.Status = "demoted"
	res.Notes = strings.Join(notes, "; ")
	return res
}

// ApplyCiteCheck runs checkCitations for the proposal at proposalPath against
// the cluster at clusterPath, then applies the outcome: a demote rewrites the
// proposal in place, a reject writes a reason-log entry and renames the
// proposal so LoadProposals never assembles it.
func ApplyCiteCheck(proposalPath, clusterPath, reasonLogDir, date string) (Result, error) {
	clusterData, err := os.ReadFile(clusterPath)
	if err != nil {
		return Result{}, fmt.Errorf("read cluster %s: %w", clusterPath, err)
	}
	var cluster Cluster
	if err := json.Unmarshal(clusterData, &cluster); err != nil {
		return Result{}, fmt.Errorf("parse cluster %s: %w", clusterPath, err)
	}
	if cluster.SignalType == "stale-ref" {
		// Peek the id best-effort for a nicer CLI line; never fail on it — the
		// point of checking signal_type first is to skip needing a valid proposal.
		id := ""
		if data, err := os.ReadFile(proposalPath); err == nil {
			var p struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(StripCodeFence(data), &p) == nil {
				id = p.ID
			}
		}
		return Result{ID: id, Status: "ok"}, nil
	}

	propData, err := os.ReadFile(proposalPath)
	if err != nil {
		return Result{}, fmt.Errorf("read proposal %s: %w", proposalPath, err)
	}
	stripped := StripCodeFence(propData)
	var prop citeProposal
	if err := json.Unmarshal(stripped, &prop); err != nil {
		return Result{}, fmt.Errorf("parse proposal %s: %w", proposalPath, err)
	}

	result := checkCitations(prop, cluster)
	switch result.Status {
	case "demoted":
		if err := rewriteDemoted(proposalPath, stripped, result.Notes); err != nil {
			return result, fmt.Errorf("rewrite %s: %w", proposalPath, err)
		}
	case "rejected":
		if err := writeCitationRejection(prop, result, reasonLogDir, date); err != nil {
			return result, fmt.Errorf("write reason-log for %s: %w", proposalPath, err)
		}
		if err := os.Rename(proposalPath, proposalPath+".cite-rejected"); err != nil {
			return result, fmt.Errorf("rename %s: %w", proposalPath, err)
		}
	}
	return result, nil
}

// rewriteDemoted caps confidence to medium and prefixes reason_log, rewriting
// the proposal file via a raw map so fields cite-check doesn't know about
// (schema additions) survive the round trip.
func rewriteDemoted(path string, original []byte, notes string) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(original, &m); err != nil {
		return err
	}
	var reasonLog string
	if err := json.Unmarshal(m["reason_log"], &reasonLog); err != nil {
		return err
	}
	newReasonLog := fmt.Sprintf("citation check: confidence capped high→medium — %s\n\n%s", notes, reasonLog)
	rl, err := json.Marshal(newReasonLog)
	if err != nil {
		return err
	}
	m["reason_log"] = rl
	conf, err := json.Marshal("medium")
	if err != nil {
		return err
	}
	m["confidence"] = conf
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// writeCitationRejection writes the reason-log entry for a citation-check
// rejection, distinct in heading and outcome from the normal WriteReasonLogs
// entry: LinkReasonLog never matches the heading, and neither FilterRejected
// nor Escalate treats an uncited outcome as a settled finding.
func writeCitationRejection(prop citeProposal, result Result, dir, date string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	slug := slugify(prop.ID)
	if slug == "" {
		slug = fmt.Sprintf("%08x", fnv32a(prop.ID))
	}
	path := filepath.Join(dir, date+"-"+slug+"-citation-rejected.md")

	var b strings.Builder
	fmt.Fprintf(&b, "# %s — citation check failed\n\n", prop.ID)
	fmt.Fprintf(&b, "**Artifact:** %s  \n", prop.ImplicatedArtifact)
	if prop.SignalType != "" {
		fmt.Fprintf(&b, "**Signal:** %s  \n", prop.SignalType)
	}
	fmt.Fprintf(&b, "**Date:** %s\n\n", date)
	fmt.Fprintf(&b, "## Failed citations\n\n")
	for _, f := range result.Failures {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	fmt.Fprintf(&b, "\n%s\n", outcomeMarker(OutcomeUncited))

	// O_EXCL: a second reject run on the same date keeps the existing entry.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	_, err = f.WriteString(b.String())
	return err
}
