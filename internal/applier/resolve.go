package applier

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// Target is the resolved destination for one proposal's edit.
type Target struct {
	RepoRoot   string `json:"repo_root"`
	FilePath   string `json:"file_path"`
	Section    string `json:"section"`
	Owner      string `json:"owner"`
	BranchName string `json:"branch_name"`
	Base       string `json:"base"`
}

// splitArtifact splits an "implicated_artifact" into its file path and optional
// "#section" anchor (the anchor is informational — passed to the editor, not used
// to slice the file). Only the first '#' separates them.
func splitArtifact(s string) (path, section string) {
	if i := strings.IndexByte(s, '#'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// commitType maps a fix_type to a conventional-commit type, used for both the
// branch name and the commit subject. Prose edits are docs; a hook/permission
// (escalate) is chore.
func commitType(fixType string) string {
	if fixType == "escalate-out-of-instructions" {
		return "chore"
	}
	return "docs"
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func git(dir string, args ...string) ([]byte, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	return c.CombinedOutput()
}

// repoRoot finds the git toplevel owning path. For a not-yet-existing file
// (an `add` target), it walks up to the deepest existing directory first.
func repoRoot(path string) (string, error) {
	dir := path
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		dir = filepath.Dir(path)
	}
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing parent dir for %s", path)
		}
		dir = parent
	}
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not in a git repo (%s): %s", strings.TrimSpace(string(out)), path)
	}
	return strings.TrimSpace(string(out)), nil
}

func classifyOwner(root string) string {
	out, _ := git(root, "remote", "get-url", "origin")
	url := strings.TrimSpace(string(out))
	switch {
	case strings.Contains(url, "nix-config"):
		return "nix-config"
	case strings.Contains(url, "factify"):
		return "factify-inc"
	default:
		return "personal"
	}
}

func defaultBranch(root string) string {
	out, err := git(root, "symbolic-ref", "refs/remotes/origin/HEAD")
	if err == nil {
		return path.Base(strings.TrimSpace(string(out)))
	}
	return "main"
}

// Resolve maps a proposal's implicated_artifact to the repo, file, owner, and
// branch the applier will act on.
func Resolve(p analyst.Proposal) (Target, error) {
	path, section := splitArtifact(p.ImplicatedArtifact)
	root, err := repoRoot(path)
	if err != nil {
		return Target{}, err
	}
	return Target{
		RepoRoot:   root,
		FilePath:   path,
		Section:    section,
		Owner:      classifyOwner(root),
		BranchName: fmt.Sprintf("%s/agent-smith-%s", commitType(p.FixType), slug(p.ID)),
		Base:       defaultBranch(root),
	}, nil
}
