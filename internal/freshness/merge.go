package freshness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// SignalType is the analyst.Cluster signal_type for a stale reference cluster.
const SignalType = "stale-ref"

// LoadAdjudications reads every adj-*.json file under dir and returns the set
// of ref ids adjudicated stale. dir == "" (no adjudication step run) yields an
// empty, non-nil map and no errors. A file that fails to read or decode is
// reported in errs (naming the file) and contributes nothing — default-drop,
// since an id this run cannot vouch for must not slip into "stale" by omission.
func LoadAdjudications(dir string) (stale map[string]bool, errs []error) {
	stale = map[string]bool{}
	if dir == "" {
		return stale, nil
	}
	paths, _ := filepath.Glob(filepath.Join(dir, "adj-*.json"))
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		var adjs []struct {
			ID      string `json:"id"`
			Verdict string `json:"verdict"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(analyst.StripCodeFence(data), &adjs); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p, err))
			continue
		}
		for _, a := range adjs {
			if a.Verdict == "stale" {
				stale[a.ID] = true
			}
		}
	}
	return stale, errs
}

// evidence is one stale-ref cluster's per-reference record — a Ref stripped of
// the fields (id, artifact, form) the cluster schema doesn't carry.
type evidence struct {
	Path        string   `json:"path"`
	Line        int      `json:"line"`
	RuleExcerpt string   `json:"rule_excerpt"`
	ResolvedTo  string   `json:"resolved_to"`
	SameName    []string `json:"same_name"`
}

// rejectedEvidence indexes the evidence bullets of closed/rejected stale-ref
// reason-log entries by canonical artifact key, once, so Clusters doesn't
// rescan every entry for every ref.
func rejectedEvidence(entries []analyst.Entry) map[string][]string {
	out := map[string][]string{}
	for _, e := range entries {
		if e.Signal != SignalType {
			continue
		}
		if e.Outcome != analyst.OutcomeClosed && e.Outcome != analyst.OutcomeRejected {
			continue
		}
		key := analyst.ArtifactKey(e.Artifact)
		out[key] = append(out[key], e.Evidence...)
	}
	return out
}

// isSuppressed reports whether some evidence bullet opens with path as its
// backtick-quoted leading token. Only a leading match counts — a bullet
// repointing an old path to this one ("`old.go` (line 3) → `path`") names path
// later in the bullet, not at the front, and must not suppress it.
func isSuppressed(bullets []string, path string) bool {
	prefix := "`" + path + "`"
	for _, b := range bullets {
		if strings.HasPrefix(b, prefix) {
			return true
		}
	}
	return false
}

// Clusters turns a freshness Report into one stale-ref analyst.Cluster per
// artifact. Kept refs are every Dead ref plus each AmbiguousMissing ref
// adjudicated stale; a ref already the subject of a closed/rejected stale-ref
// reason-log entry for the same artifact is suppressed rather than
// reclustered, and returned separately so the caller can log each skip.
func Clusters(r Report, stale map[string]bool, entries []analyst.Entry) (clusters []analyst.Cluster, suppressed []Ref, errs []error) {
	kept := make([]Ref, 0, len(r.Dead)+len(r.AmbiguousMissing))
	kept = append(kept, r.Dead...)
	for _, ref := range r.AmbiguousMissing {
		if stale[ref.ID] {
			kept = append(kept, ref)
		}
	}

	rejected := rejectedEvidence(entries)

	byArtifact := map[string][]Ref{}
	var order []string
	for _, ref := range kept {
		key := analyst.ArtifactKey(ref.Artifact)
		if isSuppressed(rejected[key], ref.Path) {
			suppressed = append(suppressed, ref)
			continue
		}
		if _, ok := byArtifact[ref.Artifact]; !ok {
			order = append(order, ref.Artifact)
		}
		byArtifact[ref.Artifact] = append(byArtifact[ref.Artifact], ref)
	}
	sort.Strings(order)

	for _, a := range order {
		refs := byArtifact[a]
		content, err := os.ReadFile(a)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a, err))
			continue
		}

		ev := make([]evidence, len(refs))
		for i, ref := range refs {
			sameName := ref.SameName
			if sameName == nil {
				sameName = []string{}
			}
			ev[i] = evidence{
				Path:        ref.Path,
				Line:        ref.Line,
				RuleExcerpt: ref.RuleExcerpt,
				ResolvedTo:  ref.ResolvedTo,
				SameName:    sameName,
			}
		}
		evJSON, err := json.Marshal(ev)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a, err))
			continue
		}

		truncated := analyst.TruncateArtifact(string(content))
		clusters = append(clusters, analyst.Cluster{
			ClusterID:       SignalType + "::" + a,
			SignalType:      SignalType,
			Artifact:        a,
			ArtifactContent: &truncated,
			ArtifactExists:  true,
			TotalIncidents:  len(ev),
			Incidents:       json.RawMessage("[]"),
			Evidence:        evJSON,
		})
	}

	return clusters, suppressed, errs
}
