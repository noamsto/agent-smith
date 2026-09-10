# glitch-2026-09-10-mono-agents-tool-error-no-diagnosable-failure

**Artifact:** /home/noams/Data/git/factify/mono/AGENTS.md  
**Signal:** tool_error  
**Fix type:** skip  **Confidence:** medium  **Date:** 2026-09-10

## Diagnosis

No diagnosable failure mode exists in this cluster, and nothing in it is attributable to the root AGENTS.md. The cluster carries 61 incidents across 10 sessions, but every incident's `detail` is only `{tool, tuid}` with no error payload, and the sampled windows do not corroborate the `tool_error` label: not one of the 25 windows contains `is_error":true`. Of the 21 tool_result turns in the sample, 9 are short enough to be untruncated, and all 9 are non-errors (`is_error":false` at 2c268470:1007, 12199553:367/369/315, or benign `tool_reference` results at 10153d5e:662/645). Even 12199553:315, whose text reads `kill: (2458362) - No such process`, is a non-error result from a `kill ...; gtrash put ...; git status` chain that returned `CLEAN`. The two windows that are legible as behavior are both cases where the harness already produced the right outcome: at e5220c05:698 the permission classifier blocked a spend-authorizing `--ignore-budget` flag and the agent explicitly declined to route around it, and at f454ccb3:214 the harness injected its own `Shell cwd was reset to ...` notice, which the agent obeyed by prefixing absolute-path `cd` into subsequent Bash calls (2c268470:1006, and likewise in incidents 2, 18, 22). The artifact is implicated by cwd, not by any rule: it is the repo-root instruction file for every session in `factify/mono` (note `redirect_from: .../CLAUDE.md`), so an artifact-keyed `tool_error` bucket collects that repo's whole session population regardless of cause. AGENTS.md contains no guidance on shell cwd, absolute paths, or permission prompts, so the tempting `add` here would be a rule about behavior the harness already announces and the agent already followed in every sampled window.

## Evidence

- e5220c05:698
- f454ccb3:214
- 2c268470:1007
- 12199553:367
- 12199553:369
- 12199553:315
- 10153d5e:662
- 10153d5e:645
- 10 sessions

## Proposed change

```

```

## Expected effect

skipped: already handled by harness — the only two legible behaviors in the sample are enforced by the harness itself, not by prose: the Bash permission classifier denied the spend-authorizing `--ignore-budget` flag (e5220c05:698, agent correctly refused to work around it), and the harness's own `Shell cwd was reset to ...` injection drove the correct absolute-path/`cd`-prefix adaptation (f454ccb3:214 -> 2c268470:1006). Writing either into AGENTS.md would restate a harness guarantee, which the Oracle hard rules forbid as bloat. Beyond that, the `tool_error` signal is unsubstantiated: incident `detail` carries no error text at all, 0 of 25 windows show `is_error":true`, and 9 of 9 untruncated tool_results are non-errors — so there is no failure mode to write a rule against, and this artifact is implicated only because it is the repo-root file shared by all 10 sessions (`redirect_from` CLAUDE.md). Confidence is medium rather than high because 12 of the 21 sampled tool_result excerpts are cut at the 300-char window cap before the `is_error` flag, so a minority could be genuine errors; that does not change the decision, since no legible error text means no concrete rule could be drafted, and 61 incidents keyed to a repo-root artifact with empty error details is a sign of attribution breadth rather than of a recurring glitch. Expected effect: no edit to AGENTS.md, root guidance stays compact per its own 'Guide maintenance' section, and the analyst's deja-vu memory on (AGENTS.md, tool_error) suppresses regeneration of this cluster. Recommended follow-up for agent-smith itself, not for this artifact: have the extractor record the tool_result error string in `detail` and stop keying `tool_error` clusters to a repo-root instruction file when no rule in it is implicated.

<!-- PR link appended by the applier; outcome appended by deja-vu -->

<!-- outcome: open -->
