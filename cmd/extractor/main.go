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
	flag.Parse()

	if signals != "" {
		cfg.Signals = strings.Split(signals, ",")
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
