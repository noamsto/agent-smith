# glitch-2026-06-07-noisy-user-correction-cluster-global-claudemd

**Artifact:** /home/noams/.claude/CLAUDE.md#safe-file-deletion  
**Fix type:** strengthen  **Confidence:** low  **Date:** 2026-06-07

## Diagnosis

This cluster (user_correction → global CLAUDE.md, 1282 incidents / 178 sessions) is highly heterogeneous and mostly mis-attributed: the sampled detail.text fields are dominated by skill-launch boilerplate (superpowers:brainstorming, nix-rebuild, finishing-a-development-branch, commit-commands:clean_gone), fork-boilerplate, and subagent review prompts — none of which are corrections of behavior governed by a specific CLAUDE.md rule. The one genuinely diagnostic user correction in the sample is c1302662: the agent moved to reconcile a factapi DB against a renamed migration ('relation already exists') and the user interrupted/rejected the tool use, an irreversible-state operation outside the file-deletion case already covered. The existing guidance only guards `rm`/destructive git commands; it does not generalize to other irreversible or hard-to-undo actions (DB migrations/resets, force operations) where the same 'ask first' instinct applies.

## Evidence

- c1302662-38f9-44b9-9563-7d7a09073f1d:264
- c1302662-38f9-44b9-9563-7d7a09073f1d:265
- d5372d79-b6ba-47cd-abb4-3d1170e91c2c:78
- 9 sampled sessions, 178 distinct sessions total

## Proposed change

```
Under '## Safe File Deletion', append a generalizing bullet so the irreversible-action guard is not limited to files/git:

- **Before any other irreversible or hard-to-undo action** (database migrate/reset/drop, `--force` pushes, destructive overwrites of live state), pause and confirm with me first — surface what will change and what cannot be recovered, rather than proceeding. Reversible, sandboxed, or read-only actions need no confirmation.
```

## Expected effect

The user_correction signal drove this, but the session-stratified sample shows the cluster is dominated by skill/fork boilerplate and subagent prompts that are not behaviors any single CLAUDE.md rule controls — so the true fixable signal is thin and diffuse. The hard rules forbid `add` where related guidance exists; the only existing rule the genuine corrections touch is Safe File Deletion (the c1302662 interrupt on an irreversible DB-migration reconcile, plus general 'proceed-then-rejected' patterns), so I strengthen that section to generalize beyond files/git rather than inventing a new section. Expected effect: the agent confirms before irreversible state changes, reducing interrupt/reject corrections. Confidence is low because the sample does not cleanly isolate one ignored rule, and a large fraction of the 1282 incidents appear to be extractor mis-attribution of skill-launch boilerplate to the global CLAUDE.md — this cluster likely warrants re-clustering rather than a heavy instruction edit.

**PR:** https://github.com/noamsto/nix-config/pull/7

<!-- outcome appended by deja-vu -->
