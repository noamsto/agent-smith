# Binary Distribution Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the agent-smith plugin "just work" on marketplace install — GoReleaser publishes CGO=0 release binaries, and a self-locating `bootstrap.sh` resolves or downloads them (plus duckdb) on first run.

**Architecture:** Tag `vX.Y.Z` ⇔ `plugin.json` version ⇔ binary `--version` (CI-enforced). `scripts/bootstrap.sh` materializes `~/.cache/agent-smith/bin` ("$BIN") with downloads or symlinks to PATH-found tools; the `/agent-smith` command prefixes every binary call with `PATH="$BIN:$PATH"`.

**Tech Stack:** Go (stdlib only), GoReleaser v2, GitHub Actions, bash (3.2-compatible), Nix flake.

**Spec:** `docs/specs/2026-06-07-binary-distribution-design.md`

**Branch:** create `feat/binary-distribution` via `wt switch -c feat/binary-distribution` before Task 1 (personal repo, no issue → no id in branch name).

---

### Task 1: `--version` flag in the three binaries

**Files:**
- Modify: `cmd/extractor/main.go`
- Modify: `cmd/analyst/main.go`
- Modify: `cmd/applier/main.go`

Each main package gets `var version = "dev"` (ldflags-stamped at release) and prints it on `--version` as the first arg. No unit test — `main` wiring; verified by running the binaries.

- [ ] **Step 1: extractor**

In `cmd/extractor/main.go`, add the var after the imports and the check as the first lines of `main()`:

```go
var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	cfg := extractor.DefaultConfig()
	// ... (existing body unchanged)
```

- [ ] **Step 2: analyst**

In `cmd/analyst/main.go`, add `var version = "dev"` after the imports, and a case to the existing subcommand switch:

```go
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
	case "cluster":
		runCluster(os.Args[2:])
```

- [ ] **Step 3: applier**

Same as analyst in `cmd/applier/main.go`: `var version = "dev"` after imports, plus:

```go
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
	case "prepare":
		runPrepare(os.Args[2:])
```

- [ ] **Step 4: Verify**

```
go run ./cmd/extractor --version   # → dev
go run ./cmd/analyst --version     # → dev
go run ./cmd/applier --version     # → dev
go test ./...                      # all pass
```

- [ ] **Step 5: Commit**

```bash
git add cmd
git commit -m "feat(cmd): --version flag on extractor/analyst/applier, ldflags-stampable"
```

---

### Task 2: Flake stamps the version; goreleaser in devshell

**Files:**
- Modify: `flake.nix`

- [ ] **Step 1: Read version from plugin.json, stamp via ldflags, add goreleaser**

Replace the `let` binding and the two affected attrs in `flake.nix`:

```nix
      let
        pkgs = import nixpkgs { inherit system; };
        version = (builtins.fromJSON (builtins.readFile ./.claude-plugin/plugin.json)).version;
      in {
        packages.default = pkgs.buildGoModule {
          pname = "agent-smith";
          inherit version;
          src = ./.;
          vendorHash = null; # stdlib only
          subPackages = [ "cmd/extractor" "cmd/analyst" "cmd/applier" ];
          ldflags = [ "-X main.version=${version}" ];
          # ... nativeBuildInputs / nativeCheckInputs / postInstall unchanged
        };

        devShells.default = pkgs.mkShell {
          packages = [ pkgs.go pkgs.gopls pkgs.go-tools pkgs.duckdb pkgs.jq pkgs.git pkgs.gh pkgs.goreleaser ];
        };
      }
```

(`version = "0.1.0";` literal is deleted — plugin.json is now the single source.)

- [ ] **Step 2: Verify**

```
nix build .#default
./result/bin/extractor --version   # → 0.1.0  (matches .claude-plugin/plugin.json)
```

- [ ] **Step 3: Commit**

```bash
git add flake.nix
git commit -m "feat(flake): stamp binary version from plugin.json; goreleaser in devshell"
```

---

### Task 3: GoReleaser config

**Files:**
- Create: `.goreleaser.yaml`
- Modify: `.gitignore` (add `dist/`; create the file if absent)

- [ ] **Step 1: Write `.goreleaser.yaml`**

```yaml
version: 2
project_name: agent-smith

builds:
  - id: extractor
    main: ./cmd/extractor
    binary: extractor
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ldflags: ["-s -w -X main.version={{.Version}}"]
  - id: analyst
    main: ./cmd/analyst
    binary: analyst
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ldflags: ["-s -w -X main.version={{.Version}}"]
  - id: applier
    main: ./cmd/applier
    binary: applier
    env: [CGO_ENABLED=0]
    goos: [linux, darwin]
    goarch: [amd64, arm64]
    ldflags: ["-s -w -X main.version={{.Version}}"]

archives:
  - ids: [extractor, analyst, applier]
    name_template: "agent-smith_{{ .Os }}_{{ .Arch }}"
    formats: [tar.gz]

checksum:
  name_template: checksums.txt
```

The `name_template` is load-bearing: `bootstrap.sh` (Task 5) constructs
`agent-smith_<os>_<arch>.tar.gz` URLs from it. Never change one without the other.

- [ ] **Step 2: Add `dist/` to `.gitignore`**

- [ ] **Step 3: Verify config + snapshot build**

```
nix develop -c goreleaser check
nix develop -c goreleaser release --snapshot --clean
ls dist/   # expect agent-smith_linux_amd64.tar.gz, agent-smith_linux_arm64.tar.gz,
           #        agent-smith_darwin_amd64.tar.gz, agent-smith_darwin_arm64.tar.gz, checksums.txt
tar -tzf dist/agent-smith_linux_amd64.tar.gz   # contains extractor, analyst, applier
```

- [ ] **Step 4: Commit**

```bash
git add .goreleaser.yaml .gitignore
git commit -m "feat(release): goreleaser config — CGO=0 archives for linux/darwin amd64/arm64"
```

---

### Task 4: Release workflow with tag⇔plugin.json guard

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Write the workflow**

```yaml
name: release

on:
  push:
    tags: ["v*"]

permissions:
  contents: write

jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - name: Tag must match plugin.json version
        run: |
          tag="${GITHUB_REF_NAME#v}"
          plugin="$(jq -r .version .claude-plugin/plugin.json)"
          if [ "$tag" != "$plugin" ]; then
            echo "tag v$tag != plugin.json version $plugin" >&2
            exit 1
          fi
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

- [ ] **Step 2: Verify**

```
nix run nixpkgs#actionlint -- .github/workflows/release.yml   # no findings
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/release.yml
git commit -m "feat(ci): release workflow — goreleaser on v* tags, tag must equal plugin.json version"
```

---

### Task 5: `scripts/bootstrap.sh`

**Files:**
- Create: `scripts/bootstrap.sh` (mode 755)

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# Resolves the agent-smith binaries + duckdb into one bin dir and prints it.
# PATH-found tools at the right version are symlinked; anything else is
# downloaded from GitHub releases. Idempotent; safe to run every time.
set -euo pipefail

DUCKDB_PIN="v1.5.3"
DUCKDB_MAJOR_FLOOR=1
RELEASE_BASE="${AGENT_SMITH_DOWNLOAD_BASE:-https://github.com/noamsto/agent-smith/releases/download}"
DUCKDB_BASE="${AGENT_SMITH_DUCKDB_DOWNLOAD_BASE:-https://github.com/duckdb/duckdb/releases/download}"

plugin_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
expected="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$plugin_root/.claude-plugin/plugin.json")"
if [ -z "$expected" ]; then
  echo "bootstrap: cannot read version from $plugin_root/.claude-plugin/plugin.json" >&2
  exit 1
fi

cache="${XDG_CACHE_HOME:-$HOME/.cache}/agent-smith"
bin="$cache/bin"
mkdir -p "$bin"

case "$(uname -s)" in
  Linux) os=linux; duck_os=linux ;;
  Darwin) os=darwin; duck_os=osx ;;
  *) echo "bootstrap: unsupported OS $(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) echo "bootstrap: unsupported arch $(uname -m)" >&2; exit 1 ;;
esac

fetch() { # url dest
  curl -fsSL "$1" -o "$2" || {
    echo "bootstrap: download failed: $1" >&2
    echo "bootstrap: check network, or put the binaries on PATH yourself (nix build .#default)" >&2
    exit 1
  }
}

need=""
for tool in extractor analyst applier; do
  found="$(command -v "$tool" 2>/dev/null || true)"
  if [ -n "$found" ] && [ "$found" != "$bin/$tool" ]; then
    v="$("$found" --version 2>/dev/null || true)"
    if [ "$v" = "$expected" ] || [ "$v" = "dev" ]; then
      [ "$v" = "dev" ] && echo "bootstrap: using local dev build of $tool ($found)" >&2
      ln -sf "$found" "$bin/$tool"
      continue
    fi
    [ -n "$v" ] && echo "bootstrap: $tool on PATH is $v, want $expected — using release binary" >&2
  fi
  if [ -x "$bin/$tool" ] && [ ! -L "$bin/$tool" ] \
    && [ "$("$bin/$tool" --version 2>/dev/null || true)" = "$expected" ]; then
    continue
  fi
  need="$need $tool"
done

if [ -n "$need" ]; then
  tmp="$(mktemp -d "$cache/tmp.XXXXXX")" # same fs as $bin → atomic mv
  trap 'rm -rf "$tmp"' EXIT
  echo "bootstrap: downloading agent-smith v$expected (${os}/${arch})" >&2
  fetch "$RELEASE_BASE/v$expected/agent-smith_${os}_${arch}.tar.gz" "$tmp/agent-smith.tar.gz"
  tar -xzf "$tmp/agent-smith.tar.gz" -C "$tmp"
  # shellcheck disable=SC2086 # $need is an intentional space-separated list (bash-3.2: no arrays under set -u)
  for tool in $need; do
    chmod +x "$tmp/$tool"
    mv -f "$tmp/$tool" "$bin/$tool"
  done
fi

duck="$(command -v duckdb 2>/dev/null || true)"
if [ -n "$duck" ] && [ "$duck" != "$bin/duckdb" ] \
  && [ "$("$duck" --version 2>/dev/null | sed -En 's/^v?([0-9]+)\..*/\1/p')" -ge "$DUCKDB_MAJOR_FLOOR" ] 2>/dev/null; then
  ln -sf "$duck" "$bin/duckdb"
elif [ ! -x "$bin/duckdb" ] || [ -L "$bin/duckdb" ]; then
  tmp="${tmp:-$(mktemp -d "$cache/tmp.XXXXXX")}"
  trap 'rm -rf "$tmp"' EXIT
  asset="duckdb_cli-${duck_os}-${arch}.gz"
  [ "$duck_os" = osx ] && asset="duckdb_cli-osx-universal.gz"
  echo "bootstrap: downloading duckdb $DUCKDB_PIN" >&2
  fetch "$DUCKDB_BASE/$DUCKDB_PIN/$asset" "$tmp/duckdb.gz"
  gunzip -c "$tmp/duckdb.gz" > "$tmp/duckdb"
  chmod +x "$tmp/duckdb"
  mv -f "$tmp/duckdb" "$bin/duckdb"
fi

echo "bootstrap: ✓ $expected" >&2
echo "$bin"
```

Notes locked by the spec: bash-3.2-compatible (macOS), no `jq`/`unzip`/`gh`
dependencies, atomic `mv` from a same-filesystem temp dir, stdout is ONLY the
bin dir (everything else → stderr). The `elif` condition re-fetches duckdb when
the cache entry is a now-dangling symlink (e.g. after nix GC).

- [ ] **Step 2: shellcheck**

```
shellcheck scripts/bootstrap.sh   # zero findings
chmod +x scripts/bootstrap.sh
```

- [ ] **Step 3: Smoke test A — PATH hit (symlink path, no network)**

```bash
nix build .#default
env XDG_CACHE_HOME=/tmp/as-test-a PATH="$PWD/result/bin:$PATH" scripts/bootstrap.sh
# expect stderr "bootstrap: ✓ 0.1.0", stdout "/tmp/as-test-a/agent-smith/bin"
ls -l /tmp/as-test-a/agent-smith/bin   # extractor/analyst/applier/duckdb all symlinks
```

- [ ] **Step 4: Smoke test B — download path via file:// (no real release needed)**

```bash
mkdir -p /tmp/as-fake/v0.1.0
CGO_ENABLED=0 go build -ldflags "-X main.version=0.1.0" -o /tmp/as-fake/build/ ./cmd/...
tar -czf /tmp/as-fake/v0.1.0/agent-smith_linux_amd64.tar.gz -C /tmp/as-fake/build extractor analyst applier
env XDG_CACHE_HOME=/tmp/as-test-b PATH=/usr/bin:/bin \
  AGENT_SMITH_DOWNLOAD_BASE=file:///tmp/as-fake scripts/bootstrap.sh
# expect "downloading agent-smith v0.1.0", then ✓; binaries are real files:
/tmp/as-test-b/agent-smith/bin/extractor --version   # → 0.1.0
```

(With `PATH=/usr/bin:/bin`, `duckdb` is also absent → exercises the duckdb
download against the real duckdb release; skip that assertion if offline.)

- [ ] **Step 5: Re-run test A command — second run is quiet and instant (idempotence)**

- [ ] **Step 6: Commit**

```bash
git add scripts/bootstrap.sh
git commit -m "feat(plugin): self-locating bootstrap.sh — resolve or download binaries + duckdb into one bin dir"
```

---

### Task 6: Wire bootstrap into the `/agent-smith` command

**Files:**
- Modify: `commands/agent-smith.md`

- [ ] **Step 1: Rewrite the intro paragraph**

Replace lines 7–12 (the opening paragraph) with:

```markdown
You are orchestrating the **agent-smith** loop. The deterministic steps are the
`extractor`/`analyst`/`applier` binaries; the judgement steps are the bundled
subagents `agent-smith:oracle` and `agent-smith:editor`, which you dispatch
with the Agent tool. All intermediate artifacts live in the cwd:
`incidents.db`, `clusters.json`, `proposals.json`, `apply-plan.json`, and
`reason-log/`.

**Step zero, always:** run the plugin's `scripts/bootstrap.sh` and capture its
stdout (one line) as `$BIN`. The script lives under this command's plugin root —
the base directory of this skill (`<base>/scripts/bootstrap.sh`); in a dev
checkout of agent-smith it is `./scripts/bootstrap.sh`. If neither exists, find
it: `ls -t ~/.claude/plugins/cache/agent-smith/agent-smith/*/scripts/bootstrap.sh | head -1`.
Bootstrap resolves or downloads the binaries (and duckdb) — every
`extractor`/`analyst`/`applier` invocation below MUST be prefixed with
`PATH="$BIN:$PATH"` (each Bash call is a fresh shell; the prefix also lets the
binaries find `duckdb`). If bootstrap itself fails, stop and show its error.
```

- [ ] **Step 2: Verify nothing else asserts "on PATH"**

```
rg -n "on PATH" commands/agent-smith.md   # no hits left
```

- [ ] **Step 3: Commit**

```bash
git add commands/agent-smith.md
git commit -m "feat(plugin): /agent-smith bootstraps binaries via scripts/bootstrap.sh (step zero)"
```

---

### Task 7: README restructure

**Files:**
- Modify: `README.md` (the `## Run it` section, lines 124–143)

- [ ] **Step 1: Replace `## Run it` with Install + Develop**

```markdown
## 📦 Install

agent-smith is a **Claude Code plugin** (this repo doubles as its own
single-plugin marketplace):

```
/plugin marketplace add noamsto/agent-smith
/plugin install agent-smith@agent-smith
```

Then run it:

```
/agent-smith              # the whole loop, autonomously → draft PRs
/agent-smith mine         # extractor → clusters
/agent-smith propose      # Oracle per cluster → proposals (review-only)
/agent-smith apply [<id>] # editor → verify → draft PR
/agent-smith status       # where things stand
```

First run bootstraps everything: the `extractor`/`analyst`/`applier` binaries
(and the `duckdb` CLI, if you don't have one) download automatically for your
OS/arch into `~/.cache/agent-smith/bin`. The only assumptions are `git` and an
authenticated `gh`. Binaries already on PATH — e.g. nix-installed — are used
as-is, never downloaded over.

<details>
<summary>Declarative install (settings.json)</summary>

```json
{
  "extraKnownMarketplaces": {
    "agent-smith": { "source": { "source": "github", "repo": "noamsto/agent-smith" } }
  },
  "enabledPlugins": { "agent-smith@agent-smith": true }
}
```

</details>

## 🔧 Develop

```bash
nix develop                            # devshell: go, duckdb, jq, git, gh, goreleaser
go test ./...                          # full suite
nix build .#default                    # → result/bin/{extractor,analyst,applier}
goreleaser release --snapshot --clean  # local release dry-run → dist/
```
```

- [ ] **Step 2: Verify rendering** — skim `README.md` top-to-bottom; the `<details>` block needs the blank lines exactly as shown to render fenced code inside it.

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs(readme): install quick-start (auto-bootstrapped binaries) + develop section"
```

---

### Task 8: Merge, tag v0.1.0, verify end-to-end

**Files:** none new — release mechanics.

- [ ] **Step 1: Merge the branch** (`wt merge`, or PR per preference — ask the user)

- [ ] **Step 2: Tag and push — CONFIRM WITH USER FIRST (publishes a release)**

```bash
git tag v0.1.0    # on the merge commit on main; plugin.json already says 0.1.0
git push origin main v0.1.0
```

- [ ] **Step 3: Watch the workflow, then verify assets**

```bash
gh run watch
gh release view v0.1.0 --json assets --jq '.assets[].name'
# expect 4 tarballs + checksums.txt, names matching agent-smith_<os>_<arch>.tar.gz
```

- [ ] **Step 4: Real end-to-end bootstrap (no overrides, no PATH binaries)**

```bash
env XDG_CACHE_HOME=/tmp/as-e2e PATH=/usr/bin:/bin scripts/bootstrap.sh
/tmp/as-e2e/agent-smith/bin/extractor --version   # → 0.1.0
```

- [ ] **Step 5: Close the loop** — run `/agent-smith status` in a fresh session to confirm step-zero bootstrap works through the command path.

---

## Self-review notes

- Spec §1 (pipeline) → Tasks 3–4; §2 (bootstrap) → Tasks 5–6; §3 (README) → Task 7; version invariant → Task 4 guard + Task 8 ordering; flake stamping → Task 2; `--version` → Task 1.
- The `name_template` ↔ bootstrap URL coupling is called out in both Task 3 and the script comment.
- Smoke test B builds its own correctly-stamped tarball rather than using goreleaser snapshot artifacts (snapshot versions don't match `plugin.json`, which would trip the version check on re-runs).
