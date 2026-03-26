package agentcli

import (
	"strings"
	"testing"
)

func TestParseServerSpecHTTP(t *testing.T) {
	t.Parallel()

	transport, url, command, args, err := ParseServerSpec("https://example.com/mcp")
	if err != nil {
		t.Fatalf("ParseServerSpec returned error: %v", err)
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

	transport, url, command, args, err := ParseServerSpec("go run ./cmd/mcp-test-server")
	if err != nil {
		t.Fatalf("ParseServerSpec returned error: %v", err)
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

	transport, url, command, args, err := ParseServerSpec(`["go","run","./cmd/mcp-test-server"]`)
	if err != nil {
		t.Fatalf("ParseServerSpec returned error: %v", err)
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

func TestParseDotEnv(t *testing.T) {
	t.Parallel()

	values, err := ParseDotEnv(strings.NewReader(`
# comment
OPENAI_API_KEY=sk-test
OPENAI_MODEL="gpt-4.1-mini"
export ANTHROPIC_MODEL='claude-3-5-haiku-latest'
`))
	if err != nil {
		t.Fatalf("ParseDotEnv returned error: %v", err)
	}

	if got := values["OPENAI_API_KEY"]; got != "sk-test" {
		t.Fatalf("expected OPENAI_API_KEY, got %q", got)
	}
	if got := values["OPENAI_MODEL"]; got != "gpt-4.1-mini" {
		t.Fatalf("expected OPENAI_MODEL, got %q", got)
	}
	if got := values["ANTHROPIC_MODEL"]; got != "claude-3-5-haiku-latest" {
		t.Fatalf("expected ANTHROPIC_MODEL, got %q", got)
	}
}

func TestEnvValuePrefersShellEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "shell-key")

	got := envValue("OPENAI_API_KEY", map[string]string{
		"OPENAI_API_KEY": "file-key",
	})
	if got != "shell-key" {
		t.Fatalf("expected shell value, got %q", got)
	}
}

func TestResolveProviderUsesDotEnv(t *testing.T) {
	t.Parallel()

	provider, err := resolveProvider("", map[string]string{
		"OPENAI_API_KEY": "file-key",
	})
	if err != nil {
		t.Fatalf("resolveProvider returned error: %v", err)
	}
	if provider != "openai" {
		t.Fatalf("expected openai provider, got %q", provider)
	}
}
