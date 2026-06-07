# glitch-2026-06-07-edit-before-read-tool-error

**Artifact:** /home/noams/Data/git/factify/mono/.worktrees/eng-6037-retire-document-sharing-slice-from-factapi/CLAUDE.md  
**Fix type:** escalate-out-of-instructions  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

Across 4 sessions and 11 incidents the agent repeatedly issues Edit/Write against a file it has not Read in this session, triggering the hard tool error 'File has not been read yet. Read it first before writing to it.' It then recovers by Reading and retrying — a deterministic, mechanical wasted round-trip. The artifact CLAUDE.md is a bare redirect ('See @AGENTS.md') with no behavioral rules; a prose 'always Read before Edit' rule is exactly the kind of instruction the model already nominally knows yet keeps violating, so adding prose here would neither help (wrong file — guidance lives in AGENTS.md) nor work (prose has already failed implicitly across these sessions). The fix must make the error impossible mechanically, not exhortatively.

## Evidence

- e82e38a3-014c-4e7c-8917-4d1dc410291f:112
- ebb8f02d-92fe-484f-a9b0-feca0a0072d7:124
- ebb8f02d-92fe-484f-a9b0-feca0a0072d7:238
- ebb8f02d-92fe-484f-a9b0-feca0a0072d7:345
- ebb8f02d-92fe-454f-... :744
- a17c97c5-8742-4f6e-b3d3-2d25541ee57b:32
- 4 sessions

## Proposed change

```
Do NOT add prose to this CLAUDE.md (it is only a redirect to AGENTS.md). Instead add a PreToolUse hook that auto-recovers the Edit-before-Read race so the tool error can never surface.

Location: register in the Nix --settings overlay (home/ai/claude-code/default.nix, hooks key — the only layer permitted to hold hook entries per the global settings architecture), since the hook command is a /nix/store path.

Hook sketch (PreToolUse, matcher on Edit|Write|MultiEdit):
  - Read the tool input's file_path.
  - If the harness's read-state for that path in the current session is stale/absent, the hook performs an implicit Read (or returns a 'permissionDecision: deny' with a reason that instructs an immediate Read), so the model Reads-then-Edits in one deterministic step instead of bouncing off the error.

Minimal portable implementation — a small script invoked as:
  hooks.PreToolUse = [{ matcher = "Edit|Write|MultiEdit"; hooks = [{ type = "command"; command = "${pkgs.writeShellScript \"ensure-read-before-edit\" ''
    file=$(jq -r '.tool_input.file_path // empty')
    [ -z \"$file\" ] && exit 0
    # Emit a hookSpecificOutput that forces a Read of \"$file\" before the edit proceeds,
    # eliminating the 'File has not been read yet' round-trip.
  ''}"; }]; }];

The exact recovery mechanism (auto-inject Read vs. deny-with-reason) should match whatever PreToolUse output contract the installed Claude Code version supports; the load-bearing point is that the hook lives in the Nix overlay and converts a recurring runtime tool error into a no-op.
```

## Expected effect

Signal tool_error drove this: 11 incidents / 4 sessions, the majority being the identical 'File has not been read yet' rejection on Edit/Write (turns 112-117, 124-129, 238-243, 345-350, 744-748, 1057-1062, 1135-1138), with a few adjacent stale-cwd Bash path errors. Per hard rules, the artifact already exists but holds zero relevant guidance and is a pure redirect, so 'add' is both wrong-target and the known-failing prose remedy; the behavior is mechanical and repeats every session, which is the defining trigger for escalate-out-of-instructions. A PreToolUse hook that guarantees the file is Read before an Edit/Write makes the error impossible (Define errors out of existence), eliminating ~one wasted assistant turn per occurrence. Confidence medium rather than high because the precise PreToolUse output contract for auto-recovery is version-dependent and must be verified against the installed Claude Code before wiring.

<!-- PR link appended by the applier; outcome appended by deja-vu -->
