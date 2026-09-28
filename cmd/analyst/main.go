package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/noamsto/agent-smith/internal/analyst"
	"github.com/noamsto/agent-smith/internal/freshness"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyst <cluster|assemble|cite-check|freshness> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "--version":
		fmt.Println(version)
	case "cluster":
		runCluster(os.Args[2:])
	case "assemble":
		runAssemble(os.Args[2:])
	case "cite-check":
		runCiteCheck(os.Args[2:])
	case "freshness":
		runFreshness(os.Args[2:])
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
	fleet, droppedBacklog, droppedUnresolved, droppedTop := analyst.RankClusters(clusters, *top, *includeStale)
	if droppedBacklog > 0 {
		fmt.Fprintf(os.Stderr, "recency: %d likely-resolved cluster(s) excluded — no incidents in the last %d active days; --include-stale to rank them\n", droppedBacklog, *staleDays)
	}
	if droppedUnresolved > 0 {
		fmt.Fprintf(os.Stderr, "%d cluster(s) excluded: the artifact is a redirect-only pointer file whose @import could not be resolved\n", droppedUnresolved)
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
	fileIssues := fs.Bool("file-issues", false, "file a GitHub issue for each proposal the skeptic marked unroutable")
	selfRepo := fs.String("self-repo", "noamsto/agent-smith", "repo that receives unroutable escalations")
	_ = fs.Parse(args)

	d := *date
	if d == "" {
		d = time.Now().UTC().Format("2006-01-02")
	}
	props, errs := analyst.LoadProposals(*dir)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "skip:", e)
	}
	verdicts := analyst.LoadVerdicts(*dir)
	props, unroutable := analyst.SplitUnroutable(props, verdicts)
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

	var filer analyst.IssueFiler
	if *fileIssues {
		filer = analyst.GhIssueFiler(*selfRepo)
	}
	escalations, err := analyst.Escalate(unroutable, verdicts, *reasonLog, d, filer)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst assemble:", err)
		os.Exit(1)
	}
	for _, e := range escalations {
		if e.Skipped != "" {
			fmt.Printf("unroutable %s: no issue filed (%s)\n", e.ID, e.Skipped)
			continue
		}
		fmt.Printf("unroutable %s: filed %s\n", e.ID, e.IssueURL)
	}
}

func runFreshness(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: analyst freshness <scan|merge> [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "scan":
		runFreshnessScan(args[1:])
	case "merge":
		runFreshnessMerge(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "usage: analyst freshness <scan|merge> [flags]")
		os.Exit(2)
	}
}

func runFreshnessScan(args []string) {
	fs := flag.NewFlagSet("freshness scan", flag.ExitOnError)
	db := fs.String("db", "incidents.db", "incidents DuckDB file supplying the audit set; \"\" = artifacts only from --artifact")
	out := fs.String("out", "freshness.json", "scan report output")
	artifactPrefix := fs.String("artifact-prefix", "", "keep only artifacts under this repo root (worktree roots canonicalized)")
	var artifacts []string
	fs.Func("artifact", "an artifact to audit, in addition to --db's set (repeatable)", func(v string) error {
		artifacts = append(artifacts, v)
		return nil
	})
	_ = fs.Parse(args)

	paths := artifacts
	if *db != "" {
		fromDB, err := analyst.Artifacts(context.Background(), *db)
		if err != nil {
			fmt.Fprintln(os.Stderr, "analyst freshness scan:", err)
			os.Exit(1)
		}
		paths = append(fromDB, paths...)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "analyst freshness scan: no artifacts to audit")
		os.Exit(1)
	}

	report := freshness.Scan(paths, freshness.Options{Prefix: *artifactPrefix})
	if err := freshness.WriteReport(report, *out); err != nil {
		fmt.Fprintln(os.Stderr, "analyst freshness scan:", err)
		os.Exit(1)
	}
	fmt.Printf("scanned %d artifact(s) (%d skipped): %d dead, %d ambiguous-missing → %s\n",
		len(report.Scanned), len(report.Skipped), len(report.Dead), len(report.AmbiguousMissing), *out)
}

func runFreshnessMerge(args []string) {
	fs := flag.NewFlagSet("freshness merge", flag.ExitOnError)
	report := fs.String("report", "freshness.json", "scan report to merge")
	adjDir := fs.String("adjudications-dir", "", "directory of adj-*.json adjudication files; \"\" = no ambiguous refs kept")
	out := fs.String("out", "clusters.json", "cluster index to merge stale-ref clusters into")
	reasonLog := fs.String("reason-log-dir", "reason-log", "reason-log directory consulted for prior stale-ref suppressions")
	_ = fs.Parse(args)

	r, err := freshness.ReadReport(*report)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst freshness merge:", err)
		os.Exit(1)
	}
	stale, errs := freshness.LoadAdjudications(*adjDir)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "skip:", e)
	}
	entries, err := analyst.ReadEntries(*reasonLog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst freshness merge:", err)
		os.Exit(1)
	}
	clusters, suppressed, errs := freshness.Clusters(r, stale, entries)
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "skip:", e)
	}
	for _, ref := range suppressed {
		fmt.Fprintf(os.Stderr, "suppress %s %s: a prior stale-ref proposal was closed/rejected\n", ref.Artifact, ref.Path)
	}
	if err := analyst.MergeClusters(clusters, *out, freshness.SignalType); err != nil {
		fmt.Fprintln(os.Stderr, "analyst freshness merge:", err)
		os.Exit(1)
	}
	refs := 0
	for _, c := range clusters {
		refs += c.TotalIncidents
	}
	fmt.Printf("wrote %d stale-ref cluster(s) (%d refs) into %s (%d suppressed by reason-log)\n",
		len(clusters), refs, *out, len(suppressed))
}
