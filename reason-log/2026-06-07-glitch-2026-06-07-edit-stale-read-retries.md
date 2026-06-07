# glitch-2026-06-07-edit-stale-read-retries

**Artifact:** /home/noams/.claude/CLAUDE.md#editing-files-read-then-edit  
**Fix type:** add  **Confidence:** high  **Date:** 2026-06-07

## Diagnosis

The dominant retry pattern is an Edit rejected with 'File has not been read yet' or 'File has been modified since read', after which the model Reads and re-issues the same Edit — a wasted round-trip on every occurrence. A secondary contributor is batching dependent commands in a single parallel tool block, where one sibling's failure cancels the others ('Cancelled: parallel tool call ... errored'), forcing a full re-run. CLAUDE.md has a 'Reading Code (skeleton-first)' section that discourages full Reads, and 'Bash Command Simplicity', but NO rule on Read-before-Edit freshness or on not parallelizing dependent calls — so neither retry class is addressed.

## Evidence

- 2d62792c-313a-489c-bb83-e7bf68977b04:204
- 57ef5be4-96bd-43c0-a2ce-6b9ede7aa7c1:148
- 8803d8a3-5c1d-4cc4-a088-58d877017267:155
- c1302662-38f9-44b9-9563-7d7a09073f1d:382
- fb912b05-ae76-4a7a-a905-26377c308af1:209
- a476d231-b695-451e-8f95-8c717e2603c5:2266
- c366eebb-7e61-41fd-9edb-df0b80d40917:352
- c50ce45a-607a-4fe5-93cc-cfb6fea12a91:122
- 71 sessions / 255 incidents

## Proposed change

```
Add a new section after '## Reading Code (skeleton-first)':

## Editing Files (read-then-edit, in one beat)

Every `Edit`/`Write` requires a current read of the target. The retry tax — `File has not been read yet` / `File has been modified since read` — is pure waste, so avoid it up front:

- **Read the exact region you're about to edit immediately before editing it.** Don't carry a stale read across many turns: if a build, formatter, linter, codegen, or `git` step ran since your last read of that file, Read it again before the Edit. Skeleton-first still applies to *locating* the region; the freshness rule applies to the *slice you edit*.
- **After a successful Edit, the file state is current** (the tool says so) — don't re-Read just to confirm. Re-Read only when something external could have touched the file since.
- **Never batch dependent steps into one parallel tool block.** A single failing call cancels its siblings (`Cancelled: parallel tool call ... errored`) and forces a re-run. Parallelize only mutually independent, read-only calls (e.g. several `Read`/`Grep`); run anything that mutates state, or that a later call depends on, as its own sequential call.
```

## Expected effect

Drove by the `retry` signal: the sampled windows are overwhelmingly Edit-after-stale-read failures (six of eight shown) plus parallel-call cancellations (two shown), recurring across 71 distinct sessions and 255 incidents — a mechanical, high-frequency waste pattern. Chose `add` over `strengthen` because no existing section covers read-freshness-before-edit or parallel-call dependency (the skeleton-first rule is adjacent but addresses the opposite concern — minimizing reads). Avoided `escalate-out-of-instructions` because the precondition is already harness-enforced; the loss is wasted turns, not incorrect outcomes, so a targeted behavioral rule is the proportionate fix. Expected effect: model re-reads the edit slice after any intervening mutation and stops batching dependent calls, eliminating the read-then-retry round-trip and the cancel-cascade re-runs.

**PR:** https://github.com/noamsto/nix-config/pull/3

<!-- outcome appended by deja-vu -->
