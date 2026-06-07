# glitch-2026-06-07-claudemd-redirect-stub-no-correction-anchor

**Artifact:** /home/noams/Data/git/factify/mono/CLAUDE.md  
**Fix type:** add  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

CLAUDE.md for the mono repo contains nothing but the redirect line `See @AGENTS.md`. It carries zero repo-local behavioral guidance, so the 308 recurring user-corrections across 42 sessions (skill templates that ship broken commands the user/agent must hand-patch e.g. `awk '{print}'` missing `$1`; mid-task tool rejections then manual re-direction such as 'add asaf shitrit and shay to wave 1 and 3'; repeated re-issuing of /simplify+/deslop) have no anchor in this file to be encoded against. Because the file is a bare pointer, the recurring fact is not a wrong rule but the absence of any correction-capturing surface in the repo's own CLAUDE.md. The signal is user_correction, the file currently holds no guidance on the corrected behaviors, so `add` is the correct branch (no existing rule is being duplicated).

## Evidence

- d5372d79:78
- 45238d20:8
- a159b1af:73
- 82e841f4:311
- d3d515a2:593
- 715cfb68:385
- 79aefe98:374
- 91c5c151:414
- ≥42 sessions, 308 incidents

## Proposed change

```
Replace the bare redirect with a redirect that still defers substance to AGENTS.md but adds a thin repo-local section that captures the two behaviors driving the corrections — without copying AGENTS.md prose:

--- a/CLAUDE.md
+++ b/CLAUDE.md
@@
-See @AGENTS.md
+See @AGENTS.md
+
+## Repo-local guardrails
+
+These do not duplicate @AGENTS.md — they encode corrections that keep recurring in this repo.
+
+- **Verify skill/command templates before executing them.** Several committed skill templates (e.g. `commit-commands:clean_gone`) ship shell snippets with bugs such as `awk '{print}'` missing `$1`. When a launched skill hands you a command, read it and fix obvious shell mistakes before running — do not execute the template verbatim and do not silently retry the broken form.
+- **Confirm membership / wave / assignment lists with the user before acting on them.** Branch-cleanup, team-wave, and reviewer-assignment steps are repeatedly corrected after the fact (`add asaf shitrit and shay to wave 1 and 3`). When a step enumerates people, branches, or PRs to act on, surface the resolved list and wait for confirmation before the destructive/dispatch call.
```

## Expected effect

Driven by the user_correction signal at extreme volume (42 sessions / 308 incidents) against an artifact that is a pure `See @AGENTS.md` stub. Hard rule forbids editing AGENTS.md (not this artifact) and forbids `add` when guidance already exists — but CLAUDE.md holds no guidance whatsoever, so `add` is the only valid branch and there is nothing to duplicate. I deliberately scoped the addition to the two concrete, observable correction patterns in the windows (broken skill-template commands being run/retried verbatim; people/wave/assignment lists corrected after dispatch) rather than dumping broad prose into a redirect file. Expected effect: gives the repo a local anchor so the recurring template-bug and confirm-the-list corrections stop repeating, while keeping AGENTS.md as the single source for everything else. Confidence is medium, not high: the 308 incidents are diffuse and the cluster implicates a redirect stub, so the true root cause for some incidents likely lives in AGENTS.md or the individual skill templates, which this artifact-scoped fix cannot reach.

**PR:** https://github.com/factify-inc/mono/pull/2017

<!-- outcome appended by deja-vu -->
