package main

import "testing"

func TestParseServerSpecHTTP(t *testing.T) {
	t.Parallel()

	transport, url, command, args, err := parseServerSpec("https://example.com/mcp")
	if err != nil {
		t.Fatalf("parseServerSpec returned error: %v", err)
	}
	if transport != "http" {
		t.Fatalf("expected http transport, got %q", transport)
	}
	if url != "https://example.com/mcp" {
		t.Fatalf("expected url to match, got %q", url)
	}
	if command != "" {
		t.Fatalf("expected empty command, got %q", command)
	}
	if len(args) != 0 {
		t.Fatalf("expected no args, got %#v", args)
	}
}

func TestParseServerSpecCommandString(t *testing.T) {
	t.Parallel()

	transport, url, command, args, err := parseServerSpec("go run ./cmd/mcp-test-server")
	if err != nil {
		t.Fatalf("parseServerSpec returned error: %v", err)
	}
	if transport != "stdio" {
		t.Fatalf("expected stdio transport, got %q", transport)
	}
	if url != "" {
		t.Fatalf("expected empty url, got %q", url)
	}
	if command != "go" {
		t.Fatalf("expected command go, got %q", command)
	}
	if len(args) != 2 || args[0] != "run" || args[1] != "./cmd/mcp-test-server" {
		t.Fatalf("unexpected args %#v", args)
	}
}

func TestParseServerSpecJSONArray(t *testing.T) {
	t.Parallel()

	transport, url, command, args, err := parseServerSpec(`["go","run","./cmd/mcp-test-server"]`)
	if err != nil {
		t.Fatalf("parseServerSpec returned error: %v", err)
	}
	if transport != "stdio" {
		t.Fatalf("expected stdio transport, got %q", transport)
	}
	if url != "" {
		t.Fatalf("expected empty url, got %q", url)
	}
	if command != "go" {
		t.Fatalf("expected command go, got %q", command)
	}
	if len(args) != 2 || args[0] != "run" || args[1] != "./cmd/mcp-test-server" {
		t.Fatalf("unexpected args %#v", args)
	}
}
