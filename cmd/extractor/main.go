package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/noamsto/agent-smith/internal/extractor"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	cfg := extractor.DefaultConfig()
	var signals string

	flag.StringVar(&cfg.CorpusGlob, "corpus", cfg.CorpusGlob, "glob of .jsonl files to mine")
	flag.StringVar(&cfg.OutDB, "out", cfg.OutDB, "output DuckDB file")
	flag.StringVar(&cfg.Since, "since", cfg.Since, "ISO8601 lower bound on record timestamp")
	flag.StringVar(&signals, "signals", "", "comma-separated signals (default: all)")
	flag.StringVar(&cfg.MemoryLimit, "memory-limit", cfg.MemoryLimit, "DuckDB memory_limit pragma (e.g. 4GB)")
	flag.IntVar(&cfg.Threads, "threads", cfg.Threads, "DuckDB threads pragma (0 = duckdb default / all cores)")
	flag.StringVar(&cfg.GlobalClaudeMd, "global-claude-md", cfg.GlobalClaudeMd, "path used as the global CLAUDE.md candidate for main-session incidents")
	flag.Parse()

	for _, s := range strings.Split(signals, ",") {
		if s = strings.TrimSpace(s); s != "" { // tolerate "a, b" and trailing commas
			cfg.Signals = append(cfg.Signals, s)
		}
	}

	// Default --since to the previous run's marker so a re-mine (deja-vu) only
	// processes sessions newer than last time. An explicit --since (even "") wins.
	sinceSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "since" {
			sinceSet = true
		}
	})
	if !sinceSet {
		if m := extractor.ReadMarker(cfg.OutDB); m != "" {
			cfg.Since = m
			fmt.Fprintf(os.Stderr, "extractor: --since defaulted to last-run marker %s\n", m)
		}
	}

	// The corpus on disk is a rolling retention window, so the db is the only
	// record of anything older. A run that creates it starts history at the
	// earliest surviving transcript; a marker without a db means someone deleted
	// one and not the other. Both are silent data loss unless we say so.
	_, dbErr := os.Stat(cfg.OutDB)
	freshDB := os.IsNotExist(dbErr)
	if freshDB {
		if extractor.ReadMarker(cfg.OutDB) != "" {
			fmt.Fprintf(os.Stderr, "extractor: WARNING %s is missing but its last-run marker is not — "+
				"the database was deleted while the marker survived\n", cfg.OutDB)
		}
		fmt.Fprintf(os.Stderr, "extractor: WARNING creating a new %s — history will begin at the "+
			"earliest transcript still on disk; anything older is unrecoverable\n", cfg.OutDB)
	}

	runStart := time.Now()
	ctx := context.Background()
	if err := extractor.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "extractor:", err)
		os.Exit(1)
	}
	if err := extractor.WriteMarker(cfg.OutDB, runStart); err != nil {
		fmt.Fprintln(os.Stderr, "extractor: write last-run marker:", err)
		os.Exit(1)
	}
	summary, err := extractor.Summary(ctx, cfg.OutDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extractor: summary:", err)
		os.Exit(1)
	}
	coverage, err := extractor.ReadCoverage(ctx, cfg.OutDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extractor: coverage:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote incidents to %s\n%s%s\n", cfg.OutDB, summary, coverage)
}
