# glitch-2026-06-07-skeleton-first-large-reads-ignored

**Artifact:** /home/noams/.claude/CLAUDE.md#reading-code-skeleton-first  
**Fix type:** escalate-out-of-instructions  **Confidence:** high  **Date:** 2026-06-07

## Diagnosis

The 'Reading Code (skeleton-first)' section already mandates a structure pass (ast-grep / code-structure skill / Grep) before a full Read of files larger than ~300 lines, yet across 78 distinct sessions and 127 incidents the agent issues an unscoped full Read on files of 1000-2400+ lines anyway, frequently without any prior signature/Grep pass. The rule exists, is clear, and is imperative, but is being routinely ignored across two model generations (opus-4-7 and opus-4-8) — a prose-only rule failing at scale. Strengthening the wording will not change behavior that already violates clear, bolded guidance; the error needs to be made structurally hard, not merely discouraged.

## Evidence

- 79cf9d4e:11 (full Read of handler.go, 1083 lines)
- 715cfb68:61 (full Read of handler.go, 1131 lines)
- cdf1eddd:156 (full Read of service.go, 1139 lines)
- 2bc27123:86 (full Read of service.go, 1128 lines)
- dd05131d:42 (full Read of plan.md, 1493 lines)
- 9704fed1:27 (full Read of /tmp/decouple-plan.md, 1119 lines)
- 9bdff62e:25 (full Read of theme-toggle.sh, 1519 lines)
- 4a9928f9:37 (full Read of pr1938.diff, 2437 lines, then paged)
- 78 sessions, 127 incidents

## Proposed change

```
Keep the prose section but stop relying on it alone — add a PreToolUse(Read) enforcement hook so an unscoped full Read of a large file becomes impossible-by-default rather than discouraged.

Location (per CLAUDE.md 'Settings Architecture': hooks reference /nix/store paths, so they live in the --settings overlay, NOT settings.json):
  - Define the hook script in home/ai/claude-code/default.nix and wire it via nix-settings-json under hooks.PreToolUse with a matcher of "Read".

Hook sketch (script the nix derivation installs, invoked with the tool input JSON on stdin):
  #!/usr/bin/env bash
  # nix: skeleton-first guard — blocks unscoped full Reads of large files
  input=$(cat)
  path=$(jq -r '.tool_input.file_path' <<<"$input")
  offset=$(jq -r '.tool_input.offset // empty' <<<"$input")
  limit=$(jq -r '.tool_input.limit // empty' <<<"$input")
  # only guard whole-file reads (no offset/limit window requested)
  if [ -n "$offset" ] || [ -n "$limit" ]; then exit 0; fi
  case "$path" in *.png|*.jpg|*.jpeg|*.gif|*.webp|*.pdf|*.ipynb) exit 0;; esac
  lines=$(wc -l < "$path" 2>/dev/null || echo 0)
  if [ "$lines" -gt 300 ]; then
    jq -n --arg p "$path" --arg n "$lines" '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: ("skeleton-first: \($p) is \($n) lines (>300). Run ast-grep for signatures, the context-efficient-tools:code-structure skill, or Grep for the symbol first; then Read with offset/limit for the region that matters. Whole-file Read is allowed only for genuine top-to-bottom work — re-issue with a window or override deliberately.")
      }
    }'
    exit 0
  fi

Also update the CLAUDE.md section to point at the guard so the prose and the hook agree (append to the 'Reading Code (skeleton-first)' section):
  > Enforced by a PreToolUse(Read) guard (home/ai/claude-code/default.nix): unscoped full Reads of files >300 lines are denied with a reminder to skeleton first or pass offset/limit.
```

## Expected effect

Signal is inefficiency: the recurring behavior in every sampled window is a full, unscoped Read of a file far larger than the 300-line threshold (1083-2437 lines), often with no preceding structure pass. Because CLAUDE.md ALREADY contains clear, bolded skeleton-first guidance, 'add' is forbidden and 'strengthen' is unlikely to help — the rule is already imperative and is being violated across 78 sessions and two model generations, the textbook trigger for escalate-out-of-instructions. A PreToolUse(Read) hook that denies unscoped whole-file Reads above 300 lines (exempting windowed reads and binary/PDF/notebook formats, which the Read tool handles specially) converts a soft prose preference into a hard default, forcing either a windowed Read or an explicit structure-first pass. Expected effect: large-file context burn drops sharply and the skeleton-first workflow becomes the path of least resistance instead of an ignored suggestion. The hook is placed in the --settings overlay per the user's settings-architecture rule because it references a /nix/store script path.

**PR:** https://github.com/noamsto/nix-config/pull/8

<!-- outcome appended by deja-vu -->
