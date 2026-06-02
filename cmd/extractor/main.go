package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/noamsto/agent-smith/internal/extractor"
)

func main() {
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

	ctx := context.Background()
	if err := extractor.Run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "extractor:", err)
		os.Exit(1)
	}
	summary, err := extractor.Summary(ctx, cfg.OutDB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extractor: summary:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote incidents to %s\n%s", cfg.OutDB, summary)
}
