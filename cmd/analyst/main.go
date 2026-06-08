package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/noamsto/agent-smith/internal/analyst"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyst <cluster|assemble> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
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
	out := fs.String("out", "clusters.json", "cluster index file; per-cluster JSON is written to a sibling clusters/ dir")
	reasonLog := fs.String("reason-log-dir", "reason-log", "reason-log directory consulted to skip closed/rejected clusters")
	minSessions := fs.Int("min-sessions", 5, "minimum distinct sessions for an actionable cluster")
	maxIncidents := fs.Int("max-incidents-per-cluster", 50, "cap incidents per cluster fed to the Oracle (session-stratified sample); 0 = uncapped")
	top := fs.Int("top", 0, "keep only the top N clusters by signal strength (distinct_sessions, then total_incidents); 0 = keep all")
	_ = fs.Parse(args)

	clusters, dropped, err := analyst.ClusterDB(context.Background(), *db, *minSessions, *maxIncidents)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	if dropped > 0 {
		fmt.Fprintf(os.Stderr, "dropped %d cluster(s) whose canonical artifact no longer exists\n", dropped)
	}
	entries, err := analyst.ReadEntries(*reasonLog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	clusters, skipped := analyst.FilterRejected(clusters, entries)
	for _, c := range skipped {
		fmt.Fprintf(os.Stderr, "skip %s: a prior proposal was closed/rejected (reason-log)\n", c.ClusterID)
	}
	clusters, topDropped := analyst.TopClusters(clusters, *top)
	if topDropped > 0 {
		cutoff := clusters[len(clusters)-1]
		fmt.Fprintf(os.Stderr, "--top %d: dropped %d lower-signal clusters; cutoff at %d distinct sessions / %d incidents\n",
			*top, topDropped, cutoff.DistinctSessions, cutoff.TotalIncidents)
	}
	if err := analyst.WriteClusters(clusters, *out); err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	fmt.Printf("wrote %d clusters: index %s + clusters/<id>.json (%d skipped as closed/rejected)\n", len(clusters), *out, len(skipped))
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
