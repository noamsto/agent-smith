package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noamsto/agent-smith/internal/analyst"
)

func TestSuggest(t *testing.T) {
	plan := []PlanEntry{
		{ProposalID: "p-ready", RepoRoot: "/r", FilePath: "/r/CLAUDE.md", Owner: "nix-config",
			BranchName: "docs/agent-smith-p-ready", Base: "main", Status: StatusReady},
		{ProposalID: "p-skip", Status: StatusUnresolved},
	}
	props := []analyst.Proposal{
		{ID: "p-ready", ImplicatedArtifact: "/r/CLAUDE.md#reading-code", FixType: "strengthen",
			Confidence: "high", Diagnosis: "rule ignored", ProposedChange: "make imperative"},
	}
	md := Suggest(plan, props)
	for _, want := range []string{
		"## p-ready", "/r/CLAUDE.md#reading-code", "strengthen", "high",
		"docs/agent-smith-p-ready", "main", "/r", "rule ignored", "make imperative",
		"## Skipped", "`p-skip`", "skip-unresolved",
		"1 actionable proposal(s); 1 skipped",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("suggest output missing %q\n---\n%s", want, md)
		}
	}
}

func TestSuggestNoSkips(t *testing.T) {
	plan := []PlanEntry{{ProposalID: "p1", Owner: "personal", BranchName: "docs/agent-smith-p1", Base: "main", RepoRoot: "/r", Status: StatusReady}}
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
