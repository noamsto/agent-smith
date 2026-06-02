package applier

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenAndDropWorktree(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	tg := Target{RepoRoot: root, FilePath: filepath.Join(root, "CLAUDE.md"),
		BranchName: "docs/agent-smith-test", Base: "main"}

	wt, err := Open(tg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, "CLAUDE.md")); err != nil {
		t.Errorf("worktree missing seeded file: %v", err)
	}
	if wf := WorktreeFile(tg, wt); wf != filepath.Join(wt, "CLAUDE.md") {
		t.Errorf("WorktreeFile = %q", wf)
	}
	tgOut := Target{RepoRoot: root, FilePath: "/tmp/completely-unrelated-file"}
	if wf := WorktreeFile(tgOut, wt); wf != "/tmp/completely-unrelated-file" {
		t.Errorf("expected fallback for out-of-tree path, got %q", wf)
	}
	out, err := git(root, "branch", "--list", "docs/agent-smith-test")
	if err != nil {
		t.Fatalf("git branch --list: %v", err)
	}
	if len(out) == 0 {
		t.Error("branch was not created")
	}
	if err := Drop(root, wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("worktree dir should be gone after Drop")
	}
}
