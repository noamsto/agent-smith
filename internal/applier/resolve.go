package applier

import (
	"errors"
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

// settingsFileRel is the hook-layer settings file inside the settings-owning
// repo. It is the `--settings` overlay that references /nix/store paths, so it
// cannot live in the implicated repo's worktree (see agents/editor.md's
// two-layer rule). The editor still picks the actual layer to edit within the
// repo; this is the file hint handed to it.
const settingsFileRel = "home/ai/claude-code/default.nix"

// splitArtifact splits an "implicated_artifact" into its file path and optional
// "#section" anchor (the anchor is informational — passed to the editor, not used
// to slice the file). Only the first '#' separates them.
func splitArtifact(s string) (path, section string) {
	if before, after, ok := strings.Cut(s, "#"); ok {
		return before, after
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

// resolveRealPath canonicalizes an artifact path through symlinks to its real
// on-disk location. For a not-yet-existing file (an `add` target), it resolves
// the deepest existing ancestor directory and rejoins the missing remainder, so a
// symlinked parent is still followed. A broken symlink returns an error.
func resolveRealPath(p string) (string, error) {
	if _, err := os.Lstat(p); err == nil {
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return "", fmt.Errorf("resolveRealPath %s: %w", p, err)
		}
		return real, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolveRealPath %s: %w", p, err)
	}
	dir := filepath.Dir(p)
	rest := filepath.Base(p)
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no existing ancestor for %s", p)
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolveRealPath %s: %w", p, err)
	}
	return filepath.Join(realDir, rest), nil
}

// isImmutableStorePath reports whether p is inside the read-only Nix store.
func isImmutableStorePath(p string) bool {
	return strings.HasPrefix(p, "/nix/store/")
}

// mainRepoRoot returns the canonical repo root for a worktree root: for a linked
// git worktree it is the main working tree that owns the shared .git; otherwise
// worktreeRoot is returned unchanged. Detected by comparing the worktree's git-dir
// to its git-common-dir (they differ only for a linked worktree). On any git
// error it returns worktreeRoot unchanged (intentional silent fallback).
func mainRepoRoot(worktreeRoot string) string {
	gd, err1 := git(worktreeRoot, "rev-parse", "--path-format=absolute", "--git-dir")
	cd, err2 := git(worktreeRoot, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err1 != nil || err2 != nil {
		return worktreeRoot
	}
	gitDir := strings.TrimSpace(string(gd))
	commonDir := strings.TrimSpace(string(cd))
	if gitDir == commonDir || commonDir == "" {
		return worktreeRoot
	}
	return filepath.Dir(commonDir)
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
// branch the applier will act on. It follows symlinks to the editable source and
// resolves linked worktrees to their canonical main repo.
func Resolve(p analyst.Proposal) (Target, error) {
	artifactPath, section := splitArtifact(p.ImplicatedArtifact)
	real, err := resolveRealPath(artifactPath)
	if err != nil {
		return Target{}, fmt.Errorf("resolve %s: %w", artifactPath, err)
	}
	if isImmutableStorePath(real) {
		return Target{}, fmt.Errorf("artifact resolves into the immutable nix store: %s", real)
	}
	worktreeRoot, err := repoRoot(real)
	if err != nil {
		return Target{}, fmt.Errorf("resolve %s: %w", artifactPath, err)
	}
	mainRoot := mainRepoRoot(worktreeRoot)
	file := real
	// mainRepoRoot returns worktreeRoot verbatim for a main checkout, so this
	// inequality fires only for a linked worktree (no path-normalization race).
	if mainRoot != worktreeRoot {
		rel, rerr := filepath.Rel(worktreeRoot, real)
		if rerr != nil {
			return Target{}, fmt.Errorf("remap %s from worktree to main repo: %w", real, rerr)
		}
		file = filepath.Join(mainRoot, rel)
	}
	return Target{
		RepoRoot:   mainRoot,
		FilePath:   file,
		Section:    section,
		Owner:      classifyOwner(mainRoot),
		BranchName: fmt.Sprintf("%s/agent-smith-%s", commitType(p.FixType), slug(p.ID)),
		Base:       defaultBranch(mainRoot),
	}, nil
}

// ResolveEscalation routes an `escalate-out-of-instructions` proposal to the
// settings-owning repo instead of the implicated repo: the proposed hook/
// permission/default lands in the settings layer, which lives in settingsRepo,
// not in the repo whose CLAUDE.md surfaced the glitch. settingsRepo is the repo
// root that owns home/ai/claude-code/ and settings.json. It must be a git repo;
// callers that lack a configured settings repo skip the proposal rather than
// calling this.
func ResolveEscalation(p analyst.Proposal, settingsRepo string) (Target, error) {
	root, err := repoRoot(settingsRepo)
	if err != nil {
		return Target{}, fmt.Errorf("settings repo %s: %w", settingsRepo, err)
	}
	mainRoot := mainRepoRoot(root)
	return Target{
		RepoRoot:   mainRoot,
		FilePath:   filepath.Join(mainRoot, settingsFileRel),
		Owner:      classifyOwner(mainRoot),
		BranchName: fmt.Sprintf("%s/agent-smith-%s", commitType(p.FixType), slug(p.ID)),
		Base:       defaultBranch(mainRoot),
	}, nil
}
