# glitch-2026-09-10-nix-config-user-correction-skip

**Artifact:** /home/noams/nix-config/CLAUDE.md  
**Signal:** user_correction  
**Fix type:** skip  **Confidence:** high  **Date:** 2026-09-10

## Diagnosis

The cluster does not describe one recurring behavior. 18 of 25 incidents come from a single orchestration session (ad0acc2f); every sampled window from it contains only 'Async agent launched successfully' tool-result metadata and subagent dispatch prompts — no user correction at all, so the extractor appears to have keyed on teammate-message dispatch text rather than on corrective user turns. The same holds for efbb1e4d (both windows are SendMessage/spawn metadata). Only three sampled turns are genuine corrections, and they are three different behaviors: stale git-remote state ('wait, i repushed'), a claim that a flake.lock check covered the mysecrets bump when it did not, and a conclusion about gnome-keyring 50.0 upstream behavior asserted without reading the source. The one thread linking them — asserting a conclusion from inference instead of from freshly-read evidence — is generic, appears at most twice in any single session, and is already mandated outside this artifact.

## Evidence

- acf23afa-bf1e-41eb-ba9a-153748ffdd92:126 ("wait, i repushed")
- acf23afa-bf1e-41eb-ba9a-153748ffdd92:172 ("but maybe not after mysecrets")
- e528f935-f87a-4075-9266-758fbd2afb3c:623 (teammate corrected an unsourced gnome-keyring conclusion)
- 6b8de05e-7469-4818-8207-94adb2283f30:27 (bug report, not a correction)
- ad0acc2f-df6b-4966-ae6d-d9e846f3bba4:3408 (window is subagent-launch metadata; no correction present)
- 5 sessions

## Proposed change

```

```

## Expected effect

skipped: already handled by harness — the only behavior common to the three genuine corrections (assert a conclusion without verifying it against current evidence) is enforced by the Claude Code system prompt's 'Report outcomes faithfully: if tests fail, say so with the output; if a step was skipped, say that', by the org-level floor 'Do not claim a check passed without the output', by the user's global ~/.claude/AGENTS.shared.md rule 'Verify unfamiliar APIs before use — never guess that an import, method, or option exists', and by the superpowers:verification-before-completion skill. The dominant reason to decline, however, is evidence quality rather than redundancy: 18/25 incidents are one session whose windows hold no correction content, so the cluster's 25 total_incidents / 5 distinct_sessions overstates a pattern that is really three unrelated one-off corrections. Writing any rule into nix-config/CLAUDE.md here would either restate the global verify-before-asserting rule (bloat — the failure mode agent-smith exists to cut) or invent a repo-specific flake.lock/git-remote staleness gotcha from two incidents inside a single session, which is below the recurrence bar. Expected effect: no change to the artifact; the (artifact, user_correction) pair is recorded as declined so it is not regenerated, and the extractor's mis-keying of teammate-dispatch prompts as user corrections is flagged for the analyst.

<!-- PR link appended by the applier; outcome appended by deja-vu -->

<!-- outcome: open -->
