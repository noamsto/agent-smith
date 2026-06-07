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
# exact-match against --version output: the binaries print the bare version (fmt.Println)
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
  v="$("$bin/$tool" --version 2>/dev/null || true)"
  if [ -x "$bin/$tool" ] && { [ "$v" = "$expected" ] || [ "$v" = "dev" ]; }; then
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
    [ -f "$tmp/$tool" ] || { echo "bootstrap: release tarball missing $tool" >&2; exit 1; }
    chmod +x "$tmp/$tool"
    mv -f "$tmp/$tool" "$bin/$tool"
  done
fi

duck="$(command -v duckdb 2>/dev/null || true)"
if [ -n "$duck" ] && [ "$duck" != "$bin/duckdb" ] \
  && [ "$("$duck" --version 2>/dev/null | sed -En 's/^v?([0-9]+)\..*/\1/p')" -ge "$DUCKDB_MAJOR_FLOOR" ] 2>/dev/null; then
  ln -sf "$duck" "$bin/duckdb"
elif [ ! -x "$bin/duckdb" ]; then
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
