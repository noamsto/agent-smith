# Analyst golden eval — skeleton-first acceptance bar

Verifies the analyst traces recurring whole-file reads to the EXISTING reading-code
rule and chooses `strengthen` (or `escalate-out-of-instructions`), never `add`.

## Deterministic prep (automatable — run these and check the cluster)

```bash
# absolute path to the fixture CLAUDE.md so it lands in incident candidates
FIX="$(pwd)/fixtures/analyst/CLAUDE.md"

# 1. Build incidents.db from the 3 fixture sessions, pointing the global candidate
#    at the fixture CLAUDE.md (so the cluster reads a file we control).
nix develop -c go run ./cmd/extractor \
  --corpus 'fixtures/analyst/*.jsonl' \
  --global-claude-md "$FIX" \
  --signals inefficiency \
  --out /tmp/analyst-eval.db

# 2. Cluster.
nix develop -c go run ./cmd/analyst cluster --db /tmp/analyst-eval.db --out /tmp/clusters.json --min-sessions 3

# 3. Confirm exactly one actionable cluster, on the fixture CLAUDE.md, content bundled.
nix develop -c jq '.[] | {cluster_id, distinct_sessions, artifact_exists,
  has_rule: (.artifact_content | test("skeleton-first"))}' /tmp/clusters.json
```

Expected: one cluster, `inefficiency::.../fixtures/analyst/CLAUDE.md`,
`distinct_sessions: 3`, `artifact_exists: true`, `has_rule: true`.

## Oracle step (on-demand, manual — dispatch a real subagent)

Dispatch a subagent with the prompt from `internal/analyst/oracle.md` (inline its
content), passing the single object from `/tmp/clusters.json` as the input cluster.
Save its JSON output to `/tmp/proposals-in/p1.json`.

## Assert (the acceptance bar)

```bash
nix develop -c jq -e '.fix_type as $f
  | ($f=="strengthen" or $f=="escalate-out-of-instructions")
  and (.fix_type!="add")
  and (.implicated_artifact | test("CLAUDE.md"))
  and (.proposed_change | length > 0)' /tmp/proposals-in/p1.json && echo "ACCEPTANCE PASS"
```

Then run assembly and confirm the ledger:

```bash
nix develop -c go run ./cmd/analyst assemble --proposals-dir /tmp/proposals-in \
  --out /tmp/proposals.json --reason-log-dir /tmp/reason-log --date 2026-06-01
nix develop -c jq '.[].fix_type' /tmp/proposals.json
ls /tmp/reason-log/
```

Cleanup: `gtrash put /tmp/analyst-eval.db /tmp/clusters.json /tmp/proposals.json /tmp/proposals-in /tmp/reason-log`
