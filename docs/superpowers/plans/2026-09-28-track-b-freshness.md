# Track B v1 Freshness Audit — Implementation Plan

**Goal:** Ship `analyst freshness scan|merge`, the `internal/freshness` package, the
`/agent-smith:freshness` skill, and the Oracle/Skeptic `stale-ref` handling specified in
`docs/superpowers/specs/2026-06-22-track-b-freshness-design.md` (the spec — every rule
below cites its section).

**Tech stack:** Go 1.23 stdlib only (module `github.com/noamsto/agent-smith`), DuckDB via
the existing `queryJSON`/`runDuckDB` helpers, markdown command/agent prompts. `go` is
not on the bare PATH: run every Go command as `nix develop -c go …` from the repo root.

**Gate commands** (every step): `nix develop -c go vet ./...` and
`nix develop -c go test ./...`; final: `nix build .` (the flake has no `checks`, CI runs
exactly `go test ./...` in the devshell plus `nix build .`).

## File map

| File | Change |
|---|---|
| `internal/analyst/cluster.go` | shared canonical SQL expr, `Artifacts`, exported `CanonicalizeRepoPrefix`, `Cluster.Evidence`, `TruncateArtifact`, `clusterFileName`, `MergeClusters` |
| `internal/analyst/reasonlog.go`, `escalate.go` | `artifactPath` → exported `ArtifactKey`; `Entry.Evidence` parsed |
| `internal/analyst/freshness_merge_test.go` (new) | `Artifacts`, `MergeClusters`, cluster-after-merge prune, `Entry.Evidence` |
| `internal/freshness/extract.go` (+`_test`) | candidate extraction §4.1 |
| `internal/freshness/classify.go` (+`_test`) | static classification §4.2 |
| `internal/freshness/resolve.go` (+`_test`) | bases, liveness, repo index, gitignore §4.3 |
| `internal/freshness/scan.go` (+`_test`) | audit set, import expansion, `Report` §3.1/§5.1 |
| `internal/freshness/merge.go` (+`_test`) | adjudications, per-ref suppression, `stale-ref` clusters §3.3/§5.2 |
| `internal/freshness/golden_test.go`, `testdata/golden/**` | golden fixture §8 |
| `cmd/analyst/main.go` | `case "freshness"` + `runFreshness` only |
| `agents/oracle.md` | new trailing `## stale-ref clusters` section only |
| `agents/skeptic.md` | one bullet in Procedure step 4 |
| `commands/freshness.md` (new) | the skill |
| `internal/analyst/oracle_test.go`, `internal/plugin/manifest_test.go` | assert new text / command |
| `docs/analyst.md`, `README.md`, `.gitignore` | document; ignore `freshness.json` |

## Steps

Ordering: `S1 ∥ S2` → `S3 ∥ S6` → `S4` → `S5` → `S7`. Steps that run concurrently
share one worktree, so each gates on its **own package** only
(`nix develop -c go vet ./internal/<pkg>/ && nix develop -c go test ./internal/<pkg>/`);
the full `./...` suite runs at each join and in S7. Tests that shell out to `git`
point `HOME` at a temp dir and set `GIT_CONFIG_NOSYSTEM=1` (via `t.Setenv`) so a
developer's global excludes cannot change a verdict. Test repos in every step are real
git repos (`git init -q` — `git` is in the devshell and `nativeCheckInputs`); a bare
`mkdir .git` makes `git check-ignore` exit 128, which S3.7 maps to Ambiguous.

## Consumer map (shared contracts: cluster file/index, `signal_type: stale-ref`, reason-log `Entry`)

| Edge | Symbol / file | Disposition | Proof |
|---|---|---|---|
| Track A producer | `ClusterDB` → `WriteClusters` (cluster.go) | compatible — `evidence` is `omitempty`, files byte-identical | existing cluster/pipeline tests unchanged |
| New producer | `MergeClusters` | changed (new) | S1.10 |
| Index reader | `commands/propose.md` `jq -r '.[].file'` | compatible — stale-ref entries are normal `ClusterIndexEntry` rows with `file` | S1.10 index shape |
| Index readers | `commands/mine.md` checkpoint, `commands/status.md` counts | compatible — mine runs before freshness; status counts entries | code reference |
| Prune | `pruneStaleClusters` via `WriteClusters` | compatible, documented order (spec §3.4) | S1.10 cluster-after-merge |
| Diagnoser | `agents/oracle.md` | changed — new `stale-ref` section | S6.4 oracle_test |
| Verifier | `agents/skeptic.md` step 4 | changed — one sentence | S6.2 (prompt) |
| Validator | `LoadProposals`/`Validate` (assemble.go) | compatible — fix types `fix-stale`/`remove`/`skip` valid, evidence non-empty strings | existing assemble tests |
| #83 citation validator | other worker | compatible — skips `signal_type == stale-ref` by its contract | task doc |
| Ledger writer → reader | `WriteReasonLogs` `- <e>` bullets → `parseEntry` | changed — `Entry.Evidence` parsed | S1.10 |
| Per-ref suppression | `freshness.Clusters` over `Entry` | changed (new) | S4.4 |
| Track A dedup | `FilterRejected` (artifact, signal) | compatible — absent for stale-ref (never called on them) | code reference |
| Unroutable dedup | `Escalate` `known[(artifact, signal)]` (escalate.go:132) | compatible — dedups stale-ref escalations per artifact, as Track A per artifact+signal | existing escalate tests |
| Applier | `prepare.go` resolve + pending dedup, `submit.go` `proposalBody` evidence, `reconcile` | compatible — consume `proposals.json`/reason-log, unchanged shapes | existing applier tests |
| Editor | `agents/editor.md` `fix-stale` | compatible | code reference |
| Apply dir pickup | `commands/apply.md` `ls -dt /tmp/agent-smith-*/` | compatible only if freshness's run dir is outside that glob | S6.3 |
| Failure paths | missing db / no artifacts / missing index / bad adjudication file / unreadable artifact at merge | covered | S3.12, S4.4, S1.10 |

### S1 — analyst plumbing  (`implement: sonnet`)

Files: `internal/analyst/cluster.go`, `reasonlog.go`, `escalate.go`, new
`internal/analyst/freshness_merge_test.go`.

1. Add `func canonicalArtifactExpr(col string) string` returning
   `regexp_replace(regexp_replace(<col>, '/\.worktrees/[^/]+/', '/'), '([^/]+)-worktrees/[^/]+/', '\1/')`;
   make `clusterSQL`'s `exploded` CTE use it on `unnest(CAST(candidates AS VARCHAR[]))`
   (keep the existing comment). Existing cluster tests must pass unchanged.
2. `func Artifacts(ctx context.Context, db string) ([]string, error)`: runs
   `SELECT DISTINCT <expr over unnest(CAST(candidates AS VARCHAR[]))> AS artifact FROM incidents ORDER BY artifact`
   via `queryJSON`, decodes `[{"artifact":…}]`; empty output → nil.
3. Rename `canonicalizeRepoPrefix` → `CanonicalizeRepoPrefix` (update doc comment + the
   one caller).
4. `Cluster` gains `Evidence json.RawMessage \`json:"evidence,omitempty"\`` (after
   `Incidents`), doc-commented as the `stale-ref` per-reference list. Track A files are
   byte-identical (field omitted).
5. `func TruncateArtifact(s string) string { return truncate(s, maxArtifactContentBytes) }`.
6. Extract `clusterFileName(id string) string` (the slugify + fnv naming now inline in
   `WriteClusters`) and `indexEntry(c Cluster, rel string) ClusterIndexEntry`; use both
   in `WriteClusters` (behavior unchanged).
7. `func MergeClusters(clusters []Cluster, indexPath, signalType string) error`:
   read the index at `indexPath` (`os.ErrNotExist` → empty; decode error → return it);
   for every existing entry with `SignalType == signalType` remove
   `filepath.Join(dir, e.File)` if its `filepath.Dir` is `dir/clusters` (ignore
   ErrNotExist) and drop the entry; keep all other entries in order; `MkdirAll`
   `clusters/`; write each new cluster file (same format as `WriteClusters`: indented +
   newline) and append its entry; write the index. Never touches other files.
8. Rename `artifactPath` → `ArtifactKey` (reasonlog.go def + 2 callers, escalate.go 2
   callers); doc comment unchanged in substance.
9. `Entry` gains `Evidence []string`; `parseEntry` collects the `- ` bullets between
   `## Evidence` and the next `## ` heading (text after `- `, right-trimmed).
10. Tests in `freshness_merge_test.go`: `Artifacts` over a `makeIncidentsDB` with
    candidates `/r/CLAUDE.md`, `/r/.worktrees/x/CLAUDE.md`, `/s-worktrees/y/CLAUDE.md`,
    duplicate rows → exactly `["/r/CLAUDE.md","/s/CLAUDE.md"]`; `MergeClusters` into an
    index already holding a Track A entry + file and an old `stale-ref` entry + file →
    Track A entry/file intact, old stale-ref file gone, new one present, index =
    TrackA then new; missing index → created; then `WriteClusters(trackAOnly, index)` →
    stale-ref entry and file gone (spec §3.4); `parseEntry` evidence extraction.

### S2 — extraction + static classification  (`implement: sonnet`)

Files: `internal/freshness/extract.go`, `classify.go`, `extract_test.go`,
`classify_test.go`. Package doc on `extract.go`:
`// Package freshness audits instruction files for references to paths that no longer exist.`

1. Types: `type Form string` with `FormImport = "import"`, `FormLink = "link"`,
   `FormBacktick = "backtick"`; `type Candidate struct { Form Form; Path string; Line int; Text string }`
   (`Text` = trimmed line, used for excerpts and example markers).
2. `func Extract(content string) []Candidate` — per line, 1-based:
   - Fence toggling: a line whose left-trimmed (≤3 spaces) text starts with ```` ``` ````
     or `~~~` opens a fence; it closes on a line starting with the same fence char run.
     Lines inside (and the fence lines) yield nothing.
   - Inline code spans: a run of N backticks up to the next run of exactly N backticks
     on the same line. Span content (trimmed of one surrounding space) with no whitespace
     and non-empty → a `FormBacktick` candidate after stripping a trailing line ref
     `(:\d+(-\d+)?(:\d+)?|#L\d+(-L?\d+)?)$`.
   - `masked` = the line with every code span (delimiters included) replaced by spaces.
   - Imports on `masked`: `(?:^|\s)@(\S+)`; trim trailing `.,;:)` chars; non-empty →
     `FormImport` (Path excludes the `@`).
   - Links on `masked`: `!?\[[^\]]*\]\(\s*(<[^>]*>|[^)\s]+)(?:\s+"[^"]*")?\s*\)`; strip
     `<>`; cut at the first `#`; empty → ignore; else `FormLink`.
   - Dedup identical (Form, Path) on one line.
3. `type Class int` — `Skip`, `Ambiguous`, `Confident`; `func Classify(c Candidate) Class`
   implements §4.2's **static** rules exactly (filesystem rules live in S3):
   - Skip (all forms): contains `://`; prefix `mailto:`/`data:`/`tel:`; any of
     `$*?[]{}<>|='"(),\` or `…` or `...`; prefix `-`; `~` alone or `~` not followed by
     `/`; any `/`-segment whose stem (before first `.`) case-insensitively is `foo`,
     `bar`, `baz`, `qux`, `xxx`; contains `path/to/`; equals `/tmp` or prefix `/tmp/`.
   - Skip (backtick): no `/`; contains `@`.
   - Ambiguous (backtick): `!hasExt(lastSeg) && !HasSuffix("/") && !HasPrefix("./"|"../")`;
     or example marker in `Text` with code spans masked —
     `(?i)\be\.g\.|\bi\.e\.|\bexample|\bsuch as\b` (use the masked text, so a token
     like `examples/x.md` does not match itself).
   - Ambiguous (import): `!hasExt(lastSeg)`.
   - Else Confident.
   - `hasExt(seg)`: seg starts with `.` and len>1 (hidden file), or has a `.` at index
     > 0 followed by 1–10 alphanumerics to the end.
   `Candidate` needs the masked text for the marker check: add `Masked string` to it,
   populated by `Extract`.
4. Tests: table-driven — every skip rule (one row each), each ambiguous rule, confident
   rows for each form; extraction of fences (both kinds), double-backtick spans,
   imports inside code spans ignored, link title/`<>`/fragment handling, line-ref
   stripping for all 6 suffix shapes.

### S3 — resolution + scan  (`implement: opus` — the zero-false-positive contract)

Files: `internal/freshness/resolve.go`, `scan.go`, `resolve_test.go`, `scan_test.go`.
Depends on S1 (`analyst.CanonicalizeRepoPrefix`) and S2.

1. `type artifact struct { Path, Real, Dir, RealDir, Root string }`; `Real` via
   `filepath.EvalSymlinks`; `Root` = nearest ancestor of `RealDir` containing a `.git`
   entry (dir or file), `""` if none.
2. `canonical(p string) string` = `strings.TrimSuffix(analyst.CanonicalizeRepoPrefix(p), "/")`.
3. `type scanner struct { roots []string /* with trailing "/" */; home string; index map[string]*repoIndex; gitOK bool }`.
   `underRoot(p) string` returns the containing root (without slash) or "".
4. `bases(c, a) (paths []string, skip bool)` per spec §4.3:
   - `~/x` → `home/x` first (all forms). `home == ""` → skip.
   - import: abs → `[p]`; relative → `[Dir/p, RealDir/p]`. Never root-skipped.
   - link: leading `/` → `Root == ""` ? skip : `[Root+p]`; relative → `[Dir/p, RealDir/p]`.
   - backtick abs (incl. expanded `~`): `underRoot(p) == "" && underRoot(canonical(p)) == ""` → skip; else `[p, canonical(p)]`.
   - backtick relative: `Root == ""` → skip; `./`/`../` prefix → `[RealDir/p, Dir/p, Root/p]`; else `[Root/p, RealDir/p]`.
   - Dedup, `filepath.Clean` each.
5. `live(c, a, bases) bool`: any base `os.Stat`s; else symbol liveness on each base —
   trim a trailing `:Ident`/`#anchor` (`Ident` = `[A-Za-z_][A-Za-z0-9_]*`, anchor =
   `#[^/]+`) or `.Ident` where Ident is letter-led and not in the known-extension set
   (md go json yaml yml toml nix sh bash fish zsh py ts tsx js jsx mjs cjs rs txt sql
   lock mod sum cfg conf ini html css swift kt java rb lua tmpl tpl env example local
   bak xml csv db log png svg jpg jpeg gif pdf) and re-stat; else, for backtick
   relative refs with `Root != ""`, suffix liveness: normalized token (strip `./`,
   trailing `/`) equals or `HasSuffix("/"+tok)` some path in the repo index.
6. `repoIndex` built lazily per root (only when a ref is missing under every base):
   `filepath.WalkDir`, rel paths of files **and** dirs, not descending `.git`,
   `node_modules`, `vendor`, `.worktrees`, `.direnv`, `result`, `target`, `dist`;
   `byBase map[string][]string`. Walk errors on subtrees are skipped (not fatal).
   `sameName(root, path) []string`: sorted, ≤5, excluding the missing path itself.
7. Missing-ref downgrade, Confident → Ambiguous, in order: backtick with no `./`/`../`
   prefix whose first segment (relative to its root for abs refs) exists under no
   base dir (i.e. `Root/seg` and `RealDir/seg` for relative; `root/seg` for abs);
   then if the first base lies under a known root: `gitOK == false` → Ambiguous;
   `git -C <root> check-ignore -q -- <base rel to root>` exit 0 → Ambiguous, exit 1 →
   stays Confident, any other error → Ambiguous. `gitOK` = `exec.LookPath("git") == nil`.
8. `type Ref struct { ID, Artifact, Form, Path string; Line int; RuleExcerpt, ResolvedTo string; SameName []string }`
   with JSON tags `id artifact form path line rule_excerpt resolved_to same_name`
   (`same_name` always an array). `ID` = first 12 hex of sha256(artifact+"\n"+line+"\n"+path).
   `RuleExcerpt` = `Text` capped at 240 bytes. `ResolvedTo` = `bases[0]`.
9. `type Report struct { Scanned []string; Skipped []Skipped; Dead, AmbiguousMissing []Ref }`
   (tags `scanned skipped dead ambiguous_missing`, all non-nil), `Skipped{Artifact, Reason}`.
10. `type Options struct { Prefix string }`; `func Scan(paths []string, opts Options) Report`:
    sort+dedup `paths`; BFS in sorted order: missing → `Skipped{…,"missing"}`,
    read error → `"unreadable: <err>"`; `visited` keyed on `Real`; each read artifact's
    `FormImport` candidates whose bases' first existing entry is a regular file are
    enqueued (nominal resolved path) — sort each artifact's new imports before
    enqueueing. Then, if `opts.Prefix != ""`, keep only artifacts whose `Path` has prefix
    `analyst.CanonicalizeRepoPrefix(opts.Prefix)` (skipped entries too). Collect roots,
    then classify+resolve every candidate of every kept artifact: `Classify` Skip or
    bases-skip → drop; live → drop; missing + Ambiguous → AmbiguousMissing; missing +
    Confident → the step-7 downgrade → AmbiguousMissing or, if it survives, Dead.
    `Scanned` sorted; refs sorted by (artifact, line, path).
11. `WriteReport(r Report, path string) error` (indented + newline) and
    `ReadReport(path string) (Report, error)`.
12. Tests (use `t.TempDir()`, `git init -q` to mark roots, `t.Setenv("HOME", …)`):
    each base rule; abs ref into `<repo>/.worktrees/w/x` missing raw but present in main
    → live; artifact passed as a symlink into a repo resolves against the real repo;
    `@~/x.md` expansion live/missing; suffix liveness (`daemon/conn.go` under
    `a/b/daemon/conn.go`); symbol liveness (`internal/analyst.Foo`, `f.go:Foo`,
    `f.md#sec`) and the `docs/plan.md` + `docs/plan/` non-case; first-segment downgrade;
    gitignored missing → ambiguous using a real `git init` repo with `.gitignore`;
    `gitOK=false` → ambiguous; import expansion with an A→B→A cycle and a symlinked
    duplicate → each scanned once; prefix filter; `same_name` hit for a moved file.

### S4 — merge + CLI  (`implement: sonnet`)

Files: `internal/freshness/merge.go`, `merge_test.go`, `cmd/analyst/main.go`.

1. `func LoadAdjudications(dir string) (stale map[string]bool, errs []error)`:
   `dir == ""` → empty; glob `dir/adj-*.json`; each file decodes (after
   `analyst.StripCodeFence`) as `[]struct{ID, Verdict, Reason string}`; decode error →
   `errs` entry, file contributes nothing; `stale[id] = true` only for
   `Verdict == "stale"` exactly.
2. `func Clusters(r Report, stale map[string]bool, entries []analyst.Entry) (clusters []analyst.Cluster, suppressed []Ref, errs []error)`:
   kept = `r.Dead` + `AmbiguousMissing` with `stale[id]`; suppressed when some entry has
   `Signal == "stale-ref"`, `Outcome` closed/rejected, `analyst.ArtifactKey(e.Artifact) == analyst.ArtifactKey(ref.Artifact)`,
   and an `Evidence` bullet with prefix `` "`"+ref.Path+"`" ``; group rest by artifact
   (sorted); per group read the artifact (read error → `errs`, group dropped); build
   `analyst.Cluster{ClusterID: "stale-ref::"+a, SignalType: "stale-ref", Artifact: a,
   ArtifactContent: &truncated, ArtifactExists: true, TotalIncidents: len(ev),
   Incidents: json.RawMessage("[]"), Evidence: <marshalled []{path,line,rule_excerpt,resolved_to,same_name}>}`.
   `const SignalType = "stale-ref"` exported.
3. `cmd/analyst/main.go`: usage line lists `freshness`; `case "freshness": runFreshness(os.Args[2:])`;
   `runFreshness` switches on `args[0]` (`scan`/`merge`, else usage exit 2):
   - scan flags: `--db` (default `incidents.db`; `""` = no db), `--artifact`
     (repeatable via `fs.Func`), `--artifact-prefix`, `--out` (default `freshness.json`).
     Paths = `analyst.Artifacts(db)` (if db != "") + `--artifact`s; none at all → error
     exit 1. Write report; print
     `scanned N artifact(s) (S skipped): D dead, A ambiguous-missing → <out>`.
   - merge flags: `--report` (`freshness.json`), `--adjudications-dir` (`""`), `--out`
     (`clusters.json`), `--reason-log-dir` (`reason-log`). Print adjudication/read errs
     and `suppress <artifact> <path>: a prior stale-ref proposal was closed/rejected`
     to stderr; `analyst.MergeClusters(clusters, out, freshness.SignalType)`; print
     `wrote N stale-ref cluster(s) (R refs) into <out> (P suppressed by reason-log)`.
4. Tests: adjudication default-drop (missing verdict, `"drop"`, `"Stale"`, bad JSON file
   → error + nothing kept, fenced JSON accepted); suppression matches the leading token
   only (``- `new/a.go` …`` suppresses `new/a.go`, ``- `old/a.go` → `new/a.go` `` does
   not suppress `new/a.go`), open outcome does not suppress, new ref in same artifact
   still yields a cluster; schema: the file `MergeClusters` writes decodes into
   `analyst.Cluster` with `SignalType=="stale-ref"`, non-nil content, `Evidence`
   decoding to the 5-key entries, `Incidents` == `[]`.

### S5 — golden fixture  (`implement: sonnet`)

Files: `internal/freshness/golden_test.go`, `internal/freshness/testdata/golden/…`.

1. `testdata/golden/repo/` tree: `CLAUDE.md`, `AGENTS.md`, `docs/live.md`,
   `scripts/run.sh`, `internal/pkg/pkg.go`, `sub/daemon/conn.go`. `CLAUDE.md` mixes:
   live refs of every form (incl. `@AGENTS.md`, a link, `daemon/conn.go`,
   `internal/pkg.Func`, `docs/live.md:10-20`); dead refs of every form
   (`@docs/gone.md`, `[x](docs/removed.md)`, `` `scripts/old.sh` ``,
   `` `internal/pkg/gone.go` ``); example paths (`e.g. \`docs/example-output.md\``,
   `path/to/file.go`, `foo/bar.go`); `/tmp/agent-smith-run/x.json`; `$HOME/x/y.md`,
   `${REPO}/z.md`; URLs; a fenced block with a dead-looking path; an extensionless
   `` `origin/main` `` and `@types/node` (ambiguous-missing, not dead). `AGENTS.md`
   (reached via import) holds one dead ref (`` `docs/ancient.md` ``).
2. `golden_test.go`: copy the tree into `t.TempDir()`, `git init -q` it, `Scan([tmp/CLAUDE.md])`;
   assert `Dead` (artifact-relative, `(file, line, path)`) equals `testdata/golden/expected_dead.json`
   exactly; assert every live fixture path appears in neither `Dead` nor
   `AmbiguousMissing`; assert `AmbiguousMissing` paths equal the expected set.
3. DB path test (same file): `makeIncidentsDB`-equivalent (duckdb via
   `exec.Command(duckDBBin…)` — use `AGENT_SMITH_DUCKDB` env or `duckdb`) with a
   candidate at the fixture's worktree-copy path (`<tmp>/.worktrees/w/CLAUDE.md`) →
   `analyst.Artifacts` returns `<tmp>/CLAUDE.md` → `Scan` yields the same dead set.

### S6 — prompts + docs  (`implement: sonnet`)

Files: `agents/oracle.md`, `agents/skeptic.md`, `commands/freshness.md`,
`internal/analyst/oracle_test.go`, `internal/plugin/manifest_test.go`,
`docs/analyst.md`, `README.md`, `.gitignore`.

1. `agents/oracle.md`: append a final `## stale-ref clusters` section (do not edit
   existing sections): when `signal_type` is `stale-ref` this section replaces Input and
   Procedure; input = `artifact_content` + `evidence[]` (`path, line, rule_excerpt,
   resolved_to, same_name`), `incidents` empty, `distinct_sessions` 0, `total_incidents`
   = ref count; each ref is verified missing on disk; procedure: locate each line (Read
   the artifact if content is truncated), per ref decide repoint (a `same_name` hit that
   is clearly the moved file, or a corrected path) / drop the rule / benign; choose
   `fix-stale` (any repoint or in-place path correction), `remove` (only rule deletions),
   or `skip` (all benign; `reason_log` starts `skipped: benign reference`);
   `proposed_change` is a unified diff over the artifact; **each `evidence` string opens
   with the ref's `path` in backticks** — `` `docs/old.md` (line 12) → `docs/new.md` ``;
   cite only the refs the change fixes; confidence rules; echo `stale-ref`. State that
   for `stale-ref` the ledger keys **per ref** on those evidence strings (not on
   `(artifact, signal)`), and require a ref-specific `id`:
   `stale-ref-<parent dir name>-<artifact basename>-<slug of the first cited path>`
   (most artifacts are `CLAUDE.md`/`AGENTS.md`; the parent dir tells repos apart) — `WriteReasonLogs`
   skips an existing `<date>-<slug(id)>` file, so a reused id would drop the entry.
2. `agents/skeptic.md` Procedure step 4: add one sentence — for a `stale-ref` cluster
   (it carries `evidence[]` and no incidents) verify each cited path is still missing on
   disk and any repoint target exists; there are no windows or session counts, so their
   absence is not weak evidence.
3. `commands/freshness.md`: frontmatter (`description`, `allowed-tools: Bash, Read, Write, Agent`);
   step zero verbatim bootstrap paragraph from `commands/mine.md`; `$ARGUMENTS`: `repo`
   → `--artifact-prefix "$(git rev-parse --show-toplevel)"`; precondition
   `incidents.db`; steps per spec §3.2 with the exact CLI (`scan`, per-artifact
   adjudicator prompt with default-drop, `adj-<i>.json` schema, `merge`); run dir
   `FRESH_DIR=$(mktemp -d /tmp/agentsmith-fresh.XXXXXX)` — outside `apply.md`'s
   `/tmp/agent-smith-*` newest-dir glob; report + ordering note (§3.4:
   re-run after any mine); remove `$FRESH_DIR` after a successful merge;
   `echo "next: /agent-smith:propose"`.
4. Tests: `oracle_test.go` must-contain `` "## stale-ref clusters" `` and
   `"opens with the ref's"`; `manifest_test.go` commands list gains `"freshness"`.
5. `docs/analyst.md`: `## Freshness (Track B)` section — commands, report/cluster
   schema, ordering. `README.md`: Track B paragraph + mermaid subgraph label reflect
   the live v1 (file-path audit → `stale-ref` clusters) with explorers still planned;
   roadmap bullet. `.gitignore`: `freshness.json`.

### S7 — gate + smoke (lead)

`nix develop -c go vet ./...`, `nix develop -c go test ./...`, `nix build .`; run
`analyst freshness scan --db <scratch copy of crew incidents.db> --out <scratch>/freshness.json`;
hand-verify each dead/ambiguous ref is missing; then
`analyst freshness merge --report … --out <scratch>/clusters.json` and decode one
written cluster file with `jq`; record counts + sample for the PR body.

## Acceptance map

| AC | Covered by |
|---|---|
| 1 unit tests (classifier, resolution, worktree, `~`, skiplist) | S2 + S3 tests |
| 2 golden fixture | S5 |
| 3 schema + survives/regenerates around `analyst cluster` | S1 step 10, S4 schema test |
| 4 real-corpus smoke | S7 |
| 5 gate | S7 |
