---
description: Run the whole agent-smith loop — mine (wide, recency-capped) → propose → apply (draft PRs). Halts at the post-mine checkpoint unless you pass `yes`. Args `repo`/`top N`/`stale`/`yes` forward to mine.
allowed-tools: Bash, Read, Write, Agent, Skill
---

You are orchestrating the **full agent-smith loop**. Execute the three phases in
order by invoking the sibling skills with the Skill tool:

1. **agent-smith:mine** — forward `$ARGUMENTS` verbatim (`repo` / `top N` / `stale` /
   `yes`). **mine always halts at its checkpoint** (it prints the fleet table + cost,
   then blocks) **unless `$ARGUMENTS` contains `yes`**. So a bare `/agent-smith:run`
   stops for your confirmation after mining; `/agent-smith:run yes` runs hands-off
   (scheduled/cron use) — the `--top` cap is the cost bound either way.
2. **agent-smith:propose** (each Oracle proposal then faces a **skeptic** pass that
   refutes it against the real repo; refuted proposals are dropped before assembly)
3. **agent-smith:apply** (no id → every ready group; one PR per artifact group;
   `confidence: low` proposals are dropped by default)

Each phase carries its own step-zero bootstrap; do not skip it. If a phase fails
outright, stop and report; a skipped group inside `apply` is not a failure.

After all phases, print the final report table:
`group_id | repo | proposal ids | verify verdict | PR link or skip reason`.
All PRs are **drafts** — tell the user to review / `nix build` / merge them at
their leisure.
