package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/noamsto/agent-smith/internal/analyst"
	"github.com/noamsto/agent-smith/internal/applier"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: applier <prepare|open|submit|suggest> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
	case "prepare":
		runPrepare(os.Args[2:])
	case "open":
		runOpen(os.Args[2:])
	case "submit":
		runSubmit(os.Args[2:])
	case "suggest":
		runSuggest(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "applier:", err)
	os.Exit(1)
}

func runPrepare(args []string) {
	fs := flag.NewFlagSet("prepare", flag.ExitOnError)
	proposals := fs.String("proposals", "proposals.json", "assembled proposals file")
	out := fs.String("out", "apply-plan.json", "output apply-plan file")
	_ = fs.Parse(args)

	plan, err := applier.Prepare(*proposals)
	if err != nil {
		fatal(err)
	}
	if err := applier.WritePlan(plan, *out); err != nil {
		fatal(err)
	}
	ready := 0
	for _, e := range plan {
		if e.Status == applier.StatusReady {
			ready++
		} else {
			fmt.Fprintf(os.Stderr, "skip %s: %s\n", e.ProposalID, e.Status)
		}
	}
	fmt.Printf("wrote %d plan entries (%d ready) to %s\n", len(plan), ready, *out)
}

func runOpen(args []string) {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	planPath := fs.String("plan", "apply-plan.json", "apply-plan file")
	group := fs.String("group", "", "group id to open a worktree for")
	_ = fs.Parse(args)

	if *group == "" {
		fmt.Fprintln(os.Stderr, "applier open: --group is required")
		os.Exit(2)
	}

	plan, err := applier.ReadPlan(*planPath)
	if err != nil {
		fatal(err)
	}
	entries, err := applier.FindGroup(plan, *group)
	if err != nil {
		fatal(err)
	}
	tg := entries[0].Target()
	wt, err := applier.Open(tg)
	if err != nil {
		fatal(err)
	}
	// Line 1: worktree path. Line 2: the file every proposal in the group edits.
	// Lines 3+: the group's proposal ids, in apply order (edit them sequentially).
	fmt.Println(wt)
	fmt.Println(applier.WorktreeFile(tg, wt))
	for _, e := range entries {
		fmt.Println(e.ProposalID)
	}
}

func runSubmit(args []string) {
	fs := flag.NewFlagSet("submit", flag.ExitOnError)
	planPath := fs.String("plan", "apply-plan.json", "apply-plan file")
	proposalsPath := fs.String("proposals", "proposals.json", "assembled proposals file")
	group := fs.String("group", "", "group id to submit")
	wt := fs.String("worktree", "", "worktree path returned by `open`")
	reasonLog := fs.String("reason-log-dir", "reason-log", "reason-log directory")
	resultDir := fs.String("editor-result-dir", "", "directory holding editor-result-<id>.json per group proposal")
	draft := fs.Bool("draft", false, "open the PR as a draft")
	_ = fs.Parse(args)

	if *group == "" {
		fmt.Fprintln(os.Stderr, "applier submit: --group is required")
		os.Exit(2)
	}
	if *wt == "" {
		fmt.Fprintln(os.Stderr, "applier submit: --worktree is required")
		os.Exit(2)
	}
	if *resultDir == "" {
		fmt.Fprintln(os.Stderr, "applier submit: --editor-result-dir is required")
		os.Exit(2)
	}

	plan, err := applier.ReadPlan(*planPath)
	if err != nil {
		fatal(err)
	}
	entries, err := applier.FindGroup(plan, *group)
	if err != nil {
		fatal(err)
	}
	tg := entries[0].Target()

	items := make([]applier.GroupItem, 0, len(entries))
	for _, e := range entries {
		prop, err := loadProposal(*proposalsPath, e.ProposalID)
		if err != nil {
			fatal(err)
		}
		ed, err := loadEditorResult(filepath.Join(*resultDir, "editor-result-"+e.ProposalID+".json"))
		if err != nil {
			fatal(err)
		}
		items = append(items, applier.GroupItem{Proposal: prop, Editor: ed})
	}

	defer func() {
		if derr := applier.Drop(tg.RepoRoot, *wt); derr != nil {
			fmt.Fprintln(os.Stderr, "warning: drop worktree:", derr)
		}
	}()

	url, skipped, err := applier.Submit(applier.Run, tg, *wt, items, *reasonLog, *draft)
	if err != nil {
		_ = applier.Drop(tg.RepoRoot, *wt) // defer below is skipped by os.Exit in fatal()
		fatal(err)
	}
	if skipped {
		fmt.Printf("skipped group %s (every editor declined or no change)\n", *group)
		return
	}
	fmt.Printf("opened PR for group %s: %s\n", *group, url)
}

func loadAllProposals(path string) ([]analyst.Proposal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read proposals %s: %w", path, err)
	}
	var props []analyst.Proposal
	if err := json.Unmarshal(data, &props); err != nil {
		return nil, fmt.Errorf("parse proposals %s: %w", path, err)
	}
	return props, nil
}

func loadProposal(path, id string) (analyst.Proposal, error) {
	props, err := loadAllProposals(path)
	if err != nil {
		return analyst.Proposal{}, err
	}
	for _, p := range props {
		if p.ID == id {
			return p, nil
		}
	}
	return analyst.Proposal{}, fmt.Errorf("proposal %q not in %s", id, path)
}

func runSuggest(args []string) {
	fs := flag.NewFlagSet("suggest", flag.ExitOnError)
	planPath := fs.String("plan", "apply-plan.json", "apply-plan file")
	proposalsPath := fs.String("proposals", "proposals.json", "assembled proposals file")
	out := fs.String("out", "suggestions.md", "output suggestions markdown file")
	_ = fs.Parse(args)

	plan, err := applier.ReadPlan(*planPath)
	if err != nil {
		fatal(err)
	}
	props, err := loadAllProposals(*proposalsPath)
	if err != nil {
		fatal(err)
	}
	if err := applier.WriteSuggestions(applier.Suggest(plan, props), *out); err != nil {
		fatal(err)
	}
	ready := 0
	for _, e := range plan {
		if e.Status == applier.StatusReady {
			ready++
		}
	}
	fmt.Printf("wrote suggestions for %d proposal(s) (%d actionable) to %s\n", len(plan), ready, *out)
}

func loadEditorResult(path string) (applier.EditorResult, error) {
	if path == "" {
		return applier.EditorResult{}, fmt.Errorf("--editor-result is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return applier.EditorResult{}, fmt.Errorf("read editor-result %s: %w", path, err)
	}
	ed, err := applier.ParseEditorResult(data)
	if err != nil {
		return applier.EditorResult{}, fmt.Errorf("parse editor-result %s: %w", path, err)
	}
	return ed, nil
}
