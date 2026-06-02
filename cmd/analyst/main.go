package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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

// runAssemble is implemented in Task 5; this stub keeps the build green until then.
func runAssemble(args []string) {
	fmt.Fprintln(os.Stderr, "assemble: not implemented yet")
	os.Exit(1)
}
