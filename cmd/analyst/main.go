package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/noamsto/agent-smith/internal/analyst"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyst <cluster|assemble> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "cluster":
		runCluster(os.Args[2:])
	case "assemble":
		runAssemble(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}

func runCluster(args []string) {
	fs := flag.NewFlagSet("cluster", flag.ExitOnError)
	db := fs.String("db", "incidents.db", "incidents DuckDB file")
	out := fs.String("out", "clusters.json", "output clusters file")
	minSessions := fs.Int("min-sessions", 3, "minimum distinct sessions for an actionable cluster")
	_ = fs.Parse(args)

	clusters, err := analyst.ClusterDB(context.Background(), *db, *minSessions)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	if err := analyst.WriteClusters(clusters, *out); err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d clusters to %s\n", len(clusters), *out)
}

func runAssemble(args []string) {
	fs := flag.NewFlagSet("assemble", flag.ExitOnError)
	dir := fs.String("proposals-dir", "proposals", "directory of per-cluster proposal JSON files")
	out := fs.String("out", "proposals.json", "output proposals file")
	reasonLog := fs.String("reason-log-dir", "reason-log", "append-only reason-log directory")
	date := fs.String("date", "", "ISO date for reason-log filenames (default: today)")
	_ = fs.Parse(args)

	d := *date
	if d == "" {
		d = time.Now().UTC().Format("2006-01-02")
	}
	props, errs := analyst.LoadProposals(*dir)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "skip:", e)
	}
	if err := analyst.WriteProposals(props, *out); err != nil {
		fmt.Fprintln(os.Stderr, "analyst assemble:", err)
		os.Exit(1)
	}
	n, err := analyst.WriteReasonLogs(props, *reasonLog, d)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst assemble:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d proposals to %s, %d new reason-log entries (%d skipped inputs)\n",
		len(props), *out, n, len(errs))
}
