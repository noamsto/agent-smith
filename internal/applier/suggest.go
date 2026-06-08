package applier

import (
	"fmt"
	"os"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// Suggest renders a human-readable markdown index of what the applier would do:
// one section per group (one PR), enumerating the ready proposals it carries —
// where they would land, each diagnosis, and the Oracle's proposed change — plus a
// list of skipped entries. It is pure: no edits, no git, no PRs.
func Suggest(plan []PlanEntry, props []analyst.Proposal) string {
	byID := make(map[string]analyst.Proposal, len(props))
	for _, p := range props {
		byID[p.ID] = p
	}
	groups := ReadyGroupIDs(plan)
	skipped := 0
	for _, e := range plan {
		if e.Status != StatusReady {
			skipped++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# agent-smith — suggested changes\n\n")
	fmt.Fprintf(&b, "%d PR(s) across grouped proposals; %d skipped.\n", len(groups), skipped)

	for _, gid := range groups {
		entries, _ := FindGroup(plan, gid)
		head := entries[0]
		fmt.Fprintf(&b, "\n## PR `%s` — %d proposal(s)\n\n", head.BranchName, len(entries))
		fmt.Fprintf(&b, "**Artifact:** %s  \n", head.FilePath)
		fmt.Fprintf(&b, "**Owner:** %s · **Base:** `%s` · **Repo:** `%s`\n", head.Owner, head.Base, head.RepoRoot)
		for _, e := range entries {
			fmt.Fprintf(&b, "\n### %s\n\n", e.ProposalID)
			p, ok := byID[e.ProposalID]
			if !ok {
				fmt.Fprintf(&b, "_(no matching proposal in proposals.json)_\n")
				continue
			}
			fmt.Fprintf(&b, "**Fix type:** %s · **Confidence:** %s\n\n", p.FixType, p.Confidence)
			fmt.Fprintf(&b, "**Diagnosis:** %s\n\n", p.Diagnosis)
			fmt.Fprintf(&b, "**Proposed change:**\n\n```\n%s\n```\n", p.ProposedChange)
		}
	}

	if skipped > 0 {
		fmt.Fprintf(&b, "\n## Skipped\n\n")
		for _, e := range plan {
			if e.Status != StatusReady {
				fmt.Fprintf(&b, "- `%s` — %s\n", e.ProposalID, e.Status)
			}
		}
	}
	return b.String()
}

// WriteSuggestions writes the rendered index to out.
func WriteSuggestions(s, out string) error {
	return os.WriteFile(out, []byte(s), 0o644)
}
