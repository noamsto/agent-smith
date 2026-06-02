package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyst <cluster|assemble> [flags]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "cluster":
		fmt.Fprintln(os.Stderr, "cluster: not implemented yet")
		os.Exit(1)
	case "assemble":
		fmt.Fprintln(os.Stderr, "assemble: not implemented yet")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", os.Args[1])
		os.Exit(2)
	}
}
