package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noamsto/agent-smith/internal/analyst"
)

func TestSuggest(t *testing.T) {
	// Two ready proposals on the same artifact share a group → one PR section
	// enumerating both; a third targets a different file → its own PR.
	plan := []PlanEntry{
		{ProposalID: "p-a", GroupID: "r-claude", RepoRoot: "/r", FilePath: "/r/CLAUDE.md", Owner: "nix-config",
			BranchName: "docs/agent-smith-r-claude", Base: "main", Status: StatusReady},
		{ProposalID: "p-b", GroupID: "r-claude", RepoRoot: "/r", FilePath: "/r/CLAUDE.md", Owner: "nix-config",
			BranchName: "docs/agent-smith-r-claude", Base: "main", Status: StatusReady},
		{ProposalID: "p-skip", Status: StatusUnresolved},
	}
	props := []analyst.Proposal{
		{ID: "p-a", ImplicatedArtifact: "/r/CLAUDE.md#reading-code", FixType: "strengthen",
			Confidence: "high", Diagnosis: "rule ignored", ProposedChange: "make imperative"},
		{ID: "p-b", ImplicatedArtifact: "/r/CLAUDE.md#commits", FixType: "add",
			Confidence: "medium", Diagnosis: "missing commit rule", ProposedChange: "add rule"},
	}
	md := Suggest(plan, props)
	for _, want := range []string{
		"## PR `docs/agent-smith-r-claude` — 2 proposal(s)",
		"### p-a", "### p-b", "/r/CLAUDE.md", "strengthen", "high",
		"main", "/r", "rule ignored", "make imperative", "missing commit rule",
		"## Skipped", "`p-skip`", "skip-unresolved",
		"1 PR(s) across grouped proposals; 1 skipped",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("suggest output missing %q\n---\n%s", want, md)
		}
	}
}

func TestSuggestNoSkips(t *testing.T) {
	plan := []PlanEntry{{ProposalID: "p1", GroupID: "r-x", Owner: "personal", FilePath: "/r/x.md", BranchName: "docs/agent-smith-r-x", Base: "main", RepoRoot: "/r", Status: StatusReady}}
	props := []analyst.Proposal{{ID: "p1", FixType: "add", Confidence: "low", Diagnosis: "d", ProposedChange: "c", ImplicatedArtifact: "/r/x.md"}}
	md := Suggest(plan, props)
	if strings.Contains(md, "## Skipped") {
		t.Error("no Skipped section expected when nothing is skipped")
	}
}

func TestWriteSuggestions(t *testing.T) {
	out := filepath.Join(t.TempDir(), "suggestions.md")
	if err := WriteSuggestions("# x\n", out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil || string(data) != "# x\n" {
		t.Fatalf("round-trip: %v %q", err, data)
	}
}
