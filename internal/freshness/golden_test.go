package freshness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/noamsto/agent-smith/internal/analyst"
)

// goldenRef is the (file, line, path) shape expected.json asserts against —
// deliberately thinner than Ref, since form/resolved_to/same_name are exercised
// by the unit tests, not the golden fixture.
type goldenRef struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Path string `json:"path"`
}

type goldenExpected struct {
	Dead             []goldenRef `json:"dead"`
	AmbiguousMissing []goldenRef `json:"ambiguous_missing"`
}

func loadGoldenExpected(t *testing.T) goldenExpected {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "golden", "expected.json"))
	if err != nil {
		t.Fatal(err)
	}
	var exp goldenExpected
	if err := json.Unmarshal(b, &exp); err != nil {
		t.Fatal(err)
	}
	return exp
}

// setupGoldenRepo copies testdata/golden/repo into a fresh temp dir and git
// inits it, returning the repo root.
func setupGoldenRepo(t *testing.T) string {
	t.Helper()
	repo := realTempDir(t)
	copyTree(t, filepath.Join("testdata", "golden", "repo"), repo)
	gitInit(t, repo)
	return repo
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatalf("copy tree: %v", err)
	}
}

func sortGoldenRefs(refs []goldenRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].File != refs[j].File {
			return refs[i].File < refs[j].File
		}
		if refs[i].Line != refs[j].Line {
			return refs[i].Line < refs[j].Line
		}
		return refs[i].Path < refs[j].Path
	})
}

// toGoldenRefs maps Refs to (file relative to repo, line, path), sorted.
func toGoldenRefs(repo string, refs []Ref) []goldenRef {
	out := make([]goldenRef, len(refs))
	for i, r := range refs {
		rel, err := filepath.Rel(repo, r.Artifact)
		if err != nil {
			rel = r.Artifact
		}
		out[i] = goldenRef{File: filepath.ToSlash(rel), Line: r.Line, Path: r.Path}
	}
	sortGoldenRefs(out)
	return out
}

// assertNoLiveFalsePositive walks the fixture tree and asserts that no path
// which actually exists on disk is the ResolvedTo of any Dead/AmbiguousMissing
// ref — the zero-false-positive contract, stated independently of expected.json.
func assertNoLiveFalsePositive(t *testing.T, repo string, report Report) {
	t.Helper()
	live := map[string]bool{}
	err := filepath.WalkDir(repo, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == repo {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		live[p] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk fixture: %v", err)
	}
	for _, r := range append(append([]Ref{}, report.Dead...), report.AmbiguousMissing...) {
		if live[r.ResolvedTo] {
			t.Errorf("ref resolved to a path that exists on disk: %+v", r)
		}
	}
}

func assertGoldenReport(t *testing.T, repo string, report Report) {
	t.Helper()
	exp := loadGoldenExpected(t)
	sortGoldenRefs(exp.Dead)
	sortGoldenRefs(exp.AmbiguousMissing)

	if gotDead := toGoldenRefs(repo, report.Dead); !reflect.DeepEqual(gotDead, exp.Dead) {
		t.Errorf("Dead = %+v, want %+v", gotDead, exp.Dead)
	}
	if gotAmb := toGoldenRefs(repo, report.AmbiguousMissing); !reflect.DeepEqual(gotAmb, exp.AmbiguousMissing) {
		t.Errorf("AmbiguousMissing = %+v, want %+v", gotAmb, exp.AmbiguousMissing)
	}

	claude := filepath.Join(repo, "CLAUDE.md")
	agents := filepath.Join(repo, "AGENTS.md")
	if !slices.Contains(report.Scanned, claude) || !slices.Contains(report.Scanned, agents) {
		t.Errorf("Scanned = %+v, want both %s and %s", report.Scanned, claude, agents)
	}

	assertNoLiveFalsePositive(t, repo, report)
}

func TestGolden(t *testing.T) {
	isolateGit(t)
	repo := setupGoldenRepo(t)

	report := Scan([]string{filepath.Join(repo, "CLAUDE.md")}, Options{})
	assertGoldenReport(t, repo, report)
}

func duckDBBin() string {
	if b := os.Getenv("AGENT_SMITH_DUCKDB"); b != "" {
		return b
	}
	return "duckdb"
}

func runDuckDBScript(t *testing.T, db, script string) {
	t.Helper()
	cmd := exec.Command(duckDBBin(), db)
	cmd.Stdin = strings.NewReader(script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("duckdb: %v\n%s", err, out)
	}
}

// TestGoldenDB builds an incidents.db whose candidates include a worktree
// copy of the fixture's CLAUDE.md alongside the real path, and asserts that
// analyst.Artifacts canonicalizes them to the single real artifact — and that
// scanning that audit set reproduces the golden dead/ambiguous set.
func TestGoldenDB(t *testing.T) {
	isolateGit(t)
	repo := setupGoldenRepo(t)
	claude := filepath.Join(repo, "CLAUDE.md")
	worktreeCopy := filepath.Join(repo, ".worktrees", "w", "CLAUDE.md")

	candidates, err := json.Marshal([]string{worktreeCopy, claude})
	if err != nil {
		t.Fatal(err)
	}

	db := filepath.Join(t.TempDir(), "incidents.db")
	ddl := `CREATE TABLE incidents (
	  incident_id VARCHAR PRIMARY KEY, session_id VARCHAR, project VARCHAR, ts VARCHAR,
	  signal_type VARCHAR, implicated_artifact VARCHAR, candidates JSON, "window" JSON,
	  confidence VARCHAR, detail JSON);`
	insert := fmt.Sprintf(`INSERT INTO incidents VALUES
	 (md5('i1'),'s1','/p1','2026-05-01T10:00:00Z','tool_error','%s',
	   '%s'::JSON,'[]'::JSON,'medium','{}'::JSON);`, worktreeCopy, candidates)
	runDuckDBScript(t, db, ddl+insert)

	artifacts, err := analyst.Artifacts(context.Background(), db)
	if err != nil {
		t.Fatalf("Artifacts: %v", err)
	}
	if want := []string{claude}; !reflect.DeepEqual(artifacts, want) {
		t.Fatalf("Artifacts = %q, want %q", artifacts, want)
	}

	report := Scan(artifacts, Options{})
	assertGoldenReport(t, repo, report)
}
