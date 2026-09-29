package freshness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// fixtureRepo builds a git repo exercising every liveness rule.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	repo := realTempDir(t)
	gitInit(t, repo)
	for _, p := range []string{
		".gitignore",
		"a/b/daemon/conn.go",
		"internal/analyst/cluster.go",
		"pkg/f.go",
		"docs/f.md",
		"docs/guide.md",
		"docs/plan/step.md",
		"my file.md",
		"cfg/a.txt",
		"build/keep.txt",
		".claude/settings.json",
		"sub/local/thing.go",
		"internal/mover.go",
		"vendor/mover.go",
	} {
		writeFile(t, filepath.Join(repo, p), "")
	}
	writeFile(t, filepath.Join(repo, ".gitignore"), "build/\n.claude/settings.local.json\n")
	return repo
}

func bucket(r Report, path string) string {
	for _, ref := range r.Dead {
		if ref.Path == path {
			return "dead"
		}
	}
	for _, ref := range r.AmbiguousMissing {
		if ref.Path == path {
			return "ambiguous"
		}
	}
	return ""
}

func TestScanVerdicts(t *testing.T) {
	home := isolateGit(t)
	writeFile(t, filepath.Join(home, "present.md"), "")
	repo := fixtureRepo(t)

	tests := []struct {
		name     string
		artifact string // repo-relative
		line     string
		path     string
		noGit    bool
		want     string // "" = not reported
	}{
		{"backtick present under root", "CLAUDE.md", "See `internal/analyst/cluster.go`.", "internal/analyst/cluster.go", false, ""},
		{"backtick resolves from the artifact's dir", "sub/AGENTS.md", "See `local/thing.go`.", "local/thing.go", false, ""},
		{"suffix liveness", "CLAUDE.md", "See `daemon/conn.go`.", "daemon/conn.go", false, ""},
		{"symbol liveness .Ident after a dir", "CLAUDE.md", "See `internal/analyst.Foo`.", "internal/analyst.Foo", false, ""},
		{"symbol liveness :Ident after a file", "CLAUDE.md", "See `pkg/f.go:Foo`.", "pkg/f.go:Foo", false, ""},
		{"symbol liveness #anchor after a file", "CLAUDE.md", "See `docs/f.md#sec`.", "docs/f.md#sec", false, ""},
		{"known extension is not a symbol", "CLAUDE.md", "See `docs/plan.md`.", "docs/plan.md", false, "dead"},
		{"dotfile is not a symbol", "CLAUDE.md", "See `cfg/.envrc`.", "cfg/.envrc", false, "dead"},
		{"first segment missing downgrades", "CLAUDE.md", "See `nowhere/x.go`.", "nowhere/x.go", false, "ambiguous"},
		{"first segment present stays dead", "CLAUDE.md", "See `docs/x.go`.", "docs/x.go", false, "dead"},
		{"gitignored dir downgrades", "CLAUDE.md", "Write `build/out.go`.", "build/out.go", false, "ambiguous"},
		{"gitignored file downgrades", "CLAUDE.md", "Create `.claude/settings.local.json`.", ".claude/settings.local.json", false, "ambiguous"},
		{"no git downgrades", "CLAUDE.md", "See `docs/x.go`.", "docs/x.go", true, "ambiguous"},
		{"statically ambiguous stays ambiguous", "CLAUDE.md", "Run `cmd/tool`.", "cmd/tool", false, "ambiguous"},
		{"link relative to the artifact", "sub/AGENTS.md", "[g](../docs/guide.md)", "../docs/guide.md", false, ""},
		{"link does not resolve from root", "sub/AGENTS.md", "[g](docs/guide.md)", "docs/guide.md", false, "dead"},
		{"link does not get suffix liveness", "CLAUDE.md", "[c](daemon/conn.go)", "daemon/conn.go", false, "dead"},
		{"link leading slash is root-relative", "sub/AGENTS.md", "[g](/docs/guide.md)", "/docs/guide.md", false, ""},
		{"link leading slash missing", "sub/AGENTS.md", "[g](/docs/gone.md)", "/docs/gone.md", false, "dead"},
		{"link percent-encoding", "CLAUDE.md", "[m](my%20file.md)", "my%20file.md", false, ""},
		{"import relative present", "sub/AGENTS.md", "@../docs/guide.md", "../docs/guide.md", false, ""},
		{"import relative missing", "CLAUDE.md", "@gone.md", "gone.md", false, "dead"},
		{"import ~ present", "CLAUDE.md", "@~/present.md", "~/present.md", false, ""},
		{"import ~ missing outside every root stays dead", "CLAUDE.md", "@~/missing.md", "~/missing.md", false, "dead"},
		{"backtick ~ outside every root is skipped", "CLAUDE.md", "See `~/missing/x.go`.", "~/missing/x.go", false, ""},
		{"URL is skipped", "CLAUDE.md", "[u](https://example.com/x.md)", "https://example.com/x.md", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			art := filepath.Join(repo, tt.artifact)
			writeFile(t, art, tt.line+"\n")
			t.Cleanup(func() { _ = os.Remove(art) })

			r := newScanner(home, !tt.noGit).scan([]string{art}, Options{})
			if !slices.Contains(r.Scanned, art) {
				t.Fatalf("Scanned = %q, want it to include %q", r.Scanned, art)
			}
			if got := bucket(r, tt.path); got != tt.want {
				t.Fatalf("%q: got %q, want %q (report %+v)", tt.path, got, tt.want, r)
			}
		})
	}
}

func TestScanNoRootSkipsRelativeBackticks(t *testing.T) {
	dir := realTempDir(t)
	art := filepath.Join(dir, "CLAUDE.md")
	writeFile(t, art, "See `docs/x.go` and [l](docs/y.md).\n")

	r := newScanner("", true).scan([]string{art}, Options{})
	if bucket(r, "docs/x.go") != "" {
		t.Fatalf("relative backtick with no root was reported: %+v", r)
	}
	if bucket(r, "docs/y.md") != "dead" {
		t.Fatalf("relative link with no root should still resolve file-relative: %+v", r)
	}
}

func TestScanRefFields(t *testing.T) {
	isolateGit(t)
	repo := fixtureRepo(t)
	art := filepath.Join(repo, "CLAUDE.md")
	long := strings.Repeat("é", 200)
	writeFile(t, art, "intro\nMoved: `docs/old/mover.go` "+long+"\n")

	r := newScanner("", true).scan([]string{art}, Options{})
	if len(r.Dead) != 1 {
		t.Fatalf("Dead = %+v, want one ref", r.Dead)
	}
	got := r.Dead[0]
	if got.ID != refID(art, 2, "docs/old/mover.go") || len(got.ID) != 12 {
		t.Errorf("ID = %q", got.ID)
	}
	if got.Artifact != art || got.Form != "backtick" || got.Line != 2 {
		t.Errorf("ref = %+v", got)
	}
	if got.ResolvedTo != filepath.Join(repo, "docs/old/mover.go") {
		t.Errorf("ResolvedTo = %q", got.ResolvedTo)
	}
	if !reflect.DeepEqual(got.SameName, []string{"internal/mover.go"}) {
		t.Errorf("SameName = %q, want [internal/mover.go] (vendor/ is not walked)", got.SameName)
	}
	if len(got.RuleExcerpt) > maxRuleExcerptBytes || !strings.HasPrefix(got.RuleExcerpt, "Moved: ") || !json.Valid([]byte(`"`+got.RuleExcerpt+`"`)) {
		t.Errorf("RuleExcerpt = %q (%d bytes)", got.RuleExcerpt, len(got.RuleExcerpt))
	}
}

func TestScanSameLineFormsCollapse(t *testing.T) {
	isolateGit(t)
	repo := fixtureRepo(t)

	dead := filepath.Join(repo, "CLAUDE.md")
	writeFile(t, dead, "[`docs/gone.md`](docs/gone.md)\n")
	r := newScanner("", true).scan([]string{dead}, Options{})
	if len(r.Dead) != 1 || len(r.AmbiguousMissing) != 0 {
		t.Fatalf("want one collapsed dead ref, got %+v", r)
	}

	// The backtick resolves from the root (live); the link, from sub/, would not.
	live := filepath.Join(repo, "sub", "AGENTS.md")
	writeFile(t, live, "[`docs/guide.md`](docs/guide.md)\n")
	r = newScanner("", true).scan([]string{live}, Options{})
	if len(r.Dead)+len(r.AmbiguousMissing) != 0 {
		t.Fatalf("a live form must suppress the other, got %+v", r)
	}
}

func TestScanSymlinkedArtifact(t *testing.T) {
	isolateGit(t)
	repo := fixtureRepo(t)
	target := filepath.Join(repo, "sub", "AGENTS.md")
	writeFile(t, target, "See `internal/analyst/cluster.go`, [g](../docs/guide.md), `docs/gone.go`.\n")

	links := realTempDir(t)
	link := filepath.Join(links, "CLAUDE.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	r := newScanner("", true).scan([]string{link}, Options{})
	if !reflect.DeepEqual(r.Scanned, []string{link}) {
		t.Fatalf("Scanned = %q, want the nominal symlink path", r.Scanned)
	}
	if len(r.Dead) != 1 || r.Dead[0].Path != "docs/gone.go" || r.Dead[0].Artifact != link {
		t.Fatalf("Dead = %+v, want only docs/gone.go attributed to %s", r.Dead, link)
	}
	if len(r.AmbiguousMissing) != 0 {
		t.Fatalf("AmbiguousMissing = %+v", r.AmbiguousMissing)
	}
}

func TestScanImportExpansion(t *testing.T) {
	isolateGit(t)
	repo := realTempDir(t)
	gitInit(t, repo)
	a := filepath.Join(repo, "A.md")
	b := filepath.Join(repo, "B.md")
	writeFile(t, a, "@B.md\n@docs/\n")
	writeFile(t, b, "@A.md\nSee `docs/gone.go`.\n")
	writeFile(t, filepath.Join(repo, "docs", "keep.md"), "")
	dup := filepath.Join(repo, "C.md")
	if err := os.Symlink(a, dup); err != nil {
		t.Fatal(err)
	}

	r := newScanner("", true).scan([]string{dup, a}, Options{})
	if want := []string{a, b}; !reflect.DeepEqual(r.Scanned, want) {
		t.Fatalf("Scanned = %q, want %q (cycle broken, symlink dup and directory import not scanned)", r.Scanned, want)
	}
	if len(r.Dead) != 1 || r.Dead[0].Artifact != b {
		t.Fatalf("Dead = %+v, want one ref from the imported B.md", r.Dead)
	}
}

func TestScanSkipped(t *testing.T) {
	dir := realTempDir(t)
	missing := filepath.Join(dir, "missing.md")
	unreadable := filepath.Join(dir, "adir")
	if err := os.Mkdir(unreadable, 0o755); err != nil { // test fixture
		t.Fatal(err)
	}

	r := newScanner("", true).scan([]string{unreadable, missing, missing}, Options{})
	if len(r.Scanned) != 0 || len(r.Skipped) != 2 {
		t.Fatalf("report = %+v, want two skipped", r)
	}
	if r.Skipped[0] != (Skipped{Artifact: unreadable, Reason: r.Skipped[0].Reason}) || !strings.HasPrefix(r.Skipped[0].Reason, "unreadable: ") {
		t.Errorf("Skipped[0] = %+v, want unreadable %s", r.Skipped[0], unreadable)
	}
	if r.Skipped[1] != (Skipped{Artifact: missing, Reason: "missing"}) {
		t.Errorf("Skipped[1] = %+v, want missing %s", r.Skipped[1], missing)
	}
}

func TestScanPrefixFilter(t *testing.T) {
	isolateGit(t)
	parent := realTempDir(t)
	r1 := filepath.Join(parent, "r1")
	r2 := filepath.Join(parent, "r2")
	for _, r := range []string{r1, r2} {
		gitInit(t, r)
		writeFile(t, filepath.Join(r, "docs", "keep.md"), "")
		writeFile(t, filepath.Join(r, "CLAUDE.md"), "See `docs/gone.go`.\n")
	}
	paths := []string{filepath.Join(r1, "CLAUDE.md"), filepath.Join(r2, "CLAUDE.md"), filepath.Join(r2, "missing.md")}

	for _, prefix := range []string{r1, filepath.Join(r1, ".worktrees", "feat")} {
		t.Run(prefix, func(t *testing.T) {
			r := newScanner("", true).scan(paths, Options{Prefix: prefix})
			if !reflect.DeepEqual(r.Scanned, []string{filepath.Join(r1, "CLAUDE.md")}) || len(r.Skipped) != 0 {
				t.Fatalf("report = %+v, want only r1", r)
			}
			if len(r.Dead) != 1 || r.Dead[0].Artifact != filepath.Join(r1, "CLAUDE.md") {
				t.Fatalf("Dead = %+v", r.Dead)
			}
		})
	}
}

func TestReportJSON(t *testing.T) {
	r := newScanner("", true).scan(nil, Options{})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"scanned":[],"skipped":[],"dead":[],"ambiguous_missing":[]}`; string(b) != want {
		t.Fatalf("empty report = %s, want %s", b, want)
	}

	path := filepath.Join(t.TempDir(), "freshness.json")
	in := Report{
		Scanned:          []string{"/r/CLAUDE.md"},
		Skipped:          []Skipped{{Artifact: "/r/x.md", Reason: "missing"}},
		Dead:             []Ref{{ID: "abc", Artifact: "/r/CLAUDE.md", Form: "backtick", Path: "a/b.go", Line: 3, SameName: []string{}}},
		AmbiguousMissing: []Ref{},
	}
	if err := WriteReport(in, path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path) // path is under the test temp dir
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "}\n") || !strings.Contains(string(raw), `"same_name": []`) {
		t.Fatalf("written report:\n%s", raw)
	}
	out, err := ReadReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("ReadReport = %+v, want %+v", out, in)
	}
}

func TestCapBytes(t *testing.T) {
	tests := []struct {
		s    string
		n    int
		want string
	}{
		{"short", 10, "short"},
		{"abcdef", 3, "abc"},
		{"aé", 2, "a"},
	}
	for _, tt := range tests {
		if got := capBytes(tt.s, tt.n); got != tt.want {
			t.Errorf("capBytes(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
		}
	}
}

// TestScanAbsoluteLinkEndToEnd runs Scan on an absolute link, so its temp
// dirs must live outside /tmp, which Classify skips.
func TestScanAbsoluteLinkEndToEnd(t *testing.T) {
	if st, err := os.Stat("/var/tmp"); err != nil || !st.IsDir() {
		t.Skip("/var/tmp unavailable")
	}
	t.Setenv("TMPDIR", "/var/tmp")
	isolateGit(t)
	repo := fixtureRepo(t)
	if strings.HasPrefix(repo, "/tmp/") {
		t.Fatalf("repo %q is under /tmp; the test would not exercise Scan", repo)
	}
	art := filepath.Join(repo, "CLAUDE.md")
	writeFile(t, art, "[g]("+repo+"/docs/guide.md) and [x]("+repo+"/docs/gone.md)\n")

	r := newScanner("", true).scan([]string{art}, Options{})
	if got := bucket(r, repo+"/docs/guide.md"); got != "" {
		t.Errorf("absolute link to a live file reported %s: %+v", got, r)
	}
	if got := bucket(r, repo+"/docs/gone.md"); got != "dead" {
		t.Errorf("absolute link to a missing file = %q, want dead: %+v", got, r)
	}
}

func TestScanUnreadableFailsTowardLive(t *testing.T) {
	isolateGit(t)
	repo := fixtureRepo(t)
	writeFile(t, filepath.Join(repo, "locked", "f.go"), "")
	lockDir(t, filepath.Join(repo, "locked"))
	art := filepath.Join(repo, "CLAUDE.md")
	writeFile(t, art, "See `locked/f.go` and `deep/conn.go`.\n")

	r := newScanner("", true).scan([]string{art}, Options{})
	if len(r.Dead)+len(r.AmbiguousMissing) != 0 {
		t.Fatalf("refs behind an unreadable dir must count as live, got %+v", r)
	}
}

func TestScanSymlinkedArtifactNominalDir(t *testing.T) {
	isolateGit(t)
	repo := fixtureRepo(t)
	target := filepath.Join(repo, "home", "ai", "CLAUDE.global.md")
	writeFile(t, target, "See `rules/local.md` and `rules/gone.md`.\n")

	nominal := realTempDir(t)
	writeFile(t, filepath.Join(nominal, "rules", "local.md"), "")
	link := filepath.Join(nominal, "CLAUDE.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	r := newScanner("", true).scan([]string{link}, Options{})
	if got := bucket(r, "rules/local.md"); got != "" {
		t.Errorf("rules/local.md beside the nominal path reported %s: %+v", got, r)
	}
	if got := bucket(r, "rules/gone.md"); got != "dead" {
		t.Errorf("rules/gone.md = %q, want dead (rules/ exists beside the nominal path): %+v", got, r)
	}
}

func TestScanImportBounds(t *testing.T) {
	isolateGit(t)
	repo := realTempDir(t)
	gitInit(t, repo)
	outside := filepath.Join(realTempDir(t), "outside.md")
	writeFile(t, outside, "See `docs/gone.go`.\n")
	escape := filepath.Join(repo, "escape.md")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(repo, "big.md")
	writeFile(t, big, strings.Repeat("x", 1<<20+1))
	inside := filepath.Join(repo, "inside.md")
	writeFile(t, inside, "")

	imports := "@" + outside + "\n@escape.md\n@big.md\n@inside.md\n@gone.md\n"
	if _, err := os.Stat("/proc/self/status"); err == nil {
		imports += "@/proc/self/status\n"
	}
	art := filepath.Join(repo, "CLAUDE.md")
	writeFile(t, art, imports)

	r := newScanner("", true).scan([]string{art}, Options{})
	if want := []string{art, inside}; !reflect.DeepEqual(r.Scanned, want) {
		t.Fatalf("Scanned = %q, want %q (imports outside the root, oversize, or under /proc not followed)", r.Scanned, want)
	}
	if bucket(r, "gone.md") != "dead" || len(r.Dead)+len(r.AmbiguousMissing) != 1 {
		t.Fatalf("want only the missing import gone.md reported, got %+v", r)
	}
}
