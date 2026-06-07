# glitch-2026-06-07-blind-binary-probes

**Artifact:** /home/noams/Data/git/noamsto/lazytmux/CLAUDE.md#build-and-test  
**Fix type:** add  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

Recurring tool_error from blind probing of this repo's binaries and tooling: agents guess flags and TTY behavior that don't hold here (`tmux-state: error: unknown flag: --version`, `tmux-state pick` failing with `could not open TTY` outside a popup), and probe paths/commands by trial-and-error producing exit-code 1/2. CLAUDE.md documents architecture richly but gives no guidance that (a) the `tmux-state` and TUI binaries are popup/TTY-bound and cannot be exercised from a non-interactive Bash tool, and (b) flags must be verified (`--help`) before invocation. Nothing in artifact_content addresses tool-invocation behavior, so this is net-new guidance.

## Evidence

- 211c3d0a-efd2-408b-a974-a9cf2e0a5e85:72
- f2ad5712-66f9-42e5-b6e3-d5b9312634e5:55
- b8f3d12f-328f-4441-8206-56a959b5bf9a:17
- 209de575-b2f9-467b-baaa-09a7adcdf7e4:9
- 26f304f1-7520-407f-9360-07027bb2d4b2:196
- 5 of 15 sessions

## Proposed change

```
Append to the '## Build and Test' section:

### Invoking the binaries from an agent shell

The TUI/picker binaries are TTY-bound and will not run from a non-interactive Bash tool:

- `tmux-state pick`, `tmux-session-picker`, `tmux-window-picker`, and `tmux-picker-generate --tui` open a bubbletea TUI and abort with `could not open TTY: open /dev/tty: no such device or address` unless launched inside a real tmux popup. Do **not** probe them from the Bash tool to "see what they do" — read the source or run the non-`--tui` generation path instead.
- `tmux-state` is a subcommand CLI with **no `--version` flag** (`unknown flag: --version`); run `tmux-state --help` (or `tmux-state <cmd> --help`) to confirm a flag exists before invoking it. Same for the wrapper scripts — verify flags via `--help` rather than guessing.
- Exercise pure logic through the test suites (`nix flake check` runs `tests/enrich.bats`); the display path is manual (`./tests/test-display.sh` after `nix build .#default`).
```

## Expected effect

signal_type=tool_error drove this. The incident sample is heterogeneous (Skill, shellcheck, build failures appear once each), but the consistent, instruction-addressable subset across 5 distinct sessions is blind probing of repo binaries with unsupported flags and TTY-bound pickers run headlessly. CLAUDE.md currently has zero tool-invocation guidance, so `add` (not `strengthen`) is correct. Documenting that the pickers need a TTY/popup and that `tmux-state`/scripts lack `--version` (use `--help` first) makes the most repeated probes unnecessary, cutting the exit-1/2 and `could not open TTY` errors. Confidence medium: the cluster mixes unrelated one-off tool errors, so the fix targets the dominant fixable pattern rather than the whole cluster.

**PR:** https://github.com/noamsto/lazytmux/pull/12

<!-- outcome appended by deja-vu -->
