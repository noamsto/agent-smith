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
	// A prior failed submit can leave an orphan branch with no commits of its
	// own, which would make `worktree add -b` fail and block the retry. Reset
	// such a branch onto start (-B) instead; refuse to touch one that carries
	// work, since that would silently discard it.
	newBranch := "-b"
	if branchExists(t.RepoRoot, t.BranchName) {
		if !branchEmpty(t.RepoRoot, t.BranchName, start) {
			os.RemoveAll(wt)
			return "", fmt.Errorf("branch %s already exists with commits; refusing to reset", t.BranchName)
		}
		newBranch = "-B"
	}
	out, err := git(t.RepoRoot, "worktree", "add", wt, newBranch, t.BranchName, start)
	if err != nil {
		os.RemoveAll(wt)
		return "", fmt.Errorf("git worktree add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return wt, nil
}

// branchExists reports whether a local branch of the given name exists.
func branchExists(repoRoot, branch string) bool {
	_, err := git(repoRoot, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// branchEmpty reports whether branch has no commits of its own over start —
// the signature of an orphan branch left by a failed run, safe to reset.
func branchEmpty(repoRoot, branch, start string) bool {
	out, err := git(repoRoot, "rev-list", "--count", start+".."+branch)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "0"
}

// CleanupAfterSubmit drops the worktree only when submit succeeded (or was a
// clean no-op). On failure the worktree holds the applied-but-uncommitted edit,
// so it is preserved for the orchestrator to retry from. Returns whether the
// worktree was dropped.
func CleanupAfterSubmit(repoRoot, wt string, submitErr error) (dropped bool, err error) {
	if submitErr != nil {
		return false, nil
	}
	return true, Drop(repoRoot, wt)
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
