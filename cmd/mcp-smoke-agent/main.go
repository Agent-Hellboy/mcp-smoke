package main

import (
	"fmt"
	"os"

	"github.com/Agent-Hellboy/mcp-smoke/internal/agentcli"
	"github.com/Agent-Hellboy/mcp-smoke/internal/smokecli"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}

	switch args[0] {
	case "agent":
		return agentcli.Run(args[1:], os.Stdout, os.Stderr)
	case "smoke":
		return smokecli.Run(args[1:], os.Stdout, os.Stderr)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", args[0])
		printUsage(os.Stderr)
		return 2
	}
}

func printUsage(w *os.File) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  mcp-smoke-agent smoke [flags]")
	fmt.Fprintln(w, "  mcp-smoke-agent agent [flags]")
}
