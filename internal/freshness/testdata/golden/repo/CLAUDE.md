# CLAUDE.md — golden fixture

This file exercises every reference form the freshness scanner recognizes.

@AGENTS.md

See the [live doc](docs/live.md) for the project overview.

Run `scripts/run.sh` to rebuild the CLI.

The connection handling lives in `daemon/conn.go`.

Check `internal/pkg.Func` for the entry point.

Read `docs/live.md:10-20` for the config section.

The `internal/pkg/` package holds the core logic.

@docs/gone.md

See [removed](docs/removed.md) for the old design notes.

The old build script was `scripts/old.sh`.

`internal/pkg/gone.go` used to hold the parser.

For example, `docs/example-output.md` shows the expected format.

Do not use `path/to/file.go` or `foo/bar.go` as real paths.

Run artifacts land in `/tmp/agent-smith-run/x.json`.

Local config lives at `$HOME/x/y.md` and `${REPO}/z.md`.

See https://example.com/docs/a.md or [the site](https://example.com/docs/a.md).

```bash
# reference to `scripts/nope.sh` here (should be ignored, inside fence)
echo "@docs/nope.md"
```

Compare against `origin/main` using @types/node types.

The file `README.md` explains more.
