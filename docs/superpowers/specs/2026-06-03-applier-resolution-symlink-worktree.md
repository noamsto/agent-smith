# Applier resolution — symlinks & worktrees

> Make `resolve.go` reach the **editable source** of an implicated artifact:
> follow symlinks to the real git file, and resolve linked git worktrees to
> their canonical main repo, so the highest-signal artifacts stop being skipped.

**Status:** Design approved (brainstorm), pending implementation plan.
**Date:** 2026-06-03
**Unit:** Applier resolution fix (extends the [Applier](2026-06-02-applier-design.md)). Part of `github.com/noamsto/agent-smith`.

---

## 1. Motivation (from a live run)

Running the real pipeline (`extractor` → `analyst cluster` → `applier prepare`) on
the live corpus produced 36 clusters; **only 16 resolved to `ready`**. The misses:

- **`~/.claude/CLAUDE.md` — the #1 artifact (458 incident-sessions) — was
  `skip-unresolved`.** It's a Home-Manager out-of-store symlink; `~/.claude` is not
  a git repo, so `git rev-parse` on the literal path fails. `filepath.EvalSymlinks`
  resolves the chain to `~/nix-config/home/ai/claude-code/CLAUDE.global.md`, which
  **is** in the `nix-config` repo.
- **Live factify-`mono` worktree artifacts resolved with `repo_root` pointing at the
  ephemeral `.worktrees/eng-…` dir** (because `git rev-parse --show-toplevel` in a
  linked worktree returns the worktree, not the main repo) — a PR would be opened in
  a throwaway worktree instead of `factify/mono`.

This unit fixes both in `resolve.go`. Out of scope (deferred): canonicalizing
dead/removed worktree paths and upstream (extractor/analyst) path canonicalization
— those stay correctly `skip-missing-file`/`skip-unresolved`.

---

## 2. Design

All changes are in `internal/applier/resolve.go`; the `Target` struct and the
`prepare`/`worktree`/`submit` contracts are unchanged. Two canonicalization steps
are inserted into `Resolve` before owner/branch/base derivation.

### 2.1 Follow symlinks → editable source

New helper `resolveRealPath(path string) (string, error)`:

- If `path` exists: return `filepath.EvalSymlinks(path)`.
- If `path` does not exist (an `add` target): `EvalSymlinks` the deepest existing
  ancestor directory, then rejoin the not-yet-existing remainder
  (`filepath.Join(realParent, rest)`). This keeps `add`-to-a-new-file working while
  still canonicalizing a symlinked parent.
- On `EvalSymlinks` error (broken link / nothing exists): return the error.

`Resolve` calls `resolveRealPath` on the artifact's file path and uses the result
as **both** the repo-resolution input **and** the `Target.FilePath` the editor will
edit.

**Immutable-store guard.** `isImmutableStorePath(p string) bool` reports whether `p`
is under `/nix/store/`. If the resolved real path is immutable, `Resolve` returns an
error (it cannot be edited / is not in a working git tree) → `prepare` records
`skip-unresolved`. (For the target setup the real path lands in `~/nix-config`, so
this guard does not trigger; it exists for true in-store copies.)

### 2.2 Linked worktree → main repo

After `git -C <dir> rev-parse --show-toplevel` yields `worktreeRoot`, determine the
canonical repo:

- `commonDir := git -C worktreeRoot rev-parse --path-format=absolute --git-common-dir`.
- If `commonDir` equals `worktreeRoot/.git` → main checkout; `RepoRoot = worktreeRoot`.
- Else (linked worktree) → `RepoRoot = filepath.Dir(commonDir)` (the main worktree
  that owns the shared `.git`).

When `RepoRoot != worktreeRoot`, remap the file onto the main repo:
`rel := Rel(worktreeRoot, realFilePath)`, `FilePath = Join(RepoRoot, rel)`. So a
worktree-resident `CLAUDE.md` is edited and PR'd in the main repo.

`Owner`, `BranchName`, and `Base` are then derived from `RepoRoot` exactly as today.

### 2.3 Resolve flow (after changes)

```
path, section := splitArtifact(p.ImplicatedArtifact)
real, err      := resolveRealPath(path)            // EvalSymlinks (+ add-target handling)
   err  → return error  (→ skip-unresolved)
isImmutableStorePath(real) → return error  (→ skip-unresolved)
worktreeRoot, err := repoRoot(real)                // show-toplevel of real's dir
   err  → return error  (→ skip-unresolved)
mainRoot   := mainRepoRoot(worktreeRoot)           // git-common-dir → canonical repo
file       := real; if mainRoot != worktreeRoot { file = Join(mainRoot, Rel(worktreeRoot, real)) }
return Target{RepoRoot: mainRoot, FilePath: file, Section: section,
              Owner: classifyOwner(mainRoot), BranchName: …, Base: defaultBranch(mainRoot)}
```

---

## 3. Error handling

| Situation | Result |
|-----------|--------|
| Artifact symlink resolves into a working git repo | `ready`; edits the real file |
| Resolved real path under `/nix/store/` | `skip-unresolved` (immutable copy) |
| `EvalSymlinks` fails (broken link, dead worktree path, nothing exists) | `skip-unresolved` |
| `strengthen`/`fix-stale`/`remove` whose real file is absent | `skip-missing-file` (unchanged) |
| Linked worktree | `RepoRoot` = main repo; file remapped onto it |

No new `PlanEntry` status; everything flows through `prepare`'s existing gates.

---

## 4. Testing

Unit tests in `resolve_test.go`, real temp git repos (matching the package style):

- **Symlink resolution:** seed a repo with `CLAUDE.md`; create a symlink to it in a
  separate non-repo dir; `Resolve` with the symlink path → `RepoRoot` is the repo,
  `FilePath` is the real (EvalSymlinks'd) file.
- **Linked worktree → main:** `git worktree add` a linked worktree of a temp repo;
  artifact = `<worktree>/CLAUDE.md`; `Resolve` → `RepoRoot` is the **main** repo
  (not the worktree), `FilePath` under the main root.
- **Immutable guard:** `isImmutableStorePath("/nix/store/abc/CLAUDE.md")` is true;
  a normal path is false. (Pure helper — no real store needed.)
- **Hardening:** existing `TestResolve`/`TestResolveUnresolved` compare `RepoRoot`
  against `EvalSymlinks`-normalized temp paths (since `t.TempDir()` may sit under a
  symlink, e.g. macOS `/tmp → /private/tmp`).

The whole suite (`go test ./...`) and `nix build .#default` must stay green.

---

## 5. Out of scope (deferred)

- Dead/removed worktree path canonicalization (stays `skip-unresolved`).
- Upstream extractor/analyst path canonicalization (cluster de-fragmentation).
- Oracle cluster-window sampling for multi-MB clusters (separate concern surfaced
  by the same live run).
