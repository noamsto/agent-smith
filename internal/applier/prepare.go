package applier

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noamsto/agent-smith/internal/analyst"
)

const (
	StatusReady       = "ready"
	StatusUnresolved  = "skip-unresolved"
	StatusMissingFile = "skip-missing-file"
	StatusDeclined    = "skip-declined"
	StatusUnrouted    = "skip-unrouted"
)

// PlanEntry is one resolved proposal in apply-plan.json. Status gates whether the
// runbook acts on it; Reason annotates a non-ready entry so the orchestrator can
// surface why it was skipped. GroupID buckets ready entries that target the same
// artifact in the same repo so they share one worktree/branch/PR (issue #9): a
// per-proposal PR against a shared one-line file is a guaranteed conflict. Every
// entry in a group carries the same GroupID and BranchName.
type PlanEntry struct {
	ProposalID string `json:"proposal_id"`
	GroupID    string `json:"group_id"`
	RepoRoot   string `json:"repo_root"`
	FilePath   string `json:"file_path"`
	Section    string `json:"section"`
	Owner      string `json:"owner"`
	BranchName string `json:"branch_name"`
	Base       string `json:"base"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
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
// rather than failing the batch. settingsRepo is the repo root owning the Claude
// Code settings layers; `escalate-out-of-instructions` proposals route there
// instead of the implicated repo (the hook/default cannot land in the implicated
// worktree). When settingsRepo is empty or unresolvable, those proposals are
// marked skip-unrouted with a reason rather than dispatching a doomed editor. Ready
// entries that target the same artifact in the same repo share a GroupID and a group
// branch, so the apply loop lands them in one worktree/PR. The plan is sorted by
// ProposalID.
func Prepare(proposalsPath, settingsRepo string) ([]PlanEntry, error) {
	data, err := os.ReadFile(proposalsPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", proposalsPath, err)
	}
	var props []analyst.Proposal
	if err := json.Unmarshal(data, &props); err != nil {
		return nil, fmt.Errorf("parse %s: %w", proposalsPath, err)
	}
	plan := make([]PlanEntry, 0, len(props))
	// group keys a (repo, artifact) bucket to its shared identity. escalate folds the
	// branch prefix to chore/* (matching commitType) when any member is an escalate
	// fix; an all-prose group stays docs/*.
	type group struct {
		id       string
		escalate bool
	}
	groups := map[string]*group{}
	for _, p := range props {
		e := prepareOne(p, settingsRepo)
		// Group ready entries by (repo, artifact) so they share a worktree/branch/PR.
		if e.Status == StatusReady {
			key := e.RepoRoot + "\x00" + e.FilePath
			g, ok := groups[key]
			if !ok {
				g = &group{id: artifactSlug(e.RepoRoot, e.FilePath)}
				groups[key] = g
			}
			g.escalate = g.escalate || commitType(p.FixType) == "chore"
			e.GroupID = g.id
		}
		plan = append(plan, e)
	}
	for i := range plan {
		e := &plan[i]
		if e.GroupID == "" {
			continue
		}
		prefix := "docs"
		if groups[e.RepoRoot+"\x00"+e.FilePath].escalate {
			prefix = "chore"
		}
		e.BranchName = prefix + "/agent-smith-" + e.GroupID
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].ProposalID < plan[j].ProposalID })
	return plan, nil
}

func prepareOne(p analyst.Proposal, settingsRepo string) PlanEntry {
	e := PlanEntry{ProposalID: p.ID}
	if p.FixType == "skip" { // fix_type=skip: declined, no edit — target fields stay zero
		e.Status = StatusDeclined
		return e
	}
	if p.FixType == "escalate-out-of-instructions" {
		if settingsRepo == "" {
			e.Status = StatusUnrouted
			e.Reason = "escalation needs a settings repo (--settings-repo / AGENT_SMITH_SETTINGS_REPO); none configured"
			return e
		}
		tg, err := ResolveEscalation(p, settingsRepo)
		if err != nil {
			e.Status = StatusUnrouted
			e.Reason = fmt.Sprintf("settings repo unresolvable: %v", err)
			return e
		}
		e.RepoRoot, e.FilePath, e.Section = tg.RepoRoot, tg.FilePath, tg.Section
		e.Owner, e.Base = tg.Owner, tg.Base
		e.Status = StatusReady
		return e
	}
	tg, err := Resolve(p)
	if err != nil {
		e.Status = StatusUnresolved
		return e
	}
	e.RepoRoot, e.FilePath, e.Section = tg.RepoRoot, tg.FilePath, tg.Section
	e.Owner, e.Base = tg.Owner, tg.Base
	if _, statErr := os.Stat(tg.FilePath); statErr != nil && p.FixType != "add" {
		e.Status = StatusMissingFile
		return e
	}
	e.Status = StatusReady
	return e
}

// artifactSlug is the stable group identity for a (repo, artifact) pair: the repo
// basename and relative path keep the slug readable, and a short hash of the full
// repo path disambiguates two distinct repos that happen to share a basename.
func artifactSlug(repoRoot, file string) string {
	rel, err := filepath.Rel(repoRoot, file)
	if err != nil {
		rel = filepath.Base(file)
	}
	sum := sha1.Sum([]byte(repoRoot))
	return slug(filepath.Base(repoRoot)+"-"+rel) + "-" + hex.EncodeToString(sum[:])[:8]
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

// ReadyGroupIDs returns the group ids of every ready entry, in first-seen (plan)
// order with no duplicates — the units the apply loop opens one worktree/PR for.
func ReadyGroupIDs(plan []PlanEntry) []string {
	seen := map[string]bool{}
	var ids []string
	for _, e := range plan {
		if e.Status == StatusReady && !seen[e.GroupID] {
			seen[e.GroupID] = true
			ids = append(ids, e.GroupID)
		}
	}
	return ids
}

// FindGroup returns the ready entries sharing a GroupID, sorted by ProposalID. They
// share one repo/artifact/branch, so the first entry's Target() drives the worktree.
func FindGroup(plan []PlanEntry, groupID string) ([]PlanEntry, error) {
	var group []PlanEntry
	for _, e := range plan {
		if e.GroupID == groupID && e.Status == StatusReady {
			group = append(group, e)
		}
	}
	if len(group) == 0 {
		return nil, fmt.Errorf("no ready plan entries in group %q", groupID)
	}
	sort.Slice(group, func(i, j int) bool { return group[i].ProposalID < group[j].ProposalID })
	return group, nil
}
