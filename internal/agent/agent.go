package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"

	"github.com/Agent-Hellboy/mcp-smoke/internal/mcp"
)

type Config struct {
	Provider string
	Model    string
	APIKey   string
	MaxSteps int
	Log      io.Writer
}

type ExecutionResult struct {
	Model string
}

type ToolExecutor func(ctx context.Context, toolName string, arguments map[string]interface{}) (ExecutionResult, error)

type Runner struct {
	backend  backend
	maxSteps int
	log      io.Writer
}

type backend interface {
	Run(ctx context.Context, prompt string, catalog toolCatalog, exec ToolExecutor, maxSteps int, log io.Writer) (string, error)
}

type toolCatalog struct {
	Tools  []catalogTool
	lookup map[string]catalogTool
}

type catalogTool struct {
	ProviderName string
	MCPName      string
	Description  string
	InputSchema  map[string]interface{}
}

type openAIBackend struct {
	apiKey  string
	model   string
	client  *http.Client
	baseURL string
}

type anthropicBackend struct {
	apiKey  string
	model   string
	client  *http.Client
	baseURL string
}

func New(cfg Config) (*Runner, error) {
	if cfg.Provider == "" {
		return nil, errors.New("missing provider")
	}
	if cfg.APIKey == "" {
		return nil, errors.New("missing api key")
	}
	if cfg.Model == "" {
		return nil, errors.New("missing model")
	}
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 8
	}
	if cfg.Log == nil {
		cfg.Log = io.Discard
	}

	var b backend
	switch strings.ToLower(cfg.Provider) {
	case "openai":
		b = &openAIBackend{
			apiKey:  cfg.APIKey,
			model:   cfg.Model,
			client:  http.DefaultClient,
			baseURL: "https://api.openai.com",
		}
	case "anthropic":
		b = &anthropicBackend{
			apiKey:  cfg.APIKey,
			model:   cfg.Model,
			client:  http.DefaultClient,
			baseURL: "https://api.anthropic.com",
		}
	default:
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}

	return &Runner{
		backend:  b,
		maxSteps: cfg.MaxSteps,
		log:      cfg.Log,
	}, nil
}

func (r *Runner) Run(ctx context.Context, prompt string, tools []mcp.Tool, exec ToolExecutor) (string, error) {
	catalog := buildToolCatalog(tools)
	if len(catalog.Tools) == 0 {
		return "", errors.New("no MCP tools are available")
	}
	return r.backend.Run(ctx, prompt, catalog, exec, r.maxSteps, r.log)
}

func buildToolCatalog(tools []mcp.Tool) toolCatalog {
	catalog := toolCatalog{
		Tools:  make([]catalogTool, 0, len(tools)),
		lookup: make(map[string]catalogTool, len(tools)),
	}
	used := make(map[string]int)

	for i, tool := range tools {
		name := sanitizeToolName(tool.Name)
		if name == "" {
			name = fmt.Sprintf("tool_%d", i+1)
		}
		base := name
		if used[base] > 0 {
			name = fmt.Sprintf("%s_%d", base, used[base]+1)
		}
		used[base]++

		description := strings.TrimSpace(tool.Description)
		if description == "" {
			description = fmt.Sprintf("MCP tool %q", tool.Name)
		}
		if name != tool.Name {
			description = fmt.Sprintf("%s Original MCP tool name: %s.", description, tool.Name)
		}

		schema := tool.InputSchema
		if schema == nil {
			schema = map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}
		}

		item := catalogTool{
			ProviderName: name,
			MCPName:      tool.Name,
			Description:  description,
			InputSchema:  schema,
		}
		catalog.Tools = append(catalog.Tools, item)
		catalog.lookup[item.ProviderName] = item
	}

	return catalog
}

func sanitizeToolName(name string) string {
	if name == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}

	out := strings.Trim(b.String(), "_-")
	if out == "" {
		return ""
	}
	if len(out) > 64 {
		out = out[:64]
	}
	first := rune(out[0])
	if !unicode.IsLetter(first) && first != '_' {
		out = "tool_" + out
		if len(out) > 64 {
			out = out[:64]
		}
	}
	return out
}

func (c toolCatalog) Lookup(providerName string) (catalogTool, bool) {
	tool, ok := c.lookup[providerName]
	return tool, ok
}

func (b *openAIBackend) Run(ctx context.Context, prompt string, catalog toolCatalog, exec ToolExecutor, maxSteps int, log io.Writer) (string, error) {
	messages := []map[string]interface{}{
		{
			"role":    "system",
			"content": systemPrompt(),
		},
		{
			"role":    "user",
			"content": prompt,
		},
	}

	tools := make([]map[string]interface{}, 0, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		tools = append(tools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.ProviderName,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			},
		})
	}

	for step := 0; step < maxSteps; step++ {
		reqBody := map[string]interface{}{
			"model":       b.model,
			"messages":    messages,
			"tools":       tools,
			"tool_choice": "auto",
			"temperature": 0,
			"max_tokens":  1024,
		}

		var resp openAIChatResponse
		if err := doJSONRequest(
			ctx,
			b.client,
			http.MethodPost,
			b.baseURL+"/v1/chat/completions",
			map[string]string{
				"Authorization": "Bearer " + b.apiKey,
				"Content-Type":  "application/json",
			},
			reqBody,
			&resp,
			"openai",
		); err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("openai returned no choices")
		}

		message := resp.Choices[0].Message
		if len(message.ToolCalls) == 0 {
			text := strings.TrimSpace(message.Content)
			if text == "" {
				return "", errors.New("openai returned an empty response")
			}
			return text, nil
		}

		assistant := map[string]interface{}{
			"role":       "assistant",
			"tool_calls": message.ToolCalls,
			"content":    message.Content,
		}
		messages = append(messages, assistant)

		for _, call := range message.ToolCalls {
			tool, ok := catalog.Lookup(call.Function.Name)
			if !ok {
				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": call.ID,
					"content":      "unknown tool requested by model: " + call.Function.Name,
				})
				continue
			}

			args, err := parseModelArguments(call.Function.Arguments)
			if err != nil {
				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": call.ID,
					"content":      "invalid tool arguments: " + err.Error(),
				})
				continue
			}

			logToolCall(log, tool.MCPName, args)

			result, err := exec(ctx, tool.MCPName, args)
			if err != nil {
				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": call.ID,
					"content":      "tool execution failed: " + err.Error(),
				})
				continue
			}

			messages = append(messages, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": call.ID,
				"content":      result.Model,
			})
		}
	}

	return "", fmt.Errorf("openai exceeded max tool steps (%d)", maxSteps)
}

func (b *anthropicBackend) Run(ctx context.Context, prompt string, catalog toolCatalog, exec ToolExecutor, maxSteps int, log io.Writer) (string, error) {
	messages := []anthropicMessage{
		{
			Role:    "user",
			Content: prompt,
		},
	}

	tools := make([]anthropicTool, 0, len(catalog.Tools))
	for _, tool := range catalog.Tools {
		tools = append(tools, anthropicTool{
			Name:        tool.ProviderName,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		})
	}

	for step := 0; step < maxSteps; step++ {
		reqBody := anthropicRequest{
			Model:     b.model,
			MaxTokens: 1024,
			System:    systemPrompt(),
			Messages:  messages,
			Tools:     tools,
		}

		var resp anthropicResponse
		if err := doJSONRequest(
			ctx,
			b.client,
			http.MethodPost,
			b.baseURL+"/v1/messages",
			map[string]string{
				"Content-Type":      "application/json",
				"x-api-key":         b.apiKey,
				"anthropic-version": "2023-06-01",
			},
			reqBody,
			&resp,
			"anthropic",
		); err != nil {
			return "", err
		}
		if len(resp.Content) == 0 {
			return "", errors.New("anthropic returned empty content")
		}

		var textParts []string
		toolResults := make([]map[string]interface{}, 0)
		for _, block := range resp.Content {
			switch block.Type {
			case "text":
				if text := strings.TrimSpace(block.Text); text != "" {
					textParts = append(textParts, text)
				}
			case "tool_use":
				tool, ok := catalog.Lookup(block.Name)
				if !ok {
					toolResults = append(toolResults, map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": block.ID,
						"is_error":    true,
						"content":     "unknown tool requested by model: " + block.Name,
					})
					continue
				}

				args := block.Input
				if args == nil {
					args = map[string]interface{}{}
				}
				logToolCall(log, tool.MCPName, args)

				result, err := exec(ctx, tool.MCPName, args)
				if err != nil {
					toolResults = append(toolResults, map[string]interface{}{
						"type":        "tool_result",
						"tool_use_id": block.ID,
						"is_error":    true,
						"content":     "tool execution failed: " + err.Error(),
					})
					continue
				}

				toolResults = append(toolResults, map[string]interface{}{
					"type":        "tool_result",
					"tool_use_id": block.ID,
					"content":     result.Model,
				})
			}
		}

		if len(toolResults) == 0 {
			text := strings.TrimSpace(strings.Join(textParts, "\n"))
			if text == "" {
				return "", errors.New("anthropic returned an empty response")
			}
			return text, nil
		}

		messages = append(messages, anthropicMessage{
			Role:    "assistant",
			Content: resp.Content,
		})
		messages = append(messages, anthropicMessage{
			Role:    "user",
			Content: toolResults,
		})
	}

	return "", fmt.Errorf("anthropic exceeded max tool steps (%d)", maxSteps)
}

func systemPrompt() string {
	return strings.TrimSpace(`
You are mcp-smoke-agent.
Use the provided MCP tools whenever they match the user's request.
Do not invent tools or arguments.
If no tool fits, say that no MCP tool matches the request.
After tool calls, answer briefly and directly with the result.
`)
}

func logToolCall(w io.Writer, name string, arguments map[string]interface{}) {
	if w == nil {
		return
	}

	payload := "{}"
	if len(arguments) > 0 {
		if raw, err := json.Marshal(arguments); err == nil {
			payload = string(raw)
		}
	}
	fmt.Fprintf(w, "tool> %s %s\n", name, payload)
}

func parseModelArguments(raw string) (map[string]interface{}, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}, nil
	}

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	return args, nil
}

func doJSONRequest(
	ctx context.Context,
	client *http.Client,
	method string,
	url string,
	headers map[string]string,
	reqBody interface{},
	out interface{},
	label string,
) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s api status %d: %s", label, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return err
	}
	return nil
}

type openAIChatResponse struct {
	Choices []openAIChoice `json:"choices"`
}

type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

type openAIMessage struct {
	Content   string           `json:"content"`
	ToolCalls []openAIToolCall `json:"tool_calls"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIFunctionCall `json:"function"`
}

type openAIFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
	Tools     []anthropicTool    `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicTool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description,omitempty"`
	InputSchema map[string]interface{} `json:"input_schema"`
}

type anthropicResponse struct {
	Content []anthropicBlock `json:"content"`
}

type anthropicBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}
