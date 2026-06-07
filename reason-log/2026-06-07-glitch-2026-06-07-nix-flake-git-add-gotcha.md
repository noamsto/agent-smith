# glitch-2026-06-07-nix-flake-git-add-gotcha

**Artifact:** /home/noams/nix-config/CLAUDE.md#skills  
**Fix type:** escalate-out-of-instructions  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

Across 35 distinct sessions the recurring user-correction pattern centers on the Nix flake 'git add gotcha': the agent edits .nix files and runs a rebuild (nh os/home switch, nix build/flake) while one or more touched files are still untracked, so flake-parts evaluation silently ignores them and the build fails or builds stale source. The CLAUDE.md does NOT contain an inline rule for this — it only delegates to the nix-rebuild skill ('Covers the git add gotcha'). The transcripts show the agent dutifully invoking the nix-rebuild skill and then manually stitching `git add <file>` before each command, yet the correction keeps recurring — a prose pointer that must be re-read and re-applied every turn is fragile and is failing at scale. This is the classic escalate case: prose guidance exists, keeps being followed imperfectly, and the underlying error is mechanically preventable.

## Evidence

- d78c25f0-0a2a-4e30-9a67-96970d13c28a:56
- 2d092f55-545e-467e-9529-2ae4b37b52e8:225
- 32191522-c314-4178-8056-3a056489eaeb:123
- 2e81ea1d-e6e0-431f-bbcd-483dcb65b9c4:145
- 35 sessions

## Proposed change

```
Do NOT add more prose to CLAUDE.md (it already delegates the gotcha to the nix-rebuild skill). Instead make the error impossible with a PreToolUse(Bash) hook scoped to this repo.

Location: register in the Nix-generated `--settings` overlay (`home/ai/claude-code/default.nix`, the `hooks` block) since the hook script lives at a /nix/store path. Ship the script under `home/ai/claude-code/hooks/nix-stage-guard.sh`.

Hook sketch (PreToolUse, matcher: Bash):
- Read the hook JSON from stdin; extract `.tool_input.command`.
- Fire only when cwd is under /home/noams/nix-config AND the command matches a rebuild verb: `nh (os|home) switch`, `nixos-rebuild`, `home-manager switch`, or `nix (build|flake|develop|fmt)`.
- Run `git -C /home/noams/nix-config ls-files --others --exclude-standard -- '*.nix'` plus `git status --porcelain` to find untracked/modified .nix (and other flake-referenced) files.
- If any untracked .nix files exist, return `{"decision":"block","reason":"Untracked .nix files (<list>) — flake eval will ignore them. Run: git add <files> before rebuilding (nix-config git add gotcha)."}` so the rebuild is refused until staging happens.
- Exit 0 / allow when the working tree has no untracked .nix files.

This converts the per-turn prose reminder into a deterministic gate: the rebuild physically cannot run against an unstaged flake, eliminating the recurring correction.
```

## Expected effect

Signal is user_correction on /home/noams/nix-config/CLAUDE.md across 35 sessions / 64 incidents; the dominant observable in the windows is the agent invoking the nix-rebuild skill and manually running `git add <file>` immediately before rebuild/build commands — the footprint of repeatedly fighting the documented flake 'git add gotcha'. Because CLAUDE.md ALREADY references this gotcha (via the nix-rebuild skill bullet), adding a duplicate prose rule is forbidden and would not help — the rule is being read and still failing. A PreToolUse Bash hook that blocks rebuild commands while .nix files are untracked makes the failure mode unreachable rather than merely warned-about, which is the right escalation for a prose rule that fails repeatedly at scale. Confidence is medium because the raw correction text was not directly visible in the sampled windows; the diagnosis is inferred from the consistent skill-then-git-add-then-rebuild pattern tied to the artifact's own documented gotcha.

**PR:** https://github.com/noamsto/nix-config/pull/6

<!-- outcome appended by deja-vu -->
