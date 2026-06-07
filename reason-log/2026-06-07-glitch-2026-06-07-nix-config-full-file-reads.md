# glitch-2026-06-07-nix-config-full-file-reads

**Artifact:** /home/noams/nix-config/CLAUDE.md#reading-code  
**Fix type:** add  **Confidence:** high  **Date:** 2026-06-07

## Diagnosis

Across 12 distinct sessions, work in nix-config repeatedly opens large files (1519, 640, 545, 375, 367, 323 lines) with a full Read before locating the relevant region, burning context on whole-file loads when the edit touches one slice. A skeleton-first reading rule exists in the user's GLOBAL ~/.claude/CLAUDE.md, but the nix-config project CLAUDE.md has no such guidance, so the global rule is being silently ignored when this repo's task-specific instructions dominate attention. The artifact's own structure (Skills loaded on demand, modular file map) shows the repo values targeted lookups, but never states a reading discipline.

## Evidence

- 9bdff62e-7bf1-4323-b812-7c5aab35b0be:25 (Read theme-toggle.sh, 1519 lines, full)
- 2d092f55-545e-467e-9529-2ae4b37b52e8:22 (Read noctalia/default.nix, 640 lines, full)
- a9a528b2-e486-4f43-84b9-43705083d0a4:28 (Read noctalia/default.nix, 640 lines, full)
- 03d5c0e6-3bc0-4e61-b288-7e9c2b8739ef:20 (Read fish/default.nix, 545 lines, full)
- 9cc58a52-bbe0-4d1c-8ed5-0d963b08ccbc:30 (Read BTRFS-MIGRATION.md, 367 lines, full)
- c7a1c9a6-31cf-4756-bd5b-212c81450caf:18 (Read terminal/default.nix, 323 lines, full)
- afa97acb (Read wt.fish, 375 lines, full)
- 12 distinct sessions

## Proposed change

```
Add a new section after the '## Skills' section (and before '## Architecture Overview'):

## Reading Code in This Repo

This repo has several large modules — `home/terminal/fish/default.nix` (~545 lines), `home/desktop/noctalia/default.nix` (~640 lines), `home/desktop/hyprland/scripts/sh/theme-toggle.sh` (~1500 lines). **Locate before you load.**

- For any file over ~300 lines, find the target region first: `Grep`/`rg` for the option, attr-set, or function you care about, or run the `context-efficient-tools:code-structure` skill — *then* `Read` only that slice with `offset`/`limit`.
- Full `Read` is fine for short modules (<300 lines) and the small `default.nix` entry points listed in 'Important Configuration Files'.
- The directory map and 'Common Patterns' above usually tell you which file owns a feature; jump straight there instead of reading siblings to orient.

Why: most edits here touch one option block or one `lib.optionals` list; loading a whole 600-line module to change three lines wastes the context budget.
```

## Expected effect

Inefficiency signal: 12/12 sampled incidents are full Reads of files well over the 300-line threshold, recurring across 12 distinct sessions — a strong, consistent pattern. Chose 'add' rather than 'strengthen' because THIS artifact (nix-config CLAUDE.md) contains no reading-discipline guidance at all; the existing skeleton-first rule lives only in the separate global ~/.claude/CLAUDE.md, which the hard-rule branch does not cover (I may only edit this artifact, and it has no such rule to strengthen). Did not choose escalate-out-of-instructions: a Read of a large file is not blockable by a hook without breaking legitimate whole-file work (audits, the small entry-point files), so a targeted prose rule naming the actual large modules is the right lever. Expected effect: agents Grep/structure-scan the named large modules and Read with offset/limit, cutting per-session context spend on nix-config edits.

**PR:** https://github.com/noamsto/nix-config/pull/4

<!-- outcome appended by deja-vu -->
