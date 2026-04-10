package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Agent-Hellboy/mcp-smoke/internal/agentcli"
	"github.com/Agent-Hellboy/mcp-smoke/internal/smokecli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	switch args[0] {
	case "smoke":
		return smokecli.Run(args[1:], stdout, stderr)
	case "agent":
		return agentcli.Run(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		if len(args) > 1 && args[1] == "smoke" {
			return smokecli.Run([]string{"--help"}, stdout, stderr)
		}
		printUsage(stdout)
		return 0
	default:
		return agentcli.Run(args, stdout, stderr)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  mcp-smoke-agent [flags] [plain English request]")
	fmt.Fprintln(w, "  mcp-smoke-agent smoke [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Default mode connects to an MCP server and turns a plain-English request into MCP tool calls.")
	fmt.Fprintln(w, "Use `--prompt`, pipe stdin, or pass trailing text after the flags.")
	fmt.Fprintln(w, "Run `mcp-smoke-agent smoke --help` for the raw protocol smoke checks.")
	fmt.Fprintln(w)
	_ = agentcli.Run([]string{"--help"}, w, w)
}
