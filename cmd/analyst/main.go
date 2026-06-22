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
	top := fs.Int("top", 0, "keep only the top N clusters by signal strength (recent_sessions, then distinct_sessions); 0 = keep all")
	staleDays := fs.Int("stale-after-days", 14, "a cluster is backlog if it has no incidents in the most recent N active corpus days")
	includeStale := fs.Bool("include-stale", false, "rank the historical backlog by lifetime signal instead of excluding it from the fleet")
	artifactPrefix := fs.String("artifact-prefix", "", "keep only clusters whose canonical artifact lives under this repo root (worktree roots canonicalized)")
	_ = fs.Parse(args)

	clusters, dropped, err := analyst.ClusterDB(context.Background(), *db, *minSessions, *maxIncidents, *staleDays)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst cluster:", err)
		os.Exit(1)
	}
	if dropped > 0 {
		fmt.Fprintf(os.Stderr, "dropped %d cluster(s) whose canonical artifact no longer exists\n", dropped)
	}
	if *artifactPrefix != "" {
		before := len(clusters)
		clusters = analyst.FilterByPrefix(clusters, *artifactPrefix)
		if d := before - len(clusters); d > 0 {
			fmt.Fprintf(os.Stderr, "--artifact-prefix: kept %d, dropped %d cluster(s) outside %s\n", len(clusters), d, *artifactPrefix)
		}
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
	fleet, droppedBacklog, droppedTop := analyst.RankClusters(clusters, *top, *includeStale)
	if droppedBacklog > 0 {
		fmt.Fprintf(os.Stderr, "recency: %d backlog cluster(s) excluded — no incidents in the last %d active days; --include-stale to rank them\n", droppedBacklog, *staleDays)
	}
	if droppedTop > 0 {
		cutoff := fleet[len(fleet)-1]
		fmt.Fprintf(os.Stderr, "--top %d: dropped %d lower-signal cluster(s); cutoff at %d recent / %d lifetime sessions\n",
			*top, droppedTop, cutoff.RecentSessions, cutoff.DistinctSessions)
	}
	clusters = fleet
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
