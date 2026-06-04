# `/agent-smith` Plugin Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package agent-smith as a self-marketplaced Claude Code plugin: a `/agent-smith` orchestration command + the Oracle/Editor steps as bundled subagents, plus a `--draft` flag on `applier submit`.

**Architecture:** The plugin is pure markdown at the repo root (`.claude-plugin/`, `commands/`, `agents/`); the Go binaries stay on PATH (nix). The Oracle/Editor prompt bodies move from `internal/` into `agents/` (the dead `go:embed` accessors are removed). The command is a procedural prompt that runs the binaries and dispatches the bundled subagents, auto-opening draft PRs.

**Tech Stack:** Go (stdlib-only), DuckDB/git/gh via the existing binaries, Claude Code plugin format. Spec: `docs/superpowers/specs/2026-06-04-agent-smith-plugin-design.md`.

---

## File Structure

- `internal/applier/submit.go`, `cmd/applier/main.go`, `internal/applier/submit_test.go` — add `--draft` to `submit`.
- `agents/oracle.md` (moved from `internal/analyst/oracle.md`), `internal/analyst/assemble.go`, `internal/analyst/oracle_test.go` — Oracle becomes a plugin subagent; drop dead embed/accessor.
- `agents/editor.md` (moved from `internal/applier/editor.md`), `internal/applier/submit.go`, `internal/applier/editor_test.go` — Editor becomes a plugin subagent; drop dead embed/accessor.
- `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `internal/plugin/manifest_test.go` — plugin/marketplace manifests + validity test.
- `commands/agent-smith.md` — the orchestration command.
- `fixtures/analyst/RUNBOOK.md`, `fixtures/applier/RUNBOOK.md` — repoint stale prompt paths + supersession note.

---

### Task 1: Add `--draft` to `applier submit`

**Files:**
- Modify: `internal/applier/submit.go`, `cmd/applier/main.go`, `internal/applier/submit_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/applier/submit_test.go` (reuses `fakeRunner`, `sampleProposal`, `sampleEntry`):

```go
func TestSubmitDraftFlag(t *testing.T) {
	// draft=true adds --draft to gh pr create; draft=false omits it.
	for _, draft := range []bool{true, false} {
		dir := t.TempDir()
		rlPath := filepath.Join(dir, "2026-06-01-glitch-skeleton.md")
		if err := os.WriteFile(rlPath, []byte(sampleEntry), 0o644); err != nil {
			t.Fatal(err)
		}
		f := &fakeRunner{status: " M CLAUDE.md", prURL: "https://github.com/x/y/pull/9"}
		tg := Target{RepoRoot: "/r", BranchName: "docs/agent-smith-glitch-skeleton", Base: "main"}
		ed := EditorResult{Applied: true, FilesChanged: []string{"CLAUDE.md"}, Summary: "x"}

		if _, _, err := Submit(f.run, tg, "/wt", sampleProposal(), ed, dir, draft); err != nil {
			t.Fatalf("draft=%v: Submit: %v", draft, err)
		}
		last := f.calls[len(f.calls)-1]
		joined := strings.Join(last, " ")
		if got := strings.Contains(joined, "--draft"); got != draft {
			t.Errorf("draft=%v: --draft present=%v; gh args=%q", draft, got, joined)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `nix develop -c go test ./internal/applier/ -run TestSubmitDraftFlag`
Expected: FAIL — compile error (`Submit` takes 6 args, not 7).

- [ ] **Step 3: Thread `draft` through `Submit`**

In `internal/applier/submit.go`, change the signature and the `gh pr create` call. Replace:

```go
func Submit(run runner, t Target, wt string, p analyst.Proposal, ed EditorResult, reasonLogDir string) (prURL string, skipped bool, err error) {
```

with:

```go
func Submit(run runner, t Target, wt string, p analyst.Proposal, ed EditorResult, reasonLogDir string, draft bool) (prURL string, skipped bool, err error) {
```

and replace the `gh pr create` call:

```go
	out, err := run(wt, "gh", "pr", "create", "--assignee", "@me",
		"--title", title, "--body", body, "--head", t.BranchName, "--base", t.Base)
```

with:

```go
	ghArgs := []string{"pr", "create", "--assignee", "@me",
		"--title", title, "--body", body, "--head", t.BranchName, "--base", t.Base}
	if draft {
		ghArgs = append(ghArgs, "--draft")
	}
	out, err := run(wt, "gh", ghArgs...)
```

- [ ] **Step 4: Update the other call sites**

In `internal/applier/submit_test.go`, update the existing `TestSubmitCreatesPR` call:

```go
	url, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(), ed, dir)
```

to:

```go
	url, skipped, err := Submit(f.run, tg, "/wt", sampleProposal(), ed, dir, false)
```

In `cmd/applier/main.go` `runSubmit`, add the flag after `editorResult`:

```go
	draft := fs.Bool("draft", false, "open the PR as a draft")
```

and update the call:

```go
	url, skipped, err := applier.Submit(applier.Run, tg, *wt, prop, ed, *reasonLog, *draft)
```

- [ ] **Step 5: Run to verify pass**

Run: `nix develop -c go test ./internal/applier/`
Expected: PASS (new `TestSubmitDraftFlag` + unchanged `TestSubmitCreatesPR`).

- [ ] **Step 6: Commit**

```bash
git add internal/applier/submit.go cmd/applier/main.go internal/applier/submit_test.go
git commit -m "feat(applier): --draft flag on submit (open PRs as drafts)"
```

---

### Task 2: Move the Oracle prompt into a plugin subagent

**Files:**
- Move: `internal/analyst/oracle.md` → `agents/oracle.md`
- Modify: `internal/analyst/assemble.go`, `internal/analyst/oracle_test.go`

- [ ] **Step 1: Move the prompt and add subagent frontmatter**

```bash
mkdir -p agents
git mv internal/analyst/oracle.md agents/oracle.md
```

Then prepend this frontmatter to `agents/oracle.md` (keep the existing body verbatim below it):

```markdown
---
name: oracle
description: agent-smith Oracle — diagnoses ONE cluster of recurring behavioral incidents against the implicated instruction artifact and emits a single JSON fix proposal. Dispatched per cluster by /agent-smith.
tools: Read, Write
---
```

- [ ] **Step 2: Remove the dead embed + accessor**

In `internal/analyst/assemble.go`, delete these lines:

```go
//go:embed oracle.md
var oracleMD string

// OraclePrompt returns the Oracle subagent prompt (the cluster → proposal-JSON
// contract), for inlining into a dispatch by the orchestrator/eval.
func OraclePrompt() string { return oracleMD }
```

and remove the now-unused `_ "embed"` import from the import block.

- [ ] **Step 3: Repoint the prompt-content test**

Replace the body of `internal/analyst/oracle_test.go` with (reads the moved file; keeps the `contains` helper):

```go
package analyst

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOracleAgentEmbedsTheGuard(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "agents", "oracle.md"))
	if err != nil {
		t.Fatalf("read agents/oracle.md: %v", err)
	}
	p := string(b)
	if len(p) < 500 {
		t.Fatalf("oracle prompt looks too short (%d bytes)", len(p))
	}
	for _, must := range []string{
		"MUST NOT choose `add`", // the acceptance-bar guard
		"escalate-out-of-instructions",
		"Output valid JSON only",
		"artifact_content",
	} {
		if !contains(p, must) {
			t.Errorf("oracle prompt missing required text: %q", must)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
```

- [ ] **Step 4: Run to verify build + test pass**

Run: `nix develop -c go build ./... && nix develop -c go test ./internal/analyst/`
Expected: PASS (no more `OraclePrompt`; test reads `agents/oracle.md`).

- [ ] **Step 5: Commit**

```bash
git add agents/oracle.md internal/analyst/assemble.go internal/analyst/oracle_test.go
git commit -m "refactor(analyst): Oracle prompt becomes plugin subagent agents/oracle.md"
```

---

### Task 3: Move the Editor prompt into a plugin subagent

**Files:**
- Move: `internal/applier/editor.md` → `agents/editor.md`
- Modify: `internal/applier/submit.go`, `internal/applier/editor_test.go`

- [ ] **Step 1: Move the prompt and add subagent frontmatter**

```bash
git mv internal/applier/editor.md agents/editor.md
```

Then prepend this frontmatter to `agents/editor.md` (keep the existing body verbatim below it):

```markdown
---
name: editor
description: agent-smith Editor — applies ONE agent-smith proposal to its instruction artifact inside an isolated git worktree and returns a JSON summary. Dispatched per ready proposal by /agent-smith.
tools: Read, Edit, Write, Bash
---
```

- [ ] **Step 2: Remove the dead embed + accessor**

In `internal/applier/submit.go`, delete: the `//go:embed editor.md` directive, the `var editorMD string` line, the entire `EditorPrompt()` function together with its doc comment, and the now-unused `embed` import (the `_ "embed"` line) from the import block. After this, `editorMD`/`EditorPrompt` must not appear anywhere in the file.

- [ ] **Step 3: Repoint the prompt-content test**

Replace the body of `internal/applier/editor_test.go` with:

```go
package applier

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditorAgentContract(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "agents", "editor.md"))
	if err != nil {
		t.Fatalf("read agents/editor.md: %v", err)
	}
	p := string(b)
	for _, must := range []string{
		"applied", "files_changed", "summary", "reason", // output schema
		"escalate-out-of-instructions", // handles the hook case
		"settings.json",                // two-layer rule
		"/nix/store",                   // overlay rule
		"decline",                      // may refuse on drift
	} {
		if !strings.Contains(p, must) {
			t.Errorf("editor prompt missing %q", must)
		}
	}
}
```

- [ ] **Step 4: Run to verify build + test pass**

Run: `nix develop -c go build ./... && nix develop -c go test ./internal/applier/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add agents/editor.md internal/applier/submit.go internal/applier/editor_test.go
git commit -m "refactor(applier): Editor prompt becomes plugin subagent agents/editor.md"
```

---

### Task 4: Plugin + marketplace manifests with a validity test

**Files:**
- Create: `.claude-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `internal/plugin/manifest_test.go`

- [ ] **Step 1: Write the failing manifest test**

Create `internal/plugin/manifest_test.go`:

```go
package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoFile(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return b
}

func TestPluginManifest(t *testing.T) {
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(repoFile(t, ".claude-plugin/plugin.json"), &p); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	if p.Name == "" {
		t.Error("plugin.json: name is empty")
	}
}

func TestMarketplaceManifest(t *testing.T) {
	var m struct {
		Name  string `json:"name"`
		Owner struct {
			Name string `json:"name"`
		} `json:"owner"`
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(repoFile(t, ".claude-plugin/marketplace.json"), &m); err != nil {
		t.Fatalf("marketplace.json: %v", err)
	}
	if m.Name == "" || m.Owner.Name == "" || len(m.Plugins) == 0 || m.Plugins[0].Name == "" {
		t.Errorf("marketplace.json missing required fields: %+v", m)
	}
}

func TestAgentsHaveFrontmatter(t *testing.T) {
	for _, a := range []string{"agents/oracle.md", "agents/editor.md"} {
		s := string(repoFile(t, a))
		if !strings.HasPrefix(s, "---\n") {
			t.Errorf("%s: missing opening frontmatter", a)
			continue
		}
		if !strings.Contains(s[len("---\n"):], "\n---") {
			t.Errorf("%s: missing closing frontmatter", a)
		}
		if !strings.Contains(s, "description:") {
			t.Errorf("%s: frontmatter missing description", a)
		}
	}
}
```

(`TestCommandExists` is added in Task 5 once the command file exists — keeping this task self-contained.)

- [ ] **Step 2: Run to verify it fails**

Run: `nix develop -c go test ./internal/plugin/`
Expected: FAIL — `read .claude-plugin/plugin.json: ... no such file` (manifests not created yet). (`agents/*.md` already exist from Tasks 2–3.)

- [ ] **Step 3: Create the manifests**

Create `.claude-plugin/plugin.json`:

```json
{
  "name": "agent-smith",
  "description": "Mines Claude Code session history for recurring agent glitches and proposes instruction-artifact fixes via PR.",
  "version": "0.1.0"
}
```

Create `.claude-plugin/marketplace.json`:

```json
{
  "name": "agent-smith",
  "owner": { "name": "noamsto" },
  "plugins": [
    { "name": "agent-smith", "source": "./" }
  ]
}
```

- [ ] **Step 4: Run to verify pass**

Run: `nix develop -c go test ./internal/plugin/`
Expected: PASS (manifests parse; `agents/*.md` have frontmatter).

- [ ] **Step 5: Commit**

```bash
git add .claude-plugin/ internal/plugin/manifest_test.go
git commit -m "feat(plugin): plugin + marketplace manifests with a validity test"
```

---

### Task 5: The `/agent-smith` orchestration command

**Files:**
- Create/replace: `commands/agent-smith.md`

- [ ] **Step 1: Write the command**

Create `commands/agent-smith.md` with exactly this content:

````markdown
---
description: Run the agent-smith loop — mine session glitches, diagnose fixes (Oracle), and open draft PRs (Editor). Bare = full autonomous run; or pass mine|propose|apply [<id>]|status.
argument-hint: "[mine|propose|apply [<id>]|status]"
allowed-tools: Bash, Read, Write, Agent
---

You are orchestrating the **agent-smith** loop. The deterministic steps are the
`extractor`/`analyst`/`applier` binaries (on PATH). The judgement steps are the
bundled subagents `agent-smith:oracle` and `agent-smith:editor`, which you dispatch
with the Agent tool. Work from the current repo (the agent-smith checkout). All
intermediate artifacts live in the cwd: `incidents.db`, `clusters.json`,
`proposals.json`, `apply-plan.json`, and `reason-log/`.

`$ARGUMENTS` selects the phase. Empty → run the **full autonomous loop**
(`mine` → `propose` → `apply` for every ready proposal). Otherwise dispatch on the
first word: `mine`, `propose`, `apply` (optional second word = a single proposal id),
or `status`.

## mine
1. `extractor --out incidents.db`
2. `analyst cluster --db incidents.db --out clusters.json --min-sessions 3 --max-incidents-per-cluster 50`
3. Print a one-line-per-cluster summary (signal_type, artifact basename, distinct_sessions, total_incidents, sampled count) using `jq`.

## propose
Precondition: `clusters.json` exists (else run `mine` first).
1. `mkdir -p /tmp/agent-smith-proposals-in`
2. For each cluster object in `clusters.json` (iterate with `jq -c '.[]'`):
   - Write that single cluster object to `/tmp/agent-smith-proposals-in/cluster-<i>.json`.
   - Dispatch the **agent-smith:oracle** subagent (Agent tool) with this prompt:
     "Read the cluster at `/tmp/agent-smith-proposals-in/cluster-<i>.json` and follow
     your instructions to produce ONE proposal. Write ONLY the JSON object to
     `/tmp/agent-smith-proposals-in/p-<i>.json`." (Delete the cluster temp after.)
   - If the Oracle errors or writes no file, log a skip and continue.
3. `analyst assemble --proposals-dir /tmp/agent-smith-proposals-in --out proposals.json --reason-log-dir reason-log`
   (Pass `--date <today>` only if needed; default is today.)
4. Report the assembled proposals (id, fix_type, confidence). This phase is
   review-only — no edits, no PRs.

## apply [<id>]
Precondition: `proposals.json` exists (else run `propose` first).
1. `applier prepare --proposals proposals.json --out apply-plan.json`
2. Determine the targets: if `<id>` was given, just that id; else every entry with
   `status == "ready"` in `apply-plan.json` (read with `jq`).
3. For each target id:
   a. `applier open --plan apply-plan.json --id <id>` → capture line 1 as `$WT`
      (worktree) and line 2 as `$FILE`.
   b. Extract that proposal object from `proposals.json` to a temp file. Dispatch the
      **agent-smith:editor** subagent (Agent tool) with: the proposal temp-file path,
      `file=$FILE`, `repo_root=$WT`, and the instruction to follow its own contract
      and write its result JSON to `$WT/../editor-result-<id>.json`.
   c. **Verify gate** on `git -C "$WT" diff`:
      - Always run the `deslop` skill/review on the diff.
      - If the diff touches a hook, `settings.json`, or a Nix `*.nix` overlay, also
        run `find-bugs` and `code-review`.
      - If a reviewer reports a **substantive** (Critical/Important) finding, dispatch
        `agent-smith:editor` once more with the findings appended (one revision pass).
        Otherwise carry the notes forward (they go in the PR body).
   d. `applier submit --plan apply-plan.json --proposals proposals.json --id <id>
      --worktree "$WT" --editor-result <result-file> --reason-log-dir reason-log --draft`
      (always `--draft`).
   e. If the editor declined (`applied:false`), the diff was empty, or any step
      failed: record a **skip** with the reason and continue to the next id. Never abort
      the whole run for one bad proposal.
4. After all targets: commit the reason-log link update in this repo
   (`git add reason-log/ && git commit -m "docs(reason-log): link agent-smith PRs"`).

## status
Report which of `incidents.db`, `clusters.json`, `proposals.json`, `apply-plan.json`,
and `reason-log/` entries exist, the cluster/proposal counts, and the next phase to run.

## Final report (full run)
Print a table: `proposal_id | repo | fix_type | verify verdict | PR link or skip reason`.
All PRs are **drafts** — tell the user to review / `nix build` / merge them at their leisure.
````

- [ ] **Step 2: Add the command-existence check and run it**

Append to `internal/plugin/manifest_test.go`:

```go
func TestCommandExists(t *testing.T) {
	if len(repoFile(t, "commands/agent-smith.md")) == 0 {
		t.Error("commands/agent-smith.md is empty")
	}
}
```

Run: `nix develop -c go test ./internal/plugin/`
Expected: PASS (all manifest checks + `TestCommandExists`).

- [ ] **Step 3: Commit**

```bash
git add commands/agent-smith.md internal/plugin/manifest_test.go
git commit -m "feat(plugin): /agent-smith orchestration command (mine/propose/apply/status)"
```

---

### Task 6: Repoint the RUNBOOKs

**Files:**
- Modify: `fixtures/analyst/RUNBOOK.md`, `fixtures/applier/RUNBOOK.md`

- [ ] **Step 1: Update the analyst RUNBOOK**

In `fixtures/analyst/RUNBOOK.md`, replace the reference `internal/analyst/oracle.md` with `agents/oracle.md`, and add this note near the top (after the first paragraph):

```markdown
> **Superseded by `/agent-smith`.** This manual loop is now automated by the
> `/agent-smith` plugin command (`commands/agent-smith.md`); keep this RUNBOOK as the
> golden-eval recipe and for debugging individual steps.
```

- [ ] **Step 2: Update the applier RUNBOOK**

In `fixtures/applier/RUNBOOK.md`, replace the reference `internal/applier/editor.md` with `agents/editor.md`, and add the same supersession note near the top.

- [ ] **Step 3: Commit**

```bash
git add fixtures/analyst/RUNBOOK.md fixtures/applier/RUNBOOK.md
git commit -m "docs(runbook): point at agents/*.md; note /agent-smith supersedes the manual loop"
```

---

### Task 7: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Build, vet, and run the entire suite**

Run: `nix develop -c go build ./... && nix develop -c go vet ./... && nix develop -c go test ./...`
Expected: PASS across extractor, analyst, applier, and plugin; no vet findings.

- [ ] **Step 2: Confirm no stale references to the moved prompts / removed accessors**

Run: `nix develop -c grep -rn "OraclePrompt\|EditorPrompt\|internal/analyst/oracle.md\|internal/applier/editor.md" --include=*.go --include=*.md . | grep -v docs/superpowers`
Expected: no matches (all references updated; spec/plan docs under `docs/superpowers` are allowed to mention them historically).

- [ ] **Step 3: Confirm the plugin tree is present**

Run: `ls .claude-plugin/ commands/ agents/`
Expected: `.claude-plugin/{plugin.json,marketplace.json}`, `commands/agent-smith.md`, `agents/{oracle.md,editor.md}`.

- [ ] **Step 4: Final commit if any fixups were needed**

```bash
git add -A
git commit -m "chore(plugin): verification fixups"
```

(Skip if the working tree is clean.)
