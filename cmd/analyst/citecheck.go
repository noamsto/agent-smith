package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/noamsto/agent-smith/internal/analyst"
)

func runCiteCheck(args []string) {
	fs := flag.NewFlagSet("cite-check", flag.ExitOnError)
	proposal := fs.String("proposal", "", "path to the Oracle proposal JSON to check")
	cluster := fs.String("cluster", "", "path to the cluster JSON the proposal was derived from")
	reasonLog := fs.String("reason-log-dir", "reason-log", "append-only reason-log directory for rejection entries")
	date := fs.String("date", "", "ISO date for the rejection filename (default: today)")
	_ = fs.Parse(args)

	d := *date
	if d == "" {
		d = time.Now().UTC().Format("2006-01-02")
	}

	result, err := analyst.ApplyCiteCheck(*proposal, *cluster, *reasonLog, d)
	if err != nil {
		fmt.Fprintln(os.Stderr, "analyst cite-check:", err)
		os.Exit(1)
	}
	switch result.Status {
	case "demoted":
		fmt.Printf("demoted %s: %s\n", result.ID, result.Notes)
	case "rejected":
		fmt.Printf("rejected %s: %s\n", result.ID, strings.Join(result.Failures, "; "))
	default:
		fmt.Printf("ok %s\n", result.ID)
	}
}
