package applier

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/noamsto/agent-smith/internal/analyst"
)

const (
	StatusReady       = "ready"
	StatusUnresolved  = "skip-unresolved"
	StatusMissingFile = "skip-missing-file"
)

// PlanEntry is one resolved proposal in apply-plan.json. Status gates whether the
// runbook acts on it.
type PlanEntry struct {
	ProposalID string `json:"proposal_id"`
	RepoRoot   string `json:"repo_root"`
	FilePath   string `json:"file_path"`
	Section    string `json:"section"`
	Owner      string `json:"owner"`
	BranchName string `json:"branch_name"`
	Base       string `json:"base"`
	Status     string `json:"status"`
}

// Target reconstructs the Target an entry was resolved from.
func (e PlanEntry) Target() Target {
	return Target{
		RepoRoot: e.RepoRoot, FilePath: e.FilePath, Section: e.Section,
		Owner: e.Owner, BranchName: e.BranchName, Base: e.Base,
	}
}

// Prepare reads proposals.json (an array of analyst.Proposal), resolves each, and
// assigns a Status. Unresolvable or missing-file proposals are recorded as skips
// rather than failing the batch. The plan is sorted by ProposalID.
func Prepare(proposalsPath string) ([]PlanEntry, error) {
	data, err := os.ReadFile(proposalsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", proposalsPath, err)
	}
	var props []analyst.Proposal
	if err := json.Unmarshal(data, &props); err != nil {
		return nil, fmt.Errorf("parse %s: %w", proposalsPath, err)
	}
	plan := make([]PlanEntry, 0, len(props))
	for _, p := range props {
		e := PlanEntry{ProposalID: p.ID}
		tg, err := Resolve(p)
		if err != nil {
			e.Status = StatusUnresolved
			plan = append(plan, e)
			continue
		}
		e.RepoRoot, e.FilePath, e.Section = tg.RepoRoot, tg.FilePath, tg.Section
		e.Owner, e.BranchName, e.Base = tg.Owner, tg.BranchName, tg.Base
		if _, statErr := os.Stat(tg.FilePath); statErr != nil && p.FixType != "add" {
			e.Status = StatusMissingFile
		} else {
			e.Status = StatusReady
		}
		plan = append(plan, e)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].ProposalID < plan[j].ProposalID })
	return plan, nil
}

// WritePlan writes the plan as an indented JSON array.
func WritePlan(plan []PlanEntry, out string) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, append(data, '\n'), 0o644)
}

// ReadPlan loads a plan written by WritePlan.
func ReadPlan(path string) ([]PlanEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var plan []PlanEntry
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return plan, nil
}

// FindEntry returns the plan entry for a proposal id.
func FindEntry(plan []PlanEntry, id string) (PlanEntry, error) {
	for _, e := range plan {
		if e.ProposalID == id {
			return e, nil
		}
	}
	return PlanEntry{}, fmt.Errorf("no plan entry for proposal %q", id)
}
