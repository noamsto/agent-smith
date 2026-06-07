# Binary distribution — just-works plugin install

**Date:** 2026-06-07 · **Status:** approved design

## Problem

The plugin installs from the marketplace, but the `extractor`/`analyst`/`applier`
binaries don't come with it — today the user must `nix build` and put them on
PATH. That fails the "install plugin, run `/agent-smith`, done" bar. The README's
install section also reads as one cramped paragraph.

The binaries are pure Go (zero CGO — they shell out to the `duckdb` CLI and
`git`/`gh`), so static cross-compiled release assets are trivially buildable.

## Design

### 1. Release pipeline (GoReleaser)

- `.goreleaser.yaml`: three builds (`extractor`, `analyst`, `applier`),
  `CGO_ENABLED=0`, targets `{linux,darwin} × {amd64,arm64}`, version stamped via
  `-X main.version={{.Version}}`.
- One archive per platform containing all three binaries, pinned name template
  `agent-smith_{{.Os}}_{{.Arch}}.tar.gz`, plus the default checksums file.
- `.github/workflows/release.yml`: on `v*` tag push → `goreleaser/goreleaser-action`.
  The workflow **fails if the tag ≠ `plugin.json` version** (see invariant below).
- Each binary gains a `--version` flag printing the stamped version.
- `goreleaser` joins the devshell for `goreleaser release --snapshot --clean`
  local testing.

**Version invariant.** The marketplace serves `main`, not tags. Therefore:
`plugin.json`'s `version` only changes in the release commit, and the `vX.Y.Z`
tag goes on exactly that commit. Between releases, main carries the last
released version — prompt/agent-file tweaks ride along without a binary bump
(the version pins binaries, not prompts). Tag ⇔ plugin.json ⇔ binary
`--version` always agree.

### 2. Bootstrap (`scripts/bootstrap.sh`)

Ships in the plugin. The `/agent-smith` command runs it as step zero and
prepends its stdout (the resolved bin dir) to PATH for the session.

1. Read the expected version from `${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json`.
2. Per binary: PATH hit at the right `--version` → use it (nix users
   short-circuit; no download ever). Wrong version on PATH → warn, fall
   through. Else check `~/.cache/agent-smith/bin/`; on miss/mismatch, download
   `agent-smith_<os>_<arch>.tar.gz` from the matching GitHub release and unpack.
3. `duckdb`: any PATH hit **≥ the minimum version floor (1.0.0)** wins; else
   fetch the pinned official duckdb CLI asset (`duckdb_cli-linux-<arch>.zip` /
   `duckdb_cli-osx-universal.zip`, initial pin v1.5.3) into the same cache dir.
   Both values live as constants at the top of `bootstrap.sh`.
4. Downloads via `curl -fsSL` on constructed `releases/download/` URLs — no
   `gh`, which requires auth even for public repos.
5. One explicit case-mapping block reconciles naming: `uname -s/-m`
   (`Linux/Darwin`, `x86_64/aarch64`) → GoReleaser (`linux/darwin`,
   `amd64/arm64`) → duckdb (`linux/osx`; `osx-universal` on macOS avoids a
   mac-arch branch).
6. Race-safe: download to a temp dir, atomic `mv` into place.
7. Idempotent and quiet when satisfied (one "✓" line); actionable error on
   download failure. `shellcheck`-clean.

**Update flow:** plugin update → new `plugin.json` version → next run's check
misses → re-download. Binaries stay in lockstep with the command/agent prompts
that drive them.

macOS note: `curl` doesn't set the quarantine xattr, so Gatekeeper never
inspects the downloaded binaries. Not a problem to "fix".

### 3. README restructure

Replace the cramped "Run it" paragraph with:

- **Install** — numbered quick start: `/plugin marketplace add noamsto/agent-smith`
  → `/plugin install agent-smith@agent-smith` → `/agent-smith`. Note that
  binaries + duckdb auto-download on first run; only `git`/`gh` assumed.
- Declarative variant (`extraKnownMarketplaces` / `enabledPlugins`) as a proper
  fenced JSON block, secondary to the quick start.
- **Develop** — the `nix develop` / `go test` / `nix build` block moves here.

### 4. Out of scope

Windows, Homebrew tap, auto-update beyond version-pinned re-download, replacing
the duckdb CLI shell-out with Go bindings.

## Alternatives considered

- **Hand-rolled GH Actions matrix** — fewer tools, but we'd own archive naming
  + checksums; GoReleaser is less to maintain and locally testable.
- **`go install` instructions** — requires a Go toolchain; fails "just works".
- **Docs-only (nix remains the only path)** — fails the requirement outright.
- **go-duckdb CGO bindings** — kills CGO=0 static builds; big refactor for no
  user-visible win.
