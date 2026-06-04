package applier

import (
	"os"
	"path/filepath"
	"strings"
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

func TestOpenBasesOffOriginNotLocalTip(t *testing.T) {
	// Unpushed local commits on the base branch must NOT leak into the PR
	// branch (nix-config#2 shipped with an unrelated local commit). When a
	// remote-tracking origin/<base> exists, Open must branch from it.
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	gitRun := func(args ...string) []byte {
		out, err := git(root, args...)
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return out
	}
	seed := strings.TrimSpace(string(gitRun("rev-parse", "HEAD")))
	// Simulate a fetched origin/main at the seed commit.
	gitRun("update-ref", "refs/remotes/origin/main", seed)
	// Add an unpushed local-only commit on main.
	if err := os.WriteFile(filepath.Join(root, "LOCAL-ONLY.txt"), []byte("unpushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", "-A")
	gitRun("-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "local-only")

	tg := Target{RepoRoot: root, FilePath: filepath.Join(root, "CLAUDE.md"),
		BranchName: "docs/agent-smith-origin-base", Base: "main"}
	wt, err := Open(tg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = Drop(root, wt) }()
	if _, err := os.Stat(filepath.Join(wt, "CLAUDE.md")); err != nil {
		t.Errorf("worktree missing seeded file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "LOCAL-ONLY.txt")); !os.IsNotExist(err) {
		t.Error("worktree contains the unpushed local-only commit; Open must base off origin/main")
	}
}
