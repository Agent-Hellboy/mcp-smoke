package agent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Agent-Hellboy/mcp-smoke/internal/mcp"
)

func TestRunnerOpenAIRoutesSanitizedToolNameBackToMCP(t *testing.T) {
	t.Parallel()

	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch requests {
		case 1:
			tools, ok := body["tools"].([]interface{})
			if !ok || len(tools) != 1 {
				t.Fatalf("expected one tool, got %#v", body["tools"])
			}

			tool := tools[0].(map[string]interface{})
			function := tool["function"].(map[string]interface{})
			if got := function["name"]; got != "math_add" {
				t.Fatalf("expected sanitized tool name math_add, got %v", got)
			}

			return jsonResponse(map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"message": map[string]interface{}{
							"tool_calls": []map[string]interface{}{
								{
									"id":   "call_1",
									"type": "function",
									"function": map[string]interface{}{
										"name":      "math_add",
										"arguments": `{"a":2,"b":3}`,
									},
								},
							},
						},
					},
				},
			}), nil
		case 2:
			return jsonResponse(map[string]interface{}{
				"choices": []map[string]interface{}{
					{
						"message": map[string]interface{}{
							"content": "5",
						},
					},
				},
			}), nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	})}

	runner := &Runner{
		backend: &openAIBackend{
			apiKey:  "test-key",
			model:   "test-model",
			client:  client,
			baseURL: "https://openai.test",
		},
		maxSteps: 4,
		log:      io.Discard,
	}

	var calledName string
	var calledArgs map[string]interface{}
	answer, err := runner.Run(context.Background(), "add 2 and 3", []mcp.Tool{
		{
			Name:        "math/add",
			Description: "Add two numbers.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a": map[string]interface{}{"type": "number"},
					"b": map[string]interface{}{"type": "number"},
				},
				"required": []string{"a", "b"},
			},
		},
	}, func(ctx context.Context, toolName string, arguments map[string]interface{}) (ExecutionResult, error) {
		calledName = toolName
		calledArgs = arguments
		return ExecutionResult{Model: "5"}, nil
	})
	if err != nil {
		t.Fatalf("runner returned error: %v", err)
	}

	if answer != "5" {
		t.Fatalf("expected final answer 5, got %q", answer)
	}
	if calledName != "math/add" {
		t.Fatalf("expected MCP tool name math/add, got %q", calledName)
	}
	if got := calledArgs["a"]; got != float64(2) {
		t.Fatalf("expected a=2, got %#v", got)
	}
	if got := calledArgs["b"]; got != float64(3) {
		t.Fatalf("expected b=3, got %#v", got)
	}
}

func TestRunnerAnthropicUsesToolAndReturnsFinalText(t *testing.T) {
	t.Parallel()

	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++

		switch requests {
		case 1:
			return jsonResponse(map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "tool_use",
						"id":   "toolu_1",
						"name": "echo",
						"input": map[string]interface{}{
							"text": "hello",
						},
					},
				},
			}), nil
		case 2:
			return jsonResponse(map[string]interface{}{
				"content": []map[string]interface{}{
					{
						"type": "text",
						"text": "hello",
					},
				},
			}), nil
		default:
			t.Fatalf("unexpected request %d", requests)
			return nil, nil
		}
	})}

	runner := &Runner{
		backend: &anthropicBackend{
			apiKey:  "test-key",
			model:   "test-model",
			client:  client,
			baseURL: "https://anthropic.test",
		},
		maxSteps: 4,
		log:      io.Discard,
	}

	var calledName string
	var calledArgs map[string]interface{}
	answer, err := runner.Run(context.Background(), "say hello", []mcp.Tool{
		{
			Name:        "echo",
			Description: "Echo text.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"text": map[string]interface{}{"type": "string"},
				},
				"required": []string{"text"},
			},
		},
	}, func(ctx context.Context, toolName string, arguments map[string]interface{}) (ExecutionResult, error) {
		calledName = toolName
		calledArgs = arguments
		return ExecutionResult{Model: "hello"}, nil
	})
	if err != nil {
		t.Fatalf("runner returned error: %v", err)
	}

	if answer != "hello" {
		t.Fatalf("expected final answer hello, got %q", answer)
	}
	if calledName != "echo" {
		t.Fatalf("expected MCP tool name echo, got %q", calledName)
	}
	if got := calledArgs["text"]; got != "hello" {
		t.Fatalf("expected text=hello, got %#v", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func jsonResponse(body interface{}) *http.Response {
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(strings.NewReader(string(data))),
	}
}
