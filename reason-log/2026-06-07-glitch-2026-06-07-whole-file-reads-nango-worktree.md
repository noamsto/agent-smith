# glitch-2026-06-07-whole-file-reads-nango-worktree

**Artifact:** /home/noams/Data/git/factify/mono/.worktrees/chore-nango-coding-agent-skill/CLAUDE.md#reading-code  
**Fix type:** add  **Confidence:** medium  **Date:** 2026-06-07

## Diagnosis

Across 3 sessions the agent issues full-body Read calls on large artifacts before knowing which region it needs: a 2437-line diff (/tmp/pr1938.diff) read in two paginated 1210-line chunks (it even tripped the 25k-token PARTIAL-view cap), a 312-line Gmail JSON example, a 375-line Go source file, and a 304-line proto — burning context that a signature/symbol-targeted read would have saved. The project CLAUDE.md's entire body is `See @AGENTS.md`; it carries NO reading-strategy guidance of its own, so the skeleton-first habit is not reinforced at the repo level where this Nango/proto work happens.

## Evidence

- 4a9928f9-e2fb-4011-83ff-6cf8bbb6cfcb:37
- 4a9928f9-e2fb-4011-83ff-6cf8bbb6cfcb:41
- 86db232c-13d9-41cc-871e-83c583a70d0f:291
- 32b084c7-cfab-422b-a5be-4d0ea7f31917:40
- 86db232c-13d9-41cc-871e-83c583a70d0f:365
- 3 sessions

## Proposed change

```
Append to /home/noams/Data/git/factify/mono/.worktrees/chore-nango-coding-agent-skill/CLAUDE.md so it reads:

```
See @AGENTS.md

## Reading Code (skeleton-first)

For any file or artifact larger than ~300 lines, get **structure before content**: run `ast-grep` for signatures, use the `context-efficient-tools:code-structure` skill, or `Grep` for the exact symbol/section you need — *before* a full `Read`. Pull a region's full body only once you know that region matters.

This applies to generated artifacts too — large diffs, `*-example.json` fixtures, and `.proto` files: `Grep`/`rg` for the message, field, or hunk you care about instead of paging the whole file. A 2400-line diff read end-to-end is the anti-pattern this rule prevents.

Exception: short files (<300 lines) and genuine whole-file work (audit, top-to-bottom review).
```
```

## Expected effect

Driven by the `inefficiency` signal: every incident is a whole-file/large-artifact Read with no preceding structural probe. A global skeleton-first rule exists in ~/.claude/CLAUDE.md, but that is a DIFFERENT artifact and is plainly not being honored in this worktree (the 2437-line diff even hit the token cap). The implicated artifact itself contains zero reading guidance (just `See @AGENTS.md`), so `add` — not `strengthen` — is correct for THIS file. Placing the rule at the repo level, with explicit mention of diffs/JSON fixtures/proto (the exact artifact kinds that recurred), should redirect the agent to targeted Grep/ast-grep probes and cut the per-task context burn. Confidence is medium: one high-confidence incident (the capped diff) plus three low-confidence ones, and the legitimate-whole-file exception means some reads here may have been justified.

**PR:** https://github.com/factify-inc/mono/pull/2020

<!-- outcome appended by deja-vu -->
