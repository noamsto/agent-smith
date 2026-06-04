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
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if tg.RepoRoot != wantRoot {
		t.Errorf("RepoRoot = %q, want %q", tg.RepoRoot, wantRoot)
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

func TestIsImmutableStorePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/nix/store/abc-x/CLAUDE.md", true},
		{"/home/u/repo/CLAUDE.md", false},
		{"/nix/store", false},          // no trailing slash
		{"/nix/storeroom/x.md", false}, // not the store
		{"", false},
	}
	for _, c := range cases {
		if got := isImmutableStorePath(c.in); got != c.want {
			t.Errorf("isImmutableStorePath(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveRealPathSymlink(t *testing.T) {
	repo := initRepo(t, "https://github.com/x/y.git") // seeds CLAUDE.md
	link := filepath.Join(t.TempDir(), "link.md")
	if err := os.Symlink(filepath.Join(repo, "CLAUDE.md"), link); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRealPath(link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(repo, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("resolveRealPath(symlink) = %q, want %q", got, want)
	}
}

func TestResolveRealPathAddTarget(t *testing.T) {
	dir := t.TempDir() // exists; the file inside does not
	target := filepath.Join(dir, "NEW.md")
	got, err := resolveRealPath(target)
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(wantDir, "NEW.md") {
		t.Errorf("resolveRealPath(add-target) = %q, want %q", got, filepath.Join(wantDir, "NEW.md"))
	}
}

func TestResolveRealPathDeeplyMissing(t *testing.T) {
	dir := t.TempDir() // exists; a/b/c.md below it does not
	got, err := resolveRealPath(filepath.Join(dir, "a", "b", "c.md"))
	if err != nil {
		t.Fatal(err)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(wantDir, "a", "b", "c.md") {
		t.Errorf("resolveRealPath(deeply missing) = %q, want %q", got, filepath.Join(wantDir, "a", "b", "c.md"))
	}
}

func TestResolveRealPathBrokenLink(t *testing.T) {
	link := filepath.Join(t.TempDir(), "broken.md")
	if err := os.Symlink("/no/such/target-xyz", link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRealPath(link); err == nil {
		t.Error("expected error for a broken symlink")
	}
}

func TestResolveFollowsSymlink(t *testing.T) {
	repo := initRepo(t, "https://github.com/noamsto/nix-config.git")
	link := filepath.Join(t.TempDir(), "CLAUDE.md") // in a non-git dir, like ~/.claude
	if err := os.Symlink(filepath.Join(repo, "CLAUDE.md"), link); err != nil {
		t.Fatal(err)
	}
	tg, err := Resolve(analyst.Proposal{
		ID: "g", ImplicatedArtifact: link + "#reading-code", FixType: "strengthen",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if tg.RepoRoot != wantRoot {
		t.Errorf("RepoRoot = %q, want %q", tg.RepoRoot, wantRoot)
	}
	wantFile, err := filepath.EvalSymlinks(filepath.Join(repo, "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	if tg.FilePath != wantFile {
		t.Errorf("FilePath = %q, want %q", tg.FilePath, wantFile)
	}
	if tg.Owner != "nix-config" {
		t.Errorf("Owner = %q", tg.Owner)
	}
	if tg.Section != "reading-code" {
		t.Errorf("Section = %q", tg.Section)
	}
}

func TestResolveWorktreeToMain(t *testing.T) {
	main := initRepo(t, "https://github.com/noamsto/nix-config.git")
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := git(main, "worktree", "add", "-b", "wtb", wt); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	tg, err := Resolve(analyst.Proposal{
		ID: "g", ImplicatedArtifact: filepath.Join(wt, "CLAUDE.md"), FixType: "strengthen",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantMain, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	if tg.RepoRoot != wantMain {
		t.Errorf("RepoRoot = %q, want main %q (not the worktree)", tg.RepoRoot, wantMain)
	}
	if tg.FilePath != filepath.Join(wantMain, "CLAUDE.md") {
		t.Errorf("FilePath = %q, want %q", tg.FilePath, filepath.Join(wantMain, "CLAUDE.md"))
	}
	if tg.Owner != "nix-config" {
		t.Errorf("Owner = %q, want nix-config", tg.Owner)
	}
	if tg.Base != "main" {
		t.Errorf("Base = %q, want main", tg.Base)
	}
}

func TestMainRepoRoot(t *testing.T) {
	main := initRepo(t, "https://github.com/noamsto/nix-config.git")
	if got := mainRepoRoot(main); got != main {
		t.Errorf("mainRepoRoot(main checkout) = %q, want %q", got, main)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if out, err := git(main, "worktree", "add", "-b", "wtbranch", wt); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	got := mainRepoRoot(wt)
	wantMain, err := filepath.EvalSymlinks(main)
	if err != nil {
		t.Fatal(err)
	}
	gotEval, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	if gotEval != wantMain {
		t.Errorf("mainRepoRoot(worktree) = %q (eval %q), want main %q", got, gotEval, wantMain)
	}
}
