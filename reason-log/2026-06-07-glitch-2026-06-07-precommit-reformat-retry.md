# glitch-2026-06-07-precommit-reformat-retry

**Artifact:** /home/noams/nix-config/CLAUDE.md#gotchas  
**Fix type:** add  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

A recurring tool_error+retry loop on `git commit` in this repo: the commit's first attempt fails because pre-commit hooks (alejandra, deadnix, statix, prettier) reformat or flag files (`Exit code 1 ... files were modified by this hook`), forcing the agent to re-stage the rewritten file and re-run the identical commit (incidents 4b6f432d, 020e6f9b). The artifact's `## Skills` line already delegates the flake `git add` gotcha to the `nix-rebuild` skill, but NOTHING in CLAUDE.md mentions that this repo runs auto-formatting pre-commit hooks that mutate files on the first commit — so each session re-discovers it as a failed-commit round-trip.

## Evidence

- 3c83cfb8-a25e-4749-a9f5-3911d4628e78:16
- 2e81ea1d-e6e0-431f-bbcd-483dcb65b9c4:189
- d78c25f0-0a2a-4e30-9a67-96970d13c28a:58
- 32191522-c314-4178-8056-3a056489eaeb:35
- 33 sessions / 95 incidents

## Proposed change

```
Add a bullet to the `## Gotchas` section of /home/noams/nix-config/CLAUDE.md:

- **Pre-commit hooks reformat files — the FIRST `git commit` will fail.** This repo runs `alejandra`, `deadnix`, `statix`, and `prettier` as pre-commit hooks (see `flake-modules/development.nix`). On any commit touching `.nix`/formatted files, alejandra/prettier rewrite the file and the hook exits non-zero (`files were modified by this hook`); statix/deadnix may flag issues that need a manual fix. Expected workflow, not an error: (1) run the commit, (2) if it fails because a hook reformatted files, `git add` the now-formatted files and re-run the SAME commit — it passes on the second attempt. If statix/deadnix flagged something (e.g. repeated `home.*` keys), fix the source first, then re-stage and commit. Do not treat the first failure as a real problem or change the commit message.
```

## Expected effect

Driven by the tool_error signal: the most consistent novel failure across sessions is `git commit` failing on its first attempt due to formatting pre-commit hooks, followed by a re-stage + retry (4b6f432d shows alejandra reformatting lib/overlays.nix then a successful retry; 020e6f9b shows statix flagging repeated home.* keys). The closely-related uncommitted-changes flake gotcha (eb218fe9, 32191522) is already delegated to the nix-rebuild skill, so adding it would duplicate existing guidance — chose `add` only for the genuinely-uncovered pre-commit behavior. Per the hard rule I confirmed no existing CLAUDE.md guidance addresses pre-commit hooks. Expected effect: the agent anticipates the first-commit failure, performs the re-stage step deliberately instead of as surprised error recovery, and avoids spurious message edits or aborts — cutting one wasted failed round-trip per commit. Confidence medium because the sample also mixes in unrelated one-off tool errors (remote permission denial, missing symlinks, WebSearch missing-query), so the cluster is not purely the pre-commit pattern.

**PR:** https://github.com/noamsto/nix-config/pull/9

<!-- outcome appended by deja-vu -->
