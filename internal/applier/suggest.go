package applier

import (
	"fmt"
	"os"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// Suggest renders a human-readable markdown index of what the applier would do:
// one section per actionable (ready) proposal — where it would land, the
// diagnosis, and the Oracle's proposed change — plus a list of skipped entries.
// It is pure: no edits, no git, no PRs.
func Suggest(plan []PlanEntry, props []analyst.Proposal) string {
	byID := make(map[string]analyst.Proposal, len(props))
	for _, p := range props {
		byID[p.ID] = p
	}
	var ready, skipped []PlanEntry
	for _, e := range plan {
		if e.Status == StatusReady {
			ready = append(ready, e)
		} else {
			skipped = append(skipped, e)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# agent-smith — suggested changes\n\n")
	fmt.Fprintf(&b, "%d actionable proposal(s); %d skipped.\n", len(ready), len(skipped))

	for _, e := range ready {
		fmt.Fprintf(&b, "\n## %s\n\n", e.ProposalID)
		p, ok := byID[e.ProposalID]
		if !ok {
			fmt.Fprintf(&b, "_(no matching proposal in proposals.json)_\n")
			continue
		}
		fmt.Fprintf(&b, "**Artifact:** %s  \n", p.ImplicatedArtifact)
		fmt.Fprintf(&b, "**Owner:** %s · **Fix type:** %s · **Confidence:** %s  \n", e.Owner, p.FixType, p.Confidence)
		fmt.Fprintf(&b, "**Would open a PR on branch** `%s` (base `%s`) in `%s`\n\n", e.BranchName, e.Base, e.RepoRoot)
		fmt.Fprintf(&b, "### Diagnosis\n\n%s\n\n", p.Diagnosis)
		fmt.Fprintf(&b, "### Proposed change\n\n```\n%s\n```\n", p.ProposedChange)
	}

	if len(skipped) > 0 {
		fmt.Fprintf(&b, "\n## Skipped\n\n")
		for _, e := range skipped {
			fmt.Fprintf(&b, "- `%s` — %s\n", e.ProposalID, e.Status)
		}
	}
	return b.String()
}

// WriteSuggestions writes the rendered index to out.
func WriteSuggestions(s, out string) error {
	return os.WriteFile(out, []byte(s), 0o644)
}
