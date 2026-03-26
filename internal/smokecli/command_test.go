package smokecli

import (
	"testing"

	"github.com/Agent-Hellboy/mcp-smoke/internal/mcp"
)

func TestSelectToolSkipsEarlierRequiredMismatch(t *testing.T) {
	t.Parallel()

	tool, err, _ := selectTool([]mcp.Tool{
		{
			Name: "echo",
			InputSchema: map[string]interface{}{
				"required": []string{"text"},
			},
		},
		{
			Name: "add",
			InputSchema: map[string]interface{}{
				"required": []string{"a", "b"},
			},
		},
		{
			Name:        "ping",
			InputSchema: map[string]interface{}{},
		},
	}, "", map[string]interface{}{
		"a": 41,
		"b": 1,
	})
	if err != nil {
		t.Fatalf("selectTool returned error: %v", err)
	}
	if tool.Name != "add" {
		t.Fatalf("expected add, got %q", tool.Name)
	}
}

func TestSelectPromptSkipsEarlierRequiredMismatch(t *testing.T) {
	t.Parallel()

	prompt, err, _ := selectPrompt([]mcp.Prompt{
		{
			Name: "hello-name",
			Arguments: []mcp.PromptArgument{
				{Name: "name", Required: true},
			},
		},
		{
			Name: "hello",
		},
	}, "", map[string]interface{}{})
	if err != nil {
		t.Fatalf("selectPrompt returned error: %v", err)
	}
	if prompt.Name != "hello" {
		t.Fatalf("expected hello, got %q", prompt.Name)
	}
}

func TestSelectNamedToolFailsWhenRequiredArgsMissing(t *testing.T) {
	t.Parallel()

	tool, err, _ := selectTool([]mcp.Tool{
		{
			Name: "add",
			InputSchema: map[string]interface{}{
				"required": []string{"a", "b"},
			},
		},
	}, "add", map[string]interface{}{
		"a": 41,
	})
	if err == nil {
		t.Fatal("expected missing-args error")
	}
	if tool.Name != "" {
		t.Fatalf("expected empty tool, got %q", tool.Name)
	}
}

func TestSelectNamedPromptFailsWhenPromptMissing(t *testing.T) {
	t.Parallel()

	prompt, err, _ := selectPrompt([]mcp.Prompt{
		{Name: "hello"},
	}, "hello-name", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected missing prompt error")
	}
	if prompt.Name != "" {
		t.Fatalf("expected empty prompt, got %q", prompt.Name)
	}
}
