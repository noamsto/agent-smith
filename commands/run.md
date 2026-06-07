---
description: Run the whole agent-smith loop autonomously — mine session glitches, diagnose fixes (Oracle), open draft PRs (Editor).
allowed-tools: Bash, Read, Write, Agent, Skill
---

You are orchestrating the **full agent-smith loop**. Execute the three phases in
order by invoking the sibling skills with the Skill tool, each to completion:

1. **agent-smith:mine**
2. **agent-smith:propose**
3. **agent-smith:apply** (no id → every ready proposal)

Each phase carries its own step-zero bootstrap; do not skip it. If a phase fails
outright, stop and report; a skipped proposal inside `apply` is not a failure.

After all phases, print the final report table:
`proposal_id | repo | fix_type | verify verdict | PR link or skip reason`.
All PRs are **drafts** — tell the user to review / `nix build` / merge them at
their leisure.
