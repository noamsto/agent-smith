---
description: Run the whole agent-smith loop autonomously — mine session glitches, diagnose fixes (Oracle), open draft PRs (Editor). Scoped to the current repo by default; pass `all` for cross-repo.
allowed-tools: Bash, Read, Write, Agent, Skill
---

You are orchestrating the **full agent-smith loop**. Execute the three phases in
order by invoking the sibling skills with the Skill tool, each to completion:

1. **agent-smith:mine** (pass `$ARGUMENTS` through — bare = scoped to the repo
   you're launched in; `all` = cross-repo, which pauses after mine for a scope
   decision before continuing)
2. **agent-smith:propose**
3. **agent-smith:apply** (no id → every ready group; one PR per artifact group)

Each phase carries its own step-zero bootstrap; do not skip it. If a phase fails
outright, stop and report; a skipped group inside `apply` is not a failure.

After all phases, print the final report table:
`group_id | repo | proposal ids | verify verdict | PR link or skip reason`.
All PRs are **drafts** — tell the user to review / `nix build` / merge them at
their leisure.
