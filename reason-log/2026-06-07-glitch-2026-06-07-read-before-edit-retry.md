# glitch-2026-06-07-read-before-edit-retry

**Artifact:** /home/noams/Data/git/factify/mono/CLAUDE.md  
**Fix type:** escalate-out-of-instructions  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

The dominant retry pattern in this cluster is the Edit/Write tool failing with "File has not been read yet. Read it first before writing to it.", forcing the agent to issue a Read and re-attempt the same edit. It recurs across ≥6 distinct sessions (and the broader 18-session / 145-incident count), wasting a tool round-trip every time. The implicated artifact CLAUDE.md contains only `See @AGENTS.md`, so a thin prose rule could be added — but a Read-before-write prose instruction is exactly the kind of rule a model already half-knows and keeps violating; adding more prose will not make the error impossible. This is a mechanical precondition best enforced deterministically, not by instruction.

## Evidence

- 57ef5be4:148
- d3d515a2:577
- ba264db9:4772
- 138a9606:636
- 82e841f4:284
- 6b57aaec:2004
- ≥6 sessions

## Proposed change

```
Add a PreToolUse hook for the Edit and Write tools (NOT a prose rule in CLAUDE.md / AGENTS.md). Since CLAUDE.md only re-exports AGENTS.md and hooks reference nix-store paths, this belongs in the repo's hook config, not the instruction file.

Hook sketch (settings hooks block, matcher on Edit|Write):

  {
    "hooks": {
      "PreToolUse": [
        {
          "matcher": "Edit|Write",
          "hooks": [
            { "type": "command",
              "command": "<store-path>/ensure-read.sh" }
          ]
        }
      ]
    }
  }

ensure-read.sh reads the tool input from stdin (JSON: .tool_input.file_path), and for an Edit on an existing file that has not yet been Read in this session, it auto-emits the file contents back into context (or returns a structured "read this first" payload the harness consumes) so the subsequent Edit precondition is satisfied without a model-driven retry. For Write to a non-existent path it is a no-op. Effect: the read-before-write precondition is satisfied mechanically, so the "File has not been read yet" error — and its retry — can no longer occur. Location: the factify/mono hook configuration that feeds Claude Code settings for this repo (the nix-generated --settings overlay if it must reference a /nix/store script path, per the settings-architecture split).
```

## Expected effect

Drove off the `retry` signal: the most consistent, mechanically-fixable retry across the sampled windows is the Edit/Write -> 'File has not been read yet' -> Read -> retry loop (visible in 57ef5be4, d3d515a2, ba264db9, 138a9606, 82e841f4, 6b57aaec). Chose escalate-out-of-instructions because (a) the artifact holds no guidance on this, but (b) a Read-before-write rule is a precondition a model repeatedly fails on even when it 'knows' it, so adding prose would duplicate latent guidance without preventing the error; a PreToolUse hook makes the failure impossible by design (define-the-error-out-of-existence). Expected effect: eliminates the read-before-write retry round-trip across all sessions touching mono. Confidence medium: the read-before-write loop is the clearest fixable sub-pattern, but this cluster also bundles unrelated retries (duplicate `gh pr checks`, `wt switch --create` on existing branch) that the hook does not address.

<!-- PR link appended by the applier; outcome appended by deja-vu -->
