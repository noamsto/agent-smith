# Applier — Design

> Consumes the analyst's `proposals.json` and closes the loop: resolve the repo
> that owns each implicated artifact, apply the proposed change there via a
> subagent editor, verify it, open a human-gated PR, and append the PR link back
> to the committed reason-log.

**Status:** Design approved (brainstorm), pending implementation plan.
**Date:** 2026-06-02
**Unit:** Applier (Phase 1). Part of `github.com/noamsto/agent-smith`.
**Upstream:** [analyst](2026-06-01-analyst-design.md) → `proposals.json` + `reason-log/`.
**Top-level design:** [`docs/specs/2026-06-01-agent-smith-design.md`](../../specs/2026-06-01-agent-smith-design.md) §7.

---

## 1. Purpose

The extractor mines glitches; the analyst diagnoses them into proposals. The
applier is the last hop: it turns each proposal into a reviewable change in
whatever repo owns the artifact, without ever touching the user's live working
tree. Phase 1 is **PR-gated** — the applier proposes, a human merges.

**In scope:** resolve owning repo, apply the edit (prose artifacts *and* the
`escalate-out-of-instructions` hook/settings case), verify the edit, open a PR,
record the PR link.
**Out of scope (deferred):** factify Linear-ticket creation + Linear branch
naming; auto-commit (Phase 2); the `/agent-smith` orchestration command; deja-vu
outcome tracking.

---

## 2. Architecture

Mirrors the analyst's proven split — a deterministic Go binary + a pure subagent
prompt + a runbook — because a Go binary cannot dispatch a Claude Code subagent.
The deterministic git/gh/fs work lives in the binary; the judgment (making the
edit) lives in a harness-neutral prompt; the runbook drives the CC `Agent`
dispatch. Every edit happens in an **isolated `git worktree`**, so live checkouts
are never mutated and concurrent applies cannot collide.

```
proposals.json ──► applier prepare ──► apply-plan.json        (pure resolution, unit-tested)
                                            │
   per plan entry (RUNBOOK loop):           ▼
   applier open  --id X   ──► git worktree add (new branch off default) in the owning repo
   editor.md subagent     ──► reads proposal + artifact, edits IN the worktree, returns JSON
   VERIFY (subagents)     ──► deslop on the diff; find-bugs/code-review if hooks/settings touched
                              findings loop back to editor, or annotate the PR body
   applier submit --id X  ──► commit · push · gh pr create --assignee @me
                              · append PR link to reason-log · drop the worktree
```

**Design bet (inherited):** the binary is dumb and deterministic; the subagent is
smart and narrow. The 1→2 phase change (PR → auto-commit for nix-config) is a
branch in `submit`, not a rearchitecture.

---

## 3. Components

All under `internal/applier/` and `cmd/applier/`, matching the extractor/analyst
layout (`cmd/<bin>/main.go` thin CLI over an `internal/<unit>` package).

### 3.1 `resolve.go` — proposal → target (pure, no mutation)

`Resolve(p Proposal) (Target, error)` where:

```go
type Target struct {
    RepoRoot   string // git toplevel owning the artifact
    FilePath   string // absolute path to the artifact file
    Section    string // optional "#section" anchor, "" if none
    Owner      string // "nix-config" | "personal" | "factify-inc"
    BranchName string // e.g. docs/agent-smith-skeleton-reads
    Base       string // owning repo's default branch
}
```

- **Parse `implicated_artifact`:** split on the first `#`; left is the file path,
  right is the section anchor (informational — passed to the editor, not used to
  slice the file).
- **Repo root:** `git -C <dir(file)> rev-parse --show-toplevel`. For a
  not-yet-existing `add` target, walk up from the deepest existing parent.
- **Owner classification:** `git -C <root> remote get-url origin` → match
  `nix-config`, the `factify-inc` org, else `personal`. Phase 1 uses generic
  naming for all three; the `factify-inc` label only drives the Phase-2 Linear
  path and a "never auto-commit" assertion.
- **Branch name:** `<type>/agent-smith-<slug>`, `slug` from the proposal id.
  `type` = `docs` for prose fix_types (`add`/`strengthen`/`fix-stale`/`remove`),
  `chore` for `escalate-out-of-instructions`. One slash, provenance-marked.
- **Base:** `git -C <root> symbolic-ref refs/remotes/origin/HEAD` (fallback `main`).

### 3.2 `prepare` — batch resolution → `apply-plan.json`

```go
type PlanEntry struct {
    ProposalID, RepoRoot, FilePath, Section, Owner, BranchName, Base string
    Status string // "ready" | "skip-unresolved" | "skip-missing-file"
}
```

Reads `proposals.json`, resolves each, and assigns `Status`:

- `skip-unresolved` — artifact path is in no git repo.
- `skip-missing-file` — `strengthen`/`fix-stale`/`remove` whose file does not
  exist (nothing to strengthen/fix/remove). `add` to a missing file stays `ready`
  (the editor creates it).
- `ready` — everything else.

A skip never blocks other entries. Output is sorted by ProposalID for
deterministic runbook iteration.

### 3.3 `worktree.go` — isolation

- `Open(t Target) (wt string, err error)` = `git -C <RepoRoot> worktree add <tmpdir> -b <BranchName> <Base>`; returns the temp worktree path.
- `Drop(repoRoot, wt string) error` = `git -C <repoRoot> worktree remove --force <wt>`.

Plain `git worktree` — **not** `wt`/worktrunk, which is interactive and
tmux-coupled. The applier needs scriptable, headless git. The worktree lives in a
temp dir and is dropped in a deferred cleanup even on failure.

### 3.4 `editor.md` — the subagent prompt (pure `proposal → edit`)

Input (inlined by the runbook dispatch): the proposal JSON + the resolved
`FilePath` (inside the worktree) + `Section` + `FixType`. For
`escalate-out-of-instructions`, the prompt also carries the **two-layer settings
rule**: keys referencing `/nix/store` paths (hooks, statusLine) belong in the
Nix-generated `--settings` overlay (`home/ai/claude-code/default.nix`); everything
else belongs in `settings.json`.

The editor **edits the file(s) directly in the worktree** with its Edit/Write
tools and returns a JSON summary — the binary commits whatever the worktree diff
is, so it never re-parses the free-form `proposed_change`:

```json
{ "applied": true, "files_changed": ["..."], "summary": "one-line PR title material", "reason": "" }
```

Rules the prompt enforces:

- Realize the *intent* of `proposed_change` (which may be a unified diff, a
  replacement block, or a hook sketch), matching the artifact's existing style and
  altitude. Change only what the proposal calls for.
- For `add`: create/extend the section. For `strengthen`: raise/tighten the
  existing rule in place. For `fix-stale`: correct the stale reference. For
  `remove`: cut/rewrite the offending guidance. For `escalate`: write the hook /
  permission / default into the correct settings layer.
- **Decline** (`applied: false`, with `reason`) if the artifact content has
  drifted such that the diagnosis no longer holds — better no PR than a wrong one.

### 3.5 Verify gate (runbook stage, subagent dispatch)

After the editor returns `applied: true`, before `submit`:

- Dispatch **`deslop`** against the worktree diff (the primary gate — instruction
  artifacts are prose, where LLM slop concentrates).
- When the diff touches hooks/`settings.json`/the Nix overlay, also dispatch
  **`find-bugs`** / **`code-review`**.
- Findings either loop back to the editor (one revision pass) or are appended to
  the PR body as reviewer notes. The gate never silently drops findings.

### 3.6 `submit.go` — commit · PR · reason-log

`Submit(t Target, wt string, ed EditorResult) (prURL string, err error)`:

1. If `git -C <wt> diff --quiet` (no change — editor declined or was a no-op) →
   skip, report, drop worktree.
2. Conventional commit: `<type>(<scope>): <summary>`; body = diagnosis + evidence
   + `reason_log` + an agent-smith provenance line.
3. `git -C <wt> push -u origin <BranchName>`.
4. `gh pr create --assignee @me --title … --body …` (per global CLAUDE.md), capture
   the URL. **Always a PR; never a commit to the default branch.**
5. `reasonlog.AppendPRLink(...)`; drop the worktree.

`gh` and `git push` go through an **injected command runner** (`func(name string,
args ...string) ([]byte, error)`) so tests run offline against a fake runner.

### 3.7 `reasonlog.go` — close the ledger entry

`AppendPRLink(dir string, p Proposal, prURL, date string) error`: locate
`reason-log/<date>-<slug>.md` (fallback: scan for the `# <id>` heading), and
replace the `<!-- PR link appended by the applier; outcome appended by deja-vu -->`
placeholder with `**PR:** <prURL>` followed by a residual
`<!-- outcome appended by deja-vu -->` marker. Idempotent: if a `**PR:**` line is
already present, do nothing.

### 3.8 `cmd/applier/main.go`

Subcommands, matching the analyst's CLI idiom (`flag.NewFlagSet` per subcommand):

- `applier prepare --proposals proposals.json --out apply-plan.json`
- `applier open    --plan apply-plan.json --id <proposal-id>` → prints the worktree path
- `applier submit  --plan apply-plan.json --id <proposal-id> --proposals proposals.json --reason-log-dir reason-log --worktree <path> [--editor-result result.json]`

`open` prints the worktree path it created; the runbook captures it and passes it
back to `submit --worktree`. `--editor-result` is the JSON the editor subagent
returned (written to a file by the runbook), supplying the PR title/summary.

---

## 4. Data flow

```
proposals.json ─┐
                ├─► prepare ─► apply-plan.json ─► (runbook iterates ready entries)
reason-log/*.md ┘                                     │
                                                      ├─ open  → worktree path
                                                      ├─ editor subagent edits worktree
                                                      ├─ verify subagents on the diff
                                                      └─ submit → PR URL, reason-log updated, worktree dropped
```

`apply-plan.json` is the deterministic hand-off the runbook loops over; the
editor and verify subagents are the only non-deterministic steps, isolated to the
runbook (harness-specific), exactly as the Oracle is in the analyst.

---

## 5. Error handling (define errors out of existence)

| Situation | Behavior |
|-----------|----------|
| Artifact in no git repo | `skip-unresolved` in the plan; logged; other entries proceed. |
| `strengthen`/`fix-stale`/`remove` on a missing file | `skip-missing-file`; logged. |
| `add` to a missing file | `ready`; editor creates the file. |
| Editor declines (`applied:false`) or makes no change | `submit` finds an empty diff → skips with the reason; no PR. |
| Verify gate flags issues | one editor revision pass, else notes appended to the PR body. |
| `git push` / `gh` failure | local branch + commit are left intact; reported; re-run is idempotent (branch reused, PR-link append is idempotent). |
| Dirty live checkout | irrelevant — all work is in an isolated worktree off the default branch. |
| Worktree cleanup | deferred `Drop`, runs even on panic/early return. |

---

## 6. Testing

Deterministic units are unit-tested against **temporary git repos** (created in
`t.TempDir()` with `git init` + a seed commit); no network, no real `gh`.

- `resolve_test.go` — `implicated_artifact` parsing (`#section`, bare path), owner
  classification from remote URL, branch-name derivation per fix_type,
  missing-file → status mapping. Table-driven.
- `prepare_test.go` — proposals.json → apply-plan.json, mixed ready/skip cases,
  deterministic ordering.
- `worktree_test.go` — `Open` creates the branch + worktree; `Drop` removes it.
- `submit_test.go` — commit-message shape, "empty diff → skip", PR creation via a
  **fake command runner** (asserts the `gh`/`git` argv), URL capture.
- `reasonlog_test.go` — placeholder → `**PR:**` replacement, idempotency, heading
  fallback.

**Editor + verify (the subagent acceptance bar):** on-demand golden run via
`fixtures/applier/RUNBOOK.md`, like the analyst's Oracle runbook — apply the
skeleton-first `strengthen` proposal against a throwaway git repo and confirm the
edit lands in place (no duplicate section), deslop passes, and the PR body/commit
are well-formed. Not part of `go test`.

---

## 7. Repo layout additions

```
agent-smith/
├── cmd/applier/main.go
├── internal/applier/
│   ├── resolve.go        resolve_test.go
│   ├── prepare.go        prepare_test.go      # PlanEntry, prepare
│   ├── worktree.go       worktree_test.go
│   ├── submit.go         submit_test.go
│   ├── reasonlog.go      reasonlog_test.go
│   └── editor.md                              # the subagent prompt (//go:embed)
├── fixtures/applier/RUNBOOK.md
└── docs/applier.md                            # usage (written at the end)
```

`flake.nix` `subPackages` gains `cmd/applier`; the wrapper loop already iterates
binary names. No CGO; stdlib-only Go; the binary shells out to `git`/`gh`.

---

## 8. Deferred (explicitly not Phase 1)

- factify-inc Linear-ticket creation + Linear branch naming.
- Auto-commit for nix-config-owned artifacts (Phase 2 — a `submit` branch).
- The `/agent-smith` orchestration command (extractor → analyst → applier).
- `deja-vu` outcome append to the reason-log.
