# Applier Resolution (symlinks & worktrees) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `internal/applier/resolve.go` reach the editable source of an artifact by following symlinks and resolving linked git worktrees to their main repo, so the highest-signal artifacts stop being skipped.

**Architecture:** Two canonicalization steps added to `Resolve`, via three pure-ish helpers (`resolveRealPath`, `isImmutableStorePath`, `mainRepoRoot`). No change to the `Target` struct or to `prepare`/`worktree`/`submit`. Errors flow through `prepare`'s existing skip statuses.

**Tech Stack:** Go stdlib (`path/filepath` EvalSymlinks, `os`), shelling out to `git`. Tests use real temp git repos + real symlinks + a real `git worktree add`, matching the package's existing style.

**Spec:** `docs/superpowers/specs/2026-06-03-applier-resolution-symlink-worktree.md`

---

## File Structure

| File | Change |
|------|--------|
| `internal/applier/resolve.go` | Add helpers `resolveRealPath`, `isImmutableStorePath`, `mainRepoRoot`; rewrite `Resolve` to canonicalize before repo/owner/branch derivation; rename the shadowing local `path` var to `artifactPath`. |
| `internal/applier/resolve_test.go` | Add helper tests + `Resolve` symlink/worktree tests; harden existing `TestResolve` to compare against an `EvalSymlinks`-normalized root. |

Both already exist (from the merged applier). The `git(dir, args...)`, `repoRoot`, `classifyOwner`, `defaultBranch`, `commitType`, `slug`, `splitArtifact` helpers and the `initRepo` test helper are present — reuse them, do not redefine.

---

## Task 1: Resolution helpers

**Files:**
- Modify: `internal/applier/resolve.go`
- Test: `internal/applier/resolve_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/applier/resolve_test.go` (the imports `os`, `path/filepath`, `testing` are already present from existing tests):

```go
func TestIsImmutableStorePath(t *testing.T) {
	if !isImmutableStorePath("/nix/store/abc-x/CLAUDE.md") {
		t.Error("nix store path should be immutable")
	}
	if isImmutableStorePath("/home/u/repo/CLAUDE.md") {
		t.Error("normal path should not be immutable")
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
	want, _ := filepath.EvalSymlinks(filepath.Join(repo, "CLAUDE.md"))
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
	wantDir, _ := filepath.EvalSymlinks(dir)
	if got != filepath.Join(wantDir, "NEW.md") {
		t.Errorf("resolveRealPath(add-target) = %q, want %q", got, filepath.Join(wantDir, "NEW.md"))
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
	wantMain, _ := filepath.EvalSymlinks(main)
	gotEval, _ := filepath.EvalSymlinks(got)
	if gotEval != wantMain {
		t.Errorf("mainRepoRoot(worktree) = %q (eval %q), want main %q", got, gotEval, wantMain)
	}
}
```

- [ ] **Step 2: Run, verify it fails to compile**

Run: `cd /home/noams/Data/git/noamsto/agent-smith && nix develop -c go test ./internal/applier/ -run 'TestIsImmutableStorePath|TestResolveRealPath|TestMainRepoRoot'`
Expected: FAIL — `undefined: isImmutableStorePath`, `resolveRealPath`, `mainRepoRoot`.

- [ ] **Step 3: Implement the three helpers in resolve.go**

Add these functions to `internal/applier/resolve.go` (anywhere among the other unexported helpers, e.g. after `repoRoot`). No new imports are needed (`os`, `path/filepath`, `strings`, `fmt` are already imported):

```go
// resolveRealPath canonicalizes an artifact path through symlinks to its real
// on-disk location. For a not-yet-existing file (an `add` target), it resolves
// the deepest existing ancestor directory and rejoins the missing remainder, so a
// symlinked parent is still followed. A broken symlink returns an error.
func resolveRealPath(p string) (string, error) {
	if _, err := os.Lstat(p); err == nil {
		return filepath.EvalSymlinks(p)
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
		return "", err
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
// to its git-common-dir (they differ only for a linked worktree).
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
```

- [ ] **Step 4: Run, verify the helper tests pass**

Run: `nix develop -c go test ./internal/applier/ -run 'TestIsImmutableStorePath|TestResolveRealPath|TestMainRepoRoot' -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Run the whole package (existing tests must still pass — Resolve is unchanged so far)**

Run: `nix develop -c go test ./internal/applier/ -v`
Expected: PASS (all existing + the 5 new helper tests). `nix develop -c go vet ./internal/applier/` clean; `nix develop -c gofmt -l internal/applier/` empty.

- [ ] **Step 6: Commit**

```bash
git add internal/applier/resolve.go internal/applier/resolve_test.go
git commit -m "feat(applier): resolution helpers — EvalSymlinks, nix-store guard, worktree→main"
```

---

## Task 2: Wire helpers into Resolve

**Files:**
- Modify: `internal/applier/resolve.go:104-118` (the `Resolve` function)
- Test: `internal/applier/resolve_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/applier/resolve_test.go`:

```go
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
	wantRoot, _ := filepath.EvalSymlinks(repo)
	if tg.RepoRoot != wantRoot {
		t.Errorf("RepoRoot = %q, want %q", tg.RepoRoot, wantRoot)
	}
	wantFile, _ := filepath.EvalSymlinks(filepath.Join(repo, "CLAUDE.md"))
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
	wantMain, _ := filepath.EvalSymlinks(main)
	if tg.RepoRoot != wantMain {
		t.Errorf("RepoRoot = %q, want main %q (not the worktree)", tg.RepoRoot, wantMain)
	}
	if tg.FilePath != filepath.Join(wantMain, "CLAUDE.md") {
		t.Errorf("FilePath = %q, want %q", tg.FilePath, filepath.Join(wantMain, "CLAUDE.md"))
	}
}
```

- [ ] **Step 2: Run, verify the new Resolve tests fail**

Run: `nix develop -c go test ./internal/applier/ -run 'TestResolveFollowsSymlink|TestResolveWorktreeToMain' -v`
Expected: FAIL — current `Resolve` uses the literal path, so `TestResolveFollowsSymlink` errors (`~/.claude`-style dir is not a git repo → returns an error) and `TestResolveWorktreeToMain` returns the worktree as `RepoRoot`, not the main repo.

- [ ] **Step 3: Rewrite Resolve**

Replace the entire `Resolve` function (currently `internal/applier/resolve.go:104-118`) with:

```go
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
		return Target{}, err
	}
	mainRoot := mainRepoRoot(worktreeRoot)
	file := real
	if mainRoot != worktreeRoot {
		if rel, rerr := filepath.Rel(worktreeRoot, real); rerr == nil {
			file = filepath.Join(mainRoot, rel)
		}
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
```

- [ ] **Step 4: Harden the existing TestResolve assertion**

In `internal/applier/resolve_test.go`, the existing `TestResolve` compares `tg.RepoRoot` to the raw `root`. Since `Resolve` now returns the `EvalSymlinks`-normalized root, update that one assertion. Replace:

```go
	if tg.RepoRoot != root {
		t.Errorf("RepoRoot = %q, want %q", tg.RepoRoot, root)
	}
```
with:
```go
	wantRoot, _ := filepath.EvalSymlinks(root)
	if tg.RepoRoot != wantRoot {
		t.Errorf("RepoRoot = %q, want %q", tg.RepoRoot, wantRoot)
	}
```

(Leave `TestResolveUnresolved` unchanged — a path under a non-git temp dir still resolves its real ancestor and then fails `git rev-parse`, so it still returns an error.)

- [ ] **Step 5: Run, verify all pass**

Run: `nix develop -c go test ./internal/applier/ -v`
Expected: PASS (all: existing hardened + Task 1 helpers + Task 2 Resolve tests).

Run: `nix develop -c go vet ./internal/applier/` → clean; `nix develop -c gofmt -l internal/applier/` → empty.

- [ ] **Step 6: Commit**

```bash
git add internal/applier/resolve.go internal/applier/resolve_test.go
git commit -m "feat(applier): Resolve follows symlinks + resolves worktrees to main repo"
```

---

## Task 3: Real-data acceptance (the global CLAUDE.md unblocks)

**Files:** none (verification only — read-only `prepare`, no edits/PRs).

- [ ] **Step 1: Build a one-proposal proposals.json for the real global artifact**

Run (from the repo root):
```bash
printf '[{"id":"acc-global","implicated_artifact":"/home/noams/.claude/CLAUDE.md#reading-code","fix_type":"strengthen","evidence":["acceptance"],"diagnosis":"d","proposed_change":"c","confidence":"high","reason_log":"r"}]' > /tmp/acc-proposals.json
```

- [ ] **Step 2: Run prepare and confirm it now resolves**

Run: `nix develop -c go run ./cmd/applier prepare --proposals /tmp/acc-proposals.json --out /tmp/acc-plan.json`
Expected stdout: `wrote 1 plan entries (1 ready) to /tmp/acc-plan.json` (was `0 ready` / `skip-unresolved` before this change).

Run: `nix develop -c jq -r '.[] | "\(.status)\t\(.owner)\t\(.repo_root)\t\(.file_path)"' /tmp/acc-plan.json`
Expected: `ready  nix-config  /home/noams/nix-config  /home/noams/nix-config/home/ai/claude-code/CLAUDE.global.md`

- [ ] **Step 3: Clean up the scratch files**

```bash
gtrash put /tmp/acc-proposals.json /tmp/acc-plan.json
```

(No commit — verification only.)

---

## Self-Review Notes (for the implementer)

- **No new imports:** `resolve.go` already imports `os`, `path/filepath`, `strings`, `fmt`. The `path` package stays (used by `defaultBranch`); the only `path` *variable* shadowing is removed by renaming the local to `artifactPath` in `Resolve`.
- **EvalSymlinks normalization:** all test assertions that compare against a temp root/file must run that expected value through `filepath.EvalSymlinks`, because `t.TempDir()` can sit under a symlinked path and `Resolve` now returns canonical paths. This is why `TestResolve` is hardened in Task 2 Step 4.
- **Worktree detection** uses git-dir≠git-common-dir (robust to non-`.git` dir names), not a `.git` string match.
- **Type consistency:** `Target` fields and the `git`/`repoRoot`/`classifyOwner`/`defaultBranch` signatures are unchanged; only `Resolve`'s body and three new unexported helpers are added.
