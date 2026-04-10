package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunHelpShowsDefaultUsage(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	code := run([]string{"--help"}, &output, &output)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	text := output.String()
	if !strings.Contains(text, "mcp-smoke-agent [flags] [plain English request]") {
		t.Fatalf("expected default usage, got %q", text)
	}
	if !strings.Contains(text, "mcp-smoke-agent smoke [flags]") {
		t.Fatalf("expected smoke usage, got %q", text)
	}
}

func TestRunSmokeHelpRoutesToSmokeCommand(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	code := run([]string{"smoke", "--help"}, &output, &output)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}

	text := output.String()
	if !strings.Contains(text, "Usage of smoke:") {
		t.Fatalf("expected smoke help output, got %q", text)
	}
}
