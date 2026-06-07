# glitch-2026-06-07-wrong-worktree-paths

**Artifact:** /home/noams/Data/git/factify/mono/.worktrees/chore-nango-coding-agent-skill/CLAUDE.md  
**Fix type:** escalate-out-of-instructions  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

Across 4 sessions and 49 incidents the dominant, repeating tool errors are mechanical and worktree-shaped, not knowledge gaps: (1) Read/Bash target absolute paths inside OTHER worktrees (eng-6014, eng-6016) while the session cwd is the chore-nango-coding-agent-skill worktree, yielding repeated 'File does not exist' (the tool result even echoes the cwd back); (2) Edit/Write fire before the file was Read, yielding 'File has not been read yet' then a forced re-Read. The implicated artifact is a one-line pointer ('See @AGENTS.md') with zero relevant guidance, and prose added to it would not survive a model that is already confidently constructing full absolute paths into the wrong tree. A prose rule cannot make these errors impossible; a hook can.

## Evidence

- 4a9928f9-e2fb-4011-83ff-6cf8bbb6cfcb:26-31
- 4a9928f9-e2fb-4011-83ff-6cf8bbb6cfcb:28-31
- ca002fc9-cd89-4304-b870-e4c98c4412a4:103-108
- 32b084c7-cfab-422b-a5be-4d0ea7f31917:518-523
- 32b084c7-cfab-422b-a5be-4d0ea7f31917:548-551
- 86db232c-13d9-41cc-871e-83c583a70d0f:43-48
- 4 sessions

## Proposed change

```
Do NOT add prose to this CLAUDE.md (it is a bare @AGENTS.md pointer and a rule here would be ignored, as the Edit-before-Read and wrong-worktree errors recur despite the model 'knowing' the cwd). Instead add a non-prose guard as a PreToolUse hook in the Nix-generated --settings overlay (home/ai/claude-code/default.nix, the layer that owns hooks per the settings architecture rule), matching Read|Edit|Write|Bash:

Hook sketch (PreToolUse, matcher Read|Edit|Write):
  - Resolve the tool's file_path to an absolute path.
  - If the path contains '/.worktrees/<name>/' where <name> != the basename of the session's project root (i.e. a DIFFERENT worktree than cwd), block with: 'Path is in worktree <name> but this session is in <cwd-worktree>. Re-target the path into the current worktree or `wt switch` first.' This makes the cross-worktree miss impossible rather than a silent ENOENT round-trip.

Secondary guard (PreToolUse, matcher Edit|Write): if file_path was not previously Read in this session, emit a deny with the canonical 'Read it first' message BEFORE the API round-trip — but the higher-value fix is the worktree guard; the Edit-before-Read case is already self-correcting in one turn.

Location: hooks block of the nix-settings-json --settings overlay in home/ai/claude-code/default.nix (NOT settings.json — hooks referencing a /nix/store script path belong to the overlay layer).
```

## Expected effect

Signal is tool_error, 49 incidents over 4 sessions. The most consistent cross-session pattern is wrong-worktree absolute paths (three separate Read attempts ENOENT in 4a9928f9, plus eng-6014/eng-6016 targets from the chore-nango cwd), with Edit/Write-before-Read as the second recurring shape. The artifact carries no relevant guidance, so the hard rule against duplicating existing rules does not bind; but the artifact is a degenerate pointer file and the failure is mechanical and repeats despite model awareness — the definition of an escalate-out-of-instructions case. A PreToolUse worktree-mismatch hook converts a silent ENOENT-and-retry loop into an immediate, actionable block, eliminating the dominant error class instead of restating it as prose. Marked medium because the cluster mixes several distinct error shapes (also SendMessage summary, MCP arg validation, Notion permissions) that one hook will not all cover; the worktree guard addresses the largest and most repeated slice.

<!-- PR link appended by the applier; outcome appended by deja-vu -->
