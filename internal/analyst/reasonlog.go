package analyst

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Reason-log outcome states. An entry is born Open; the applier flips it to
// Merged/Closed once the PR is resolved. Closed (a PR the user closed without
// merging) and Rejected mark work the loop must NOT regenerate.
const (
	OutcomeOpen     = "open"
	OutcomeMerged   = "merged"
	OutcomeClosed   = "closed"
	OutcomeRejected = "rejected"
)

// PRPlaceholder is the slot the applier replaces with the PR link. WriteReasonLogs
// emits it; the applier's AppendPRLink consumes it.
const PRPlaceholder = "<!-- PR link appended by the applier; outcome appended by deja-vu -->"

// prPlaceholder is the package-local alias used by WriteReasonLogs.
const prPlaceholder = PRPlaceholder

var outcomeRe = regexp.MustCompile(`<!-- outcome: (\w+) -->`)

// outcomeMarker renders the machine-readable outcome line.
func outcomeMarker(state string) string {
	return fmt.Sprintf("<!-- outcome: %s -->", state)
}

// Outcome returns the outcome state recorded in a reason-log entry's content, or
// "" if no marker is present.
func Outcome(content string) string {
	if m := outcomeRe.FindStringSubmatch(content); m != nil {
		return m[1]
	}
	return ""
}

// SetOutcome rewrites the outcome marker in a reason-log entry's content to state.
// It returns the updated content and whether a marker was found and changed.
func SetOutcome(content, state string) (string, bool) {
	if !outcomeRe.MatchString(content) {
		return content, false
	}
	return outcomeRe.ReplaceAllString(content, outcomeMarker(state)), true
}

// Entry is a parsed reason-log entry: the proposal id (heading), the implicated
// artifact, the driving signal, and the recorded outcome.
type Entry struct {
	ID       string
	Artifact string // implicated_artifact, may carry a #section suffix
	Signal   string
	Outcome  string
	Path     string
}

// artifactPath strips an optional #section suffix and normalizes a worktree path
// to its canonical form, so a cluster's bare artifact and an entry's
// `path#section` (possibly recorded against a since-deleted worktree) compare
// equal. Symlink resolution is best-effort: an unresolvable path is cleaned only.
func artifactPath(artifact string) string {
	p := artifact
	if i := strings.IndexByte(p, '#'); i >= 0 {
		p = p[:i]
	}
	p = filepath.Clean(p)
	if real, err := filepath.EvalSymlinks(p); err == nil {
		return real
	}
	return p
}

var (
	headingRe  = regexp.MustCompile(`(?m)^# (.+)$`)
	artifactMd = regexp.MustCompile(`(?m)^\*\*Artifact:\*\* (.+?)\s*$`)
	signalMd   = regexp.MustCompile(`(?m)^\*\*Signal:\*\* (.+?)\s*$`)
)

// parseEntry extracts the structured fields from one reason-log markdown body.
func parseEntry(content, path string) Entry {
	e := Entry{Path: path, Outcome: OutcomeOpen}
	if m := headingRe.FindStringSubmatch(content); m != nil {
		e.ID = strings.TrimSpace(m[1])
	}
	if m := artifactMd.FindStringSubmatch(content); m != nil {
		e.Artifact = strings.TrimSpace(m[1])
	}
	if m := signalMd.FindStringSubmatch(content); m != nil {
		e.Signal = strings.TrimSpace(m[1])
	}
	if m := outcomeRe.FindStringSubmatch(content); m != nil {
		e.Outcome = m[1]
	}
	return e
}

// ReadEntries parses every *.md reason-log entry under dir. A missing dir yields
// no entries (the ledger may not exist on a first run).
func ReadEntries(dir string) ([]Entry, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		out = append(out, parseEntry(string(data), path))
	}
	return out, nil
}

// rejectedKeys returns the set of canonical "artifact\x00signal" keys for entries
// whose outcome marks them as not-to-be-regenerated (closed or rejected).
func rejectedKeys(entries []Entry) map[string]bool {
	keys := map[string]bool{}
	for _, e := range entries {
		if e.Outcome != OutcomeClosed && e.Outcome != OutcomeRejected {
			continue
		}
		keys[artifactPath(e.Artifact)+"\x00"+e.Signal] = true
	}
	return keys
}

// FilterRejected drops clusters whose (artifact, signal) already has a reason-log
// entry with a closed/rejected outcome — the proposal the user declined on a prior
// run, which the loop must not regenerate. It returns the kept clusters and the
// dropped ones (so the caller can log each skip rather than silently dropping it).
func FilterRejected(clusters []Cluster, entries []Entry) (kept, skipped []Cluster) {
	rejected := rejectedKeys(entries)
	if len(rejected) == 0 {
		return clusters, nil
	}
	for _, c := range clusters {
		if rejected[artifactPath(c.Artifact)+"\x00"+c.SignalType] {
			skipped = append(skipped, c)
			continue
		}
		kept = append(kept, c)
	}
	return kept, skipped
}
