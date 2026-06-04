# `/agent-smith` — self-marketplaced Claude Code plugin

> Design spec. Status: approved 2026-06-04. Unit: the agent-smith orchestration plugin.

## Problem

The full loop — `extractor` → `analyst cluster` → Oracle (per cluster) → `analyst assemble`
→ `applier prepare`/`open`/`submit`, with Editor + verify subagent dispatches — works, but
is driven by hand via two RUNBOOKs (`fixtures/analyst/RUNBOOK.md`,
`fixtures/applier/RUNBOOK.md`). The design spec (§8) always intended a `/agent-smith` slash
command to be the "manual Phase-1 trigger." This unit builds it.

Only the Claude Code harness can dispatch subagents (Oracle/Editor/verify), so the orchestrator
must be a CC construct (a procedural slash command), not a Go binary. The cleanest packaging is
a **Claude Code plugin** that bundles the command and the LLM steps as named subagents.

## Decisions (locked in brainstorming)

- **Packaging:** the agent-smith repo becomes its **own single-plugin marketplace**
  (`.claude-plugin/marketplace.json` + `.claude-plugin/plugin.json`). The plugin bundles the
  command and the Oracle/Editor subagents. Distribution is just the delivery vehicle — no
  public-distribution/support commitment now (the public repo means others *can* add it later).
- **Binaries stay external, on PATH.** nix-config's flake input provides `extractor`/`analyst`/
  `applier` (the existing planned footprint). The plugin is **pure markdown**; the command calls
  the binaries by name. (No `kong`/extra deps — the engine stays stdlib-only, `vendorHash=null`.)
- **Prompt source-of-truth moves to the plugin.** The Oracle/Editor prompt bodies move to
  top-level `agents/oracle.md` & `agents/editor.md` (as subagents, with frontmatter). The
  currently-**unused** `go:embed` vars + `OraclePrompt()`/`EditorPrompt()` accessors are removed;
  the prompt-content tests are repointed to read `agents/*.md`. One source of truth, no dead code.
  The two RUNBOOKs (`fixtures/{analyst,applier}/RUNBOOK.md`) that reference the old
  `internal/.../{oracle,editor}.md` paths get those references updated to `agents/*.md` (and a note
  that `/agent-smith` now supersedes the manual loop).
- **Command shape:** a default autonomous run **and** phased entry points (the phases share the
  same intermediate artifacts, so the bare command is just the phases chained):
  - `/agent-smith` (bare) → `mine → propose → apply --all`, fully autonomous.
  - `/agent-smith mine` → `extractor` + `analyst cluster` → `incidents.db`, `clusters.json`.
  - `/agent-smith propose` → Oracle per cluster → `analyst assemble` → `proposals.json` + reason-log (review-only; no edits/PRs).
  - `/agent-smith apply [<id>]` → per ready proposal (or one `<id>`): `applier open` → Editor → verify → `applier submit`.
  - `/agent-smith status` → report which artifacts/state exist and the next step.
- **No step-by-step babysitting.** The run is autonomous up to the one outward boundary; the
  only outward action — opening PRs — is done by **auto-opening DRAFT PRs** (the PR itself is the
  review gate; drafts aren't merges). No per-proposal prompts. Editor declines and verify failures
  are **skipped and reported**, not paused on.

## Plugin layout (at repo root)

```
.claude-plugin/
  plugin.json         # { "name": "agent-smith", "description": ..., "version": ... }
  marketplace.json    # { name, owner, plugins: [ { name: "agent-smith", source: "." } ] }
commands/
  agent-smith.md      # the orchestration command (bare + mine/propose/apply/status)
agents/
  oracle.md           # moved from internal/analyst/oracle.md + frontmatter
  editor.md           # moved from internal/applier/editor.md + frontmatter
```

Subagent frontmatter (bodies unchanged from today's prompts):
- `oracle` — `description`, `tools: Read, Write` (reads the cluster JSON file, writes the proposal JSON); model left to inherit (a capable model). Pure prompt→JSON.
- `editor` — `description`, `tools: Read, Edit, Write, Bash` (edits within the worktree, runs `shellcheck`). (`hooks`/`mcpServers`/`permissionMode` are not allowed in plugin agents — not needed.)

## The command (`commands/agent-smith.md`) — procedural flow

The command is a prompt the main agent executes. Data flows through files, exactly as the manual
golden run did.

1. **mine** — run `extractor --out incidents.db`; `analyst cluster --db incidents.db --out clusters.json` (default `--max-incidents-per-cluster 50`). Report the cluster summary.
2. **propose** — for each cluster in `clusters.json`: write the single cluster to a temp file, dispatch the `agent-smith:oracle` subagent (told to read that file and write its proposal JSON to a per-cluster path). Then `analyst assemble --proposals-dir … --out proposals.json --reason-log-dir reason-log`. Review-only.
3. **apply** — `applier prepare --proposals proposals.json --out apply-plan.json`. For each `ready` entry (or the single `<id>`):
   - `applier open --plan … --id <id>` → capture worktree `$WT` + `$FILE`.
   - Dispatch the `agent-smith:editor` subagent with the proposal JSON, `file=$FILE`, `repo_root=$WT`.
   - **Verify gate** on `git -C $WT diff`: always `deslop`; if the diff touches a hook / `settings.json` / Nix overlay, also `find-bugs` + `code-review`. If findings are substantive, dispatch the editor once more with the findings appended (one revision pass); otherwise carry the notes into the PR body.
   - `applier submit … --draft` → open a **draft** PR, append the PR link to the reason-log.
   - On editor `applied:false` or empty diff or a failing step: record a skip with the reason; continue.
4. **status** — inspect which of `incidents.db` / `clusters.json` / `proposals.json` / `apply-plan.json` / reason-log entries exist and print the next action.

Bare `/agent-smith` runs 1→2→3(`--all`) then prints the final report: a table of
`[proposal_id, repo, fix_type, verify verdict, PR link | skip reason]`.

### `applier submit` draft support

`applier submit` currently opens a normal PR (`gh pr create … --assignee @me`). Add a `--draft`
flag that passes `--draft` to `gh pr create`. The command always uses `--draft`. (Small,
additive change to `cmd/applier` + `internal/applier/submit.go` + a test that the flag threads to
the `gh` args via the injected runner.)

## Components / boundaries

| File | Responsibility |
|------|----------------|
| `.claude-plugin/plugin.json` | plugin identity/metadata |
| `.claude-plugin/marketplace.json` | one-plugin catalog pointing at this repo |
| `commands/agent-smith.md` | the orchestration procedure (mine/propose/apply/status + bare) |
| `agents/oracle.md` | cluster → proposal-JSON subagent (canonical prompt) |
| `agents/editor.md` | proposal → in-worktree edit subagent (canonical prompt) |
| `internal/applier/submit.go` + `cmd/applier` | add `--draft` to `submit` |
| `internal/analyst/assemble.go`, `internal/applier/submit.go` | remove dead `OraclePrompt()`/`EditorPrompt()` + `go:embed` |
| `internal/analyst/oracle_test.go`, `internal/applier/editor_test.go` | repoint prompt-content assertions to `agents/*.md` |

## Testing

- **Go suite stays green** after the prompt move + accessor removal + `--draft` addition: `go build ./...`, `go test ./...`.
- **Prompt-content guards** (e.g. Oracle "MUST NOT choose `add`", Editor "edit in place / no duplicate") move to read `agents/oracle.md` / `agents/editor.md` (via `os.ReadFile` with a repo-relative path) and still assert the key rules survive the move.
- **`--draft` test**: with an injected runner, `submit … --draft` includes `--draft` in the `gh pr create` args; without it, it doesn't.
- **Plugin manifest validity** (a small Go test or a `just`/script check): `plugin.json` and `marketplace.json` parse as JSON with the required keys (`name`; marketplace `name`/`owner`/`plugins`), and `agents/*.md` carry valid YAML frontmatter with a `description`.
- **End-to-end** is the existing golden run the command codifies (the manual loop already exercised this session); no new corpus test.

## Acceptance bar

`/agent-smith` (bare) runs the whole loop autonomously on the real corpus and opens draft PRs
with no per-step prompts; the phased forms (`mine`/`propose`/`apply <id>`/`status`) each work
against the shared artifacts; the plugin's manifests + agents are valid so the plugin loads; and
`go test ./...` is green.

## Out of scope (deferred)

- **nix-config wiring** (flake input + `extraKnownMarketplaces`/`enabledPlugins` in the writable
  `settings.json`) — [#3](https://github.com/noamsto/agent-smith/issues/3).
- **HTML visualization / status dashboard** — [#2](https://github.com/noamsto/agent-smith/issues/2).
- `deja-vu` (Phase 2), Track B (freshness audit), non-draft / auto-merge PR modes, public
  distribution/support.
