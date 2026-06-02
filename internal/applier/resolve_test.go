package applier

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/noamsto/agent-smith/internal/analyst"
)

func TestSplitArtifact(t *testing.T) {
	cases := []struct{ in, wantPath, wantSection string }{
		{"/g/CLAUDE.md#reading-code", "/g/CLAUDE.md", "reading-code"},
		{"/g/CLAUDE.md", "/g/CLAUDE.md", ""},
		{"/a/b.md#x#y", "/a/b.md", "x#y"},
	}
	for _, c := range cases {
		p, s := splitArtifact(c.in)
		if p != c.wantPath || s != c.wantSection {
			t.Errorf("splitArtifact(%q) = (%q,%q), want (%q,%q)", c.in, p, s, c.wantPath, c.wantSection)
		}
	}
}

func TestCommitType(t *testing.T) {
	if commitType("escalate-out-of-instructions") != "chore" {
		t.Error("escalate should map to chore")
	}
	for _, ft := range []string{"add", "strengthen", "fix-stale", "remove"} {
		if commitType(ft) != "docs" {
			t.Errorf("%s should map to docs", ft)
		}
	}
}

func TestSlug(t *testing.T) {
	if got := slug("glitch-2026-06-01-Skeleton Reads!"); got != "glitch-2026-06-01-skeleton-reads" {
		t.Errorf("slug = %q", got)
	}
}

// initRepo makes a temp git repo with a remote URL and one commit on `main`,
// returning the repo root.
func initRepo(t *testing.T, originURL string) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		c := exec.Command("git", args...)
		c.Dir = root
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	run("remote", "add", "origin", originURL)
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("# rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-m", "seed")
	return root
}

func TestResolve(t *testing.T) {
	root := initRepo(t, "https://github.com/noamsto/nix-config.git")
	p := analyst.Proposal{
		ID:                 "glitch-2026-06-01-skeleton-reads",
		ImplicatedArtifact: filepath.Join(root, "CLAUDE.md") + "#reading-code",
		FixType:            "strengthen",
	}
	tg, err := Resolve(p)
	if err != nil {
		t.Fatal(err)
	}
	if tg.RepoRoot != root {
		t.Errorf("RepoRoot = %q, want %q", tg.RepoRoot, root)
	}
	if tg.Section != "reading-code" {
		t.Errorf("Section = %q", tg.Section)
	}
	if tg.Owner != "nix-config" {
		t.Errorf("Owner = %q", tg.Owner)
	}
	if tg.BranchName != "docs/agent-smith-glitch-2026-06-01-skeleton-reads" {
		t.Errorf("BranchName = %q", tg.BranchName)
	}
}

func TestResolveUnresolved(t *testing.T) {
	dir := t.TempDir() // not a git repo
	p := analyst.Proposal{ID: "x", ImplicatedArtifact: filepath.Join(dir, "missing", "C.md")}
	if _, err := Resolve(p); err == nil {
		t.Error("expected error for artifact outside any git repo")
	}
}
