# glitch-2026-06-07-claudemd-pointer-no-actionable-rule

**Artifact:** /home/noams/Data/git/factify/mono/.worktrees/eng-5883-inbound-slack-impl/CLAUDE.md  
**Fix type:** strengthen  **Confidence:** low  **Date:** 2026-06-07

## Diagnosis

The implicated artifact is a one-line pointer file whose entire content is `See @AGENTS.md` — it carries no behavioral guidance of its own. The clustered incidents are not corrections of any rule this file owns: they are ordinary skill launches (`deslop`, `superpowers:receiving-code-review`) and one unrelated `SendMessage` tool-input error (`summary is required when message is a string`). The `user_correction` signal is misattributed to this artifact; the real guidance, if any rule is needed, lives behind the `@AGENTS.md` include, which this proposal cannot touch. No coherent behavior in the windows maps to a defect in `See @AGENTS.md`, so there is no safe, non-duplicating edit to make to this file.

## Evidence

- f4bdf800-aafd-4b96-9db4-d259faf4f8b0:10
- f4bdf800-aafd-4b96-9db4-d259faf4f8b0:212
- 670b5435-abbc-4043-bacd-53356073b13f:429
- 595bfa87-5e48-4df3-bffa-c8f1dd1fad56:56
- 595bfa87-5e48-4df3-bffa-c8f1dd1fad56:170
- 3 sessions

## Proposed change

```
No change. The artifact is a pure `@AGENTS.md` redirect and is functioning correctly: it loads AGENTS.md as intended. The sampled incidents (skill invocations of deslop/receiving-code-review and a SendMessage `summary is required` tool error) are not governed by this file. Any genuinely needed rule — e.g. always supplying `summary` when SendMessage `message` is a string, or guidance on when to run /deslop — belongs in @AGENTS.md or the relevant skill/tool definition, not in this pointer file. Recommend re-attributing this cluster to AGENTS.md (or splitting the SendMessage tool-error incident into its own cluster against the SendMessage tooling) before proposing an edit.
```

## Expected effect

Hard rule check: the artifact contains no guidance on any of the observed behaviors, so `strengthen` is the closest fit over `add` only because there is genuinely nothing to add to a redirect file — but the honest conclusion is that no edit to THIS artifact is warranted. The `user_correction` signal is driven by incidents (skill launches, a tool-input error) that have no causal link to `See @AGENTS.md`; the include target AGENTS.md is the actual owner and is out of scope for changes to this file. Editing a one-line pointer would add noise without affecting behavior. Expected effect of the recommendation: re-route the cluster to AGENTS.md / the SendMessage tool so a real fix can be proposed where it can take effect. Low confidence reflects the misattribution and the absence of any window tying behavior to this artifact.

<!-- PR link appended by the applier; outcome appended by deja-vu -->
