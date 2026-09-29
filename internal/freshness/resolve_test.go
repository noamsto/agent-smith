package freshness

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// realTempDir is t.TempDir with symlinks resolved, so expected paths match the
// symlink-resolved directories the scanner computes.
func realTempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// isolateGit points HOME and XDG_CONFIG_HOME at a fresh dir and disables the
// system config, so a developer's global excludes can't change a verdict.
func isolateGit(t *testing.T) string {
	t.Helper()
	home := realTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return home
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil { //nolint:gosec // test helper running a fixed binary
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

func TestBases(t *testing.T) {
	a := artifact{Path: "/n/CLAUDE.md", Real: "/r/sub/CLAUDE.md", Dir: "/n", RealDir: "/r/sub", Root: "/r"}
	noRoot := artifact{Path: "/n/CLAUDE.md", Real: "/n/CLAUDE.md", Dir: "/n", RealDir: "/n"}
	s := &scanner{roots: []string{"/r/"}, home: "/h"}
	noHome := &scanner{roots: []string{"/r/"}}

	tests := []struct {
		name     string
		s        *scanner
		a        artifact
		form     Form
		path     string
		want     []string
		wantSkip bool
	}{
		{"import relative: nominal then resolved dir", s, a, FormImport, "x.md", []string{"/n/x.md", "/r/sub/x.md"}, false},
		{"import absolute outside every root is not skipped", s, a, FormImport, "/abs/x.md", []string{"/abs/x.md"}, false},
		{"import absolute into in-repo worktree", s, a, FormImport, "/r/.worktrees/w/x.md", []string{"/r/.worktrees/w/x.md", "/r/x.md"}, false},
		{"import ~ expands to home", s, a, FormImport, "~/x.md", []string{"/h/x.md"}, false},
		{"import ~ with no home is skipped", noHome, a, FormImport, "~/x.md", nil, true},
		{"link relative: nominal then resolved dir", s, a, FormLink, "docs/a.md", []string{"/n/docs/a.md", "/r/sub/docs/a.md"}, false},
		{"link leading slash outside every root: root-relative first", s, a, FormLink, "/docs/a.md", []string{"/r/docs/a.md", "/docs/a.md"}, false},
		{"link absolute under a root: as written first", s, a, FormLink, "/r/docs/a.md", []string{"/r/docs/a.md", "/r/r/docs/a.md"}, false},
		{"link absolute into in-repo worktree", s, a, FormLink, "/r/.worktrees/w/a.md", []string{"/r/.worktrees/w/a.md", "/r/a.md", "/r/r/.worktrees/w/a.md"}, false},
		{"link leading slash with no root is skipped", s, noRoot, FormLink, "/docs/a.md", nil, true},
		{"link absolute under a root with no artifact root", s, noRoot, FormLink, "/r/docs/a.md", []string{"/r/docs/a.md"}, false},
		{"link ~ expands to home as-is", s, a, FormLink, "~/a.md", []string{"/h/a.md"}, false},
		{"backtick absolute under a root", s, a, FormBacktick, "/r/x/y.go", []string{"/r/x/y.go"}, false},
		{"backtick absolute into in-repo worktree", s, a, FormBacktick, "/r/.worktrees/w/x/y.go", []string{"/r/.worktrees/w/x/y.go", "/r/x/y.go"}, false},
		{"backtick absolute into sibling worktree", s, a, FormBacktick, "/r-worktrees/w/x.go", []string{"/r-worktrees/w/x.go", "/r/x.go"}, false},
		{"backtick absolute outside every root is skipped", s, a, FormBacktick, "/elsewhere/x.go", nil, true},
		{"backtick ~ outside every root is skipped", s, a, FormBacktick, "~/.config/x.go", nil, true},
		{"backtick relative with no root is skipped", s, noRoot, FormBacktick, "docs/x.go", nil, true},
		{"backtick ./ : resolved dir, nominal dir, root", s, a, FormBacktick, "./x.go", []string{"/r/sub/x.go", "/n/x.go", "/r/x.go"}, false},
		{"backtick ../ dedups identical bases", s, a, FormBacktick, "../x.go", []string{"/r/x.go", "/x.go"}, false},
		{"backtick relative: root, resolved dir, nominal dir", s, a, FormBacktick, "internal/x.go", []string{"/r/internal/x.go", "/r/sub/internal/x.go", "/n/internal/x.go"}, false},
		{"backtick trailing slash is cleaned", s, a, FormBacktick, "internal/", []string{"/r/internal", "/r/sub/internal", "/n/internal"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, skip := tt.s.bases(Candidate{Form: tt.form, Path: tt.path}, tt.a)
			if skip != tt.wantSkip || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("bases(%s %q) = %q, skip=%v; want %q, skip=%v", tt.form, tt.path, got, skip, tt.want, tt.wantSkip)
			}
		})
	}
}

func TestUnderRoot(t *testing.T) {
	s := &scanner{roots: []string{"/r/", "/r/nested/", "/other/"}}
	tests := []struct{ p, want string }{
		{"/r/x.go", "/r"},
		{"/r", "/r"},
		{"/r/nested/x.go", "/r/nested"},
		{"/r-tools/x.go", ""},
		{"/elsewhere", ""},
	}
	for _, tt := range tests {
		if got := s.underRoot(tt.p); got != tt.want {
			t.Errorf("underRoot(%q) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestSymbolTrims(t *testing.T) {
	tests := []struct {
		p    string
		want []string
	}{
		{"internal/analyst.ClusterDB", []string{"internal/analyst"}},
		{"cluster.go:ClusterDB", []string{"cluster.go"}},
		{"f.md#sec", []string{"f.md"}},
		{"pkg.Type.Method", []string{"pkg.Type", "pkg"}},
		{"docs/plan.md", nil},
		{"docs/plan.MD", nil},
		{"cfg/.envrc", nil},
		{"docs/", nil},
	}
	for _, tt := range tests {
		if got := symbolTrims(tt.p); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("symbolTrims(%q) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

// TestAbsoluteRefs drives bases/live/downgrade directly: t.TempDir may sit under
// /tmp, which Classify skips, so the absolute forms can't go through Scan here.
func TestAbsoluteRefs(t *testing.T) {
	isolateGit(t)
	repo := realTempDir(t)
	gitInit(t, repo)
	writeFile(t, filepath.Join(repo, "x.go"), "")
	writeFile(t, filepath.Join(repo, "docs", "guide.md"), "")
	writeFile(t, filepath.Join(repo, "CLAUDE.md"), "")

	a := newArtifact(filepath.Join(repo, "CLAUDE.md"), filepath.Join(repo, "CLAUDE.md"))
	if a.Root != repo {
		t.Fatalf("Root = %q, want %q", a.Root, repo)
	}

	tests := []struct {
		name string
		path string
		want string // "live", "dead", "ambiguous"
	}{
		{"worktree copy missing, main checkout present", repo + "/.worktrees/w/x.go", "live"},
		{"sibling worktree missing, main checkout present", repo + "-worktrees/w/x.go", "live"},
		{"present under root", repo + "/docs/guide.md", "live"},
		{"missing under an existing first segment", repo + "/docs/gone.md", "dead"},
		{"missing with no first segment present", repo + "/nowhere/x.go", "ambiguous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScanner("", true)
			s.roots = []string{repo + "/"}
			c := Candidate{Form: FormBacktick, Path: tt.path}
			bs, skip := s.bases(c, a)
			if skip {
				t.Fatalf("bases(%q) skipped", tt.path)
			}
			got := "dead"
			switch {
			case s.live(c, a, bs):
				got = "live"
			case s.downgrade(c, a, bs):
				got = "ambiguous"
			}
			if got != tt.want {
				t.Fatalf("%q = %s, want %s", tt.path, got, tt.want)
			}
		})
	}
}

func TestRepoIndexSkipsVendoredDirs(t *testing.T) {
	root := realTempDir(t)
	for _, p := range []string{"a/b/conn.go", "node_modules/m/conn.go", "vendor/v/conn.go", ".worktrees/w/conn.go", "sub/dist/conn.go"} {
		writeFile(t, filepath.Join(root, p), "")
	}
	s := newScanner("", true)
	if got, want := s.sameName(root, filepath.Join(root, "gone", "conn.go")), []string{"a/b/conn.go"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sameName = %q, want %q", got, want)
	}
	if got := s.sameName("", "/x/conn.go"); got == nil || len(got) != 0 {
		t.Fatalf("sameName with no root = %#v, want empty non-nil", got)
	}
}

func TestSameNameCapsAndExcludesSelf(t *testing.T) {
	root := realTempDir(t)
	for _, d := range []string{"a", "b", "c", "d", "e", "f", "self"} {
		writeFile(t, filepath.Join(root, d, "x.go"), "")
	}
	got := newScanner("", true).sameName(root, filepath.Join(root, "self", "x.go"))
	want := []string{"a/x.go", "b/x.go", "c/x.go", "d/x.go", "e/x.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sameName = %q, want %q", got, want)
	}
}

// TestAbsoluteLinks drives bases/live/downgrade directly for the same /tmp
// reason as TestAbsoluteRefs.
func TestAbsoluteLinks(t *testing.T) {
	isolateGit(t)
	repo := realTempDir(t)
	gitInit(t, repo)
	writeFile(t, filepath.Join(repo, "docs", "guide.md"), "")
	a := newArtifact(filepath.Join(repo, "CLAUDE.md"), filepath.Join(repo, "CLAUDE.md"))
	outside := realTempDir(t)
	noRoot := newArtifact(filepath.Join(outside, "CLAUDE.md"), filepath.Join(outside, "CLAUDE.md"))

	tests := []struct {
		name string
		a    artifact
		path string
		want string // "live", "dead", "ambiguous", "skip"
	}{
		{"absolute link to a live file", a, repo + "/docs/guide.md", "live"},
		{"absolute link into a worktree copy", a, repo + "/.worktrees/w/docs/guide.md", "live"},
		{"absolute link to a missing file", a, repo + "/docs/gone.md", "dead"},
		{"repo-root-relative link", a, "/docs/guide.md", "live"},
		{"no root, absolute link under a known root", noRoot, repo + "/docs/guide.md", "live"},
		{"no root, absolute link outside every root", noRoot, "/elsewhere/x.md", "skip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newScanner("", true)
			s.roots = []string{repo + "/"}
			c := Candidate{Form: FormLink, Path: tt.path}
			bs, skip := s.bases(c, tt.a)
			got := "dead"
			switch {
			case skip:
				got = "skip"
			case s.live(c, tt.a, bs):
				got = "live"
			case s.downgrade(c, tt.a, bs):
				got = "ambiguous"
			}
			if got != tt.want {
				t.Fatalf("%q = %s (bases %q), want %s", tt.path, got, bs, tt.want)
			}
		})
	}
}

// lockDir makes dir unreadable for the rest of the test, restoring it first
// so t.TempDir's cleanup can remove it.
func lockDir(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) //nolint:gosec // restoring a test directory's mode
}

func TestExistsFailsTowardLive(t *testing.T) {
	dir := realTempDir(t)
	writeFile(t, filepath.Join(dir, "locked", "f.go"), "")
	writeFile(t, filepath.Join(dir, "file.go"), "")
	lockDir(t, filepath.Join(dir, "locked"))

	tests := []struct {
		p    string
		want bool
	}{
		{filepath.Join(dir, "file.go"), true},
		{filepath.Join(dir, "locked", "f.go"), true},
		{filepath.Join(dir, "locked", "unknowable.go"), true},
		{filepath.Join(dir, "missing.go"), false},
		{filepath.Join(dir, "file.go", "child.go"), false},
	}
	for _, tt := range tests {
		if got := exists(tt.p); got != tt.want {
			t.Errorf("exists(%q) = %v, want %v", tt.p, got, tt.want)
		}
	}
}

func TestRepoIndexIncompleteIsLive(t *testing.T) {
	root := realTempDir(t)
	writeFile(t, filepath.Join(root, "a", "x.go"), "")
	writeFile(t, filepath.Join(root, "locked", "x.go"), "")
	lockDir(t, filepath.Join(root, "locked"))

	s := newScanner("", true)
	if !s.repoIndex(root).hasSuffix("nowhere/x.go") {
		t.Errorf("hasSuffix on an incomplete index = false, want true (absence unprovable)")
	}
	if got, want := s.sameName(root, filepath.Join(root, "gone", "x.go")), []string{"a/x.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("sameName = %q, want %q (what the walk did reach)", got, want)
	}
}

func TestRepoIndexSaturated(t *testing.T) {
	root := realTempDir(t)
	for _, d := range []string{"a", "b", "c", "d"} {
		writeFile(t, filepath.Join(root, d, "x.go"), "")
	}
	defer func(n int) { maxIndexEntries = n }(maxIndexEntries)
	maxIndexEntries = 3

	s := newScanner("", true)
	if !s.repoIndex(root).hasSuffix("nowhere/x.go") {
		t.Errorf("hasSuffix on a saturated index = false, want true")
	}
	if got := s.sameName(root, filepath.Join(root, "gone", "x.go")); got == nil || len(got) != 0 {
		t.Errorf("sameName on a saturated index = %#v, want empty non-nil", got)
	}
}
