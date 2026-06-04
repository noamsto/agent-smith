package applier

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Open creates a fresh git worktree of t.RepoRoot on a new branch (t.BranchName
// off t.Base) in a temp directory, returning the worktree path. The live checkout
// is never touched. When a remote-tracking origin/<base> exists, the branch starts
// there — branching from the local base tip would leak unpushed local commits
// into the PR.
func Open(t Target) (string, error) {
	wt, err := os.MkdirTemp("", "agent-smith-wt-")
	if err != nil {
		return "", err
	}
	start := t.Base
	if _, err := git(t.RepoRoot, "rev-parse", "--verify", "--quiet",
		"refs/remotes/origin/"+t.Base); err == nil {
		start = "refs/remotes/origin/" + t.Base
	}
	out, err := git(t.RepoRoot, "worktree", "add", wt, "-b", t.BranchName, start)
	if err != nil {
		os.RemoveAll(wt)
		return "", fmt.Errorf("git worktree add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return wt, nil
}

// Drop removes a worktree created by Open (force: it may hold uncommitted edits
// when the editor declined late).
func Drop(repoRoot, wt string) error {
	out, err := git(repoRoot, "worktree", "remove", "--force", wt)
	if err != nil {
		return fmt.Errorf("git worktree remove: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// WorktreeFile maps the target's repo-relative artifact path into the worktree —
// the path the editor subagent must edit. If the artifact is not under
// t.RepoRoot (Rel errors or escapes with ".."), it returns t.FilePath unchanged.
func WorktreeFile(t Target, wt string) string {
	rel, err := filepath.Rel(t.RepoRoot, t.FilePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return t.FilePath
	}
	return filepath.Join(wt, rel)
}
