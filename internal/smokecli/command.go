package smokecli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Agent-Hellboy/mcp-smoke/internal/mcp"
)

const (
	DefaultProtocol = "2025-06-18"
	ClientVersion   = "0.3.0"
)

type stepResult struct {
	Name       string          `json:"name"`
	OK         bool            `json:"ok"`
	Skipped    bool            `json:"skipped,omitempty"`
	DurationMs int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

type output struct {
	OK           bool                   `json:"ok"`
	Transport    string                 `json:"transport"`
	URL          string                 `json:"url,omitempty"`
	Command      string                 `json:"command,omitempty"`
	Args         []string               `json:"args,omitempty"`
	Protocol     string                 `json:"protocol_version,omitempty"`
	Capabilities map[string]interface{} `json:"capabilities,omitempty"`
	Steps        []stepResult           `json:"steps"`
	StartedAt    string                 `json:"started_at"`
	FinishedAt   string                 `json:"finished_at"`
	DurationMs   int64                  `json:"duration_ms"`
}

type config struct {
	transport   string
	url         string
	command     string
	timeout     time.Duration
	noCall      bool
	protocol    string
	toolName    string
	toolArgs    string
	promptName  string
	promptArgs  string
	resourceURI string
	args        []string
}

func Run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}

	started := time.Now().UTC()
	out := output{
		StartedAt: started.Format(time.RFC3339),
		Transport: cfg.transport,
		URL:       cfg.url,
		Command:   cfg.command,
		Args:      cfg.args,
	}

	c, err := mcp.NewClient(cfg.transport, cfg.url, cfg.command, cfg.args, cfg.timeout)
	if err != nil {
		failAndPrint(stdout, out, err)
		return 1
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	initStep, caps, protocolVer := doInitialize(ctx, c, cfg.protocol)
	out.Steps = append(out.Steps, initStep)
	out.Capabilities = caps
	if protocolVer != "" {
		out.Protocol = protocolVer
	}
	_ = mcp.NotifyInitialized(ctx, c)

	steps := make([]stepResult, 0, 6)
	steps = append(steps, listTools(ctx, c, caps))
	steps = append(steps, listPrompts(ctx, c, caps))
	steps = append(steps, listResources(ctx, c, caps))

	if !cfg.noCall {
		if s := callTool(ctx, c, caps, cfg.toolName, cfg.toolArgs); s.Name != "" {
			steps = append(steps, s)
		}
		if s := getPrompt(ctx, c, caps, cfg.promptName, cfg.promptArgs); s.Name != "" {
			steps = append(steps, s)
		}
		if s := readResource(ctx, c, caps, cfg.resourceURI); s.Name != "" {
			steps = append(steps, s)
		}
	}

	out.Steps = append(out.Steps, steps...)
	out.OK = allStepsOK(out.Steps)

	finished := time.Now().UTC()
	out.FinishedAt = finished.Format(time.RFC3339)
	out.DurationMs = finished.Sub(started).Milliseconds()
	printJSON(stdout, out)
	if out.OK {
		return 0
	}
	return 1
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	var cfg config

	fs := flag.NewFlagSet("smoke", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.transport, "transport", "", "transport: stdio or http")
	fs.StringVar(&cfg.url, "url", "", "streamable HTTP endpoint url")
	fs.StringVar(&cfg.command, "command", "", "stdio command to run")
	fs.DurationVar(&cfg.timeout, "timeout", 15*time.Second, "timeout per request")
	fs.BoolVar(&cfg.noCall, "no-call", false, "skip tool/prompt/resource calls")
	fs.StringVar(&cfg.protocol, "protocol", DefaultProtocol, "client protocol version")
	fs.StringVar(&cfg.toolName, "tool-name", "", "specific tool name to call")
	fs.StringVar(&cfg.toolArgs, "tool-args", "{}", "json object passed to tools/call")
	fs.StringVar(&cfg.promptName, "prompt-name", "", "specific prompt name to get")
	fs.StringVar(&cfg.promptArgs, "prompt-args", "{}", "json object passed to prompts/get")
	fs.StringVar(&cfg.resourceURI, "resource-uri", "", "specific resource uri to read")

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	cfg.args = fs.Args()
	if cfg.protocol == "" {
		cfg.protocol = DefaultProtocol
	}
	return cfg, nil
}

func failAndPrint(stdout io.Writer, out output, err error) {
	out.OK = false
	out.Steps = append(out.Steps, stepResult{
		Name:       "startup",
		OK:         false,
		Error:      err.Error(),
		DurationMs: 0,
	})
	out.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	printJSON(stdout, out)
}

func printJSON(stdout io.Writer, out output) {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func allStepsOK(steps []stepResult) bool {
	for _, s := range steps {
		if !s.OK && !s.Skipped {
			return false
		}
	}
	return true
}

func doInitialize(ctx context.Context, c mcp.Client, protocol string) (stepResult, map[string]interface{}, string) {
	start := time.Now()
	result, err := mcp.Initialize(ctx, c, protocol, "mcp-smoke-agent", ClientVersion)
	if err != nil {
		return stepResult{Name: "initialize", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil, ""
	}

	detail, _ := json.Marshal(result)
	return stepResult{
		Name:       "initialize",
		OK:         true,
		Detail:     detail,
		DurationMs: time.Since(start).Milliseconds(),
	}, result.Capabilities, result.ProtocolVersion
}

func listTools(ctx context.Context, c mcp.Client, caps map[string]interface{}) stepResult {
	if !mcp.HasCapability(caps, "tools") {
		return stepResult{Name: "tools/list", OK: true, Skipped: true}
	}

	start := time.Now()
	tools, err := mcp.ListTools(ctx, c)
	if err != nil {
		return stepResult{Name: "tools/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}

	detail, _ := json.Marshal(map[string]interface{}{"count": len(tools)})
	return stepResult{Name: "tools/list", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
}

func listPrompts(ctx context.Context, c mcp.Client, caps map[string]interface{}) stepResult {
	if !mcp.HasCapability(caps, "prompts") {
		return stepResult{Name: "prompts/list", OK: true, Skipped: true}
	}

	start := time.Now()
	prompts, err := mcp.ListPrompts(ctx, c)
	if err != nil {
		return stepResult{Name: "prompts/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}

	detail, _ := json.Marshal(map[string]interface{}{"count": len(prompts)})
	return stepResult{Name: "prompts/list", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
}

func listResources(ctx context.Context, c mcp.Client, caps map[string]interface{}) stepResult {
	if !mcp.HasCapability(caps, "resources") {
		return stepResult{Name: "resources/list", OK: true, Skipped: true}
	}

	start := time.Now()
	resources, err := mcp.ListResources(ctx, c)
	if err != nil {
		return stepResult{Name: "resources/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}

	detail, _ := json.Marshal(map[string]interface{}{"count": len(resources)})
	return stepResult{Name: "resources/list", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
}

func callTool(ctx context.Context, c mcp.Client, caps map[string]interface{}, requestedName, rawArgs string) stepResult {
	if !mcp.HasCapability(caps, "tools") {
		return stepResult{Name: "tools/call", OK: true, Skipped: true}
	}

	start := time.Now()
	args, err := parseJSONArgs(rawArgs)
	if err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: "invalid --tool-args json", DurationMs: time.Since(start).Milliseconds()}
	}

	tools, err := mcp.ListTools(ctx, c)
	if err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if len(tools) == 0 {
		return stepResult{Name: "tools/call", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}

	tool, selectionErr, detail := selectTool(tools, requestedName, args)
	if selectionErr != nil {
		return stepResult{Name: "tools/call", OK: false, Error: selectionErr.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if tool.Name == "" {
		return stepResult{Name: "tools/call", OK: true, Skipped: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
	}

	if _, err := mcp.CallTool(ctx, c, tool.Name, args); err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}

	callDetail, _ := json.Marshal(map[string]interface{}{"name": tool.Name})
	return stepResult{Name: "tools/call", OK: true, Detail: callDetail, DurationMs: time.Since(start).Milliseconds()}
}

func getPrompt(ctx context.Context, c mcp.Client, caps map[string]interface{}, requestedName, rawArgs string) stepResult {
	if !mcp.HasCapability(caps, "prompts") {
		return stepResult{Name: "prompts/get", OK: true, Skipped: true}
	}

	start := time.Now()
	args, err := parseJSONArgs(rawArgs)
	if err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: "invalid --prompt-args json", DurationMs: time.Since(start).Milliseconds()}
	}

	prompts, err := mcp.ListPrompts(ctx, c)
	if err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if len(prompts) == 0 {
		return stepResult{Name: "prompts/get", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}

	prompt, selectionErr, detail := selectPrompt(prompts, requestedName, args)
	if selectionErr != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: selectionErr.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if prompt.Name == "" {
		return stepResult{Name: "prompts/get", OK: true, Skipped: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
	}

	if _, err := mcp.GetPrompt(ctx, c, prompt.Name, args); err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}

	getDetail, _ := json.Marshal(map[string]interface{}{"name": prompt.Name})
	return stepResult{Name: "prompts/get", OK: true, Detail: getDetail, DurationMs: time.Since(start).Milliseconds()}
}

func readResource(ctx context.Context, c mcp.Client, caps map[string]interface{}, resourceURI string) stepResult {
	if !mcp.HasCapability(caps, "resources") {
		return stepResult{Name: "resources/read", OK: true, Skipped: true}
	}

	start := time.Now()
	uri := resourceURI
	if uri == "" {
		resources, err := mcp.ListResources(ctx, c)
		if err != nil {
			return stepResult{Name: "resources/read", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if len(resources) == 0 {
			return stepResult{Name: "resources/read", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
		}
		uri = resources[0].URI
	}
	if uri == "" {
		return stepResult{Name: "resources/read", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}

	if _, err := mcp.ReadResource(ctx, c, uri); err != nil {
		return stepResult{Name: "resources/read", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}

	detail, _ := json.Marshal(map[string]interface{}{"uri": uri})
	return stepResult{Name: "resources/read", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
}

func selectTool(tools []mcp.Tool, requestedName string, args map[string]interface{}) (mcp.Tool, error, json.RawMessage) {
	if requestedName != "" {
		for _, tool := range tools {
			if tool.Name != requestedName {
				continue
			}
			missing := missingSchemaFields(tool.InputSchema, args)
			if len(missing) > 0 {
				return mcp.Tool{}, fmt.Errorf("tool %q is missing required args: %v", tool.Name, missing), nil
			}
			return tool, nil, nil
		}
		return mcp.Tool{}, fmt.Errorf("tool %q not found", requestedName), nil
	}

	for _, tool := range tools {
		if len(missingSchemaFields(tool.InputSchema, args)) == 0 {
			return tool, nil, nil
		}
	}

	detail, _ := json.Marshal(map[string]interface{}{
		"reason": "no tool matched the provided --tool-args",
	})
	return mcp.Tool{}, nil, detail
}

func selectPrompt(prompts []mcp.Prompt, requestedName string, args map[string]interface{}) (mcp.Prompt, error, json.RawMessage) {
	if requestedName != "" {
		for _, prompt := range prompts {
			if prompt.Name != requestedName {
				continue
			}
			missing := missingPromptArgs(prompt.Arguments, args)
			if len(missing) > 0 {
				return mcp.Prompt{}, fmt.Errorf("prompt %q is missing required args: %v", prompt.Name, missing), nil
			}
			return prompt, nil, nil
		}
		return mcp.Prompt{}, fmt.Errorf("prompt %q not found", requestedName), nil
	}

	for _, prompt := range prompts {
		if len(missingPromptArgs(prompt.Arguments, args)) == 0 {
			return prompt, nil, nil
		}
	}

	detail, _ := json.Marshal(map[string]interface{}{
		"reason": "no prompt matched the provided --prompt-args",
	})
	return mcp.Prompt{}, nil, detail
}

func missingSchemaFields(schema map[string]interface{}, args map[string]interface{}) []string {
	var missing []string
	for _, name := range requiredSchemaFields(schema) {
		value, ok := args[name]
		if !ok || value == nil {
			missing = append(missing, name)
		}
	}
	return missing
}

func missingPromptArgs(arguments []mcp.PromptArgument, args map[string]interface{}) []string {
	var missing []string
	for _, argument := range arguments {
		if !argument.Required {
			continue
		}
		value, ok := args[argument.Name]
		if !ok || value == nil {
			missing = append(missing, argument.Name)
		}
	}
	return missing
}

func requiredSchemaFields(schema map[string]interface{}) []string {
	if schema == nil {
		return nil
	}

	raw := schema["required"]
	switch required := raw.(type) {
	case []string:
		return append([]string(nil), required...)
	case []interface{}:
		names := make([]string, 0, len(required))
		for _, item := range required {
			name, ok := item.(string)
			if ok && name != "" {
				names = append(names, name)
			}
		}
		return names
	default:
		return nil
	}
}

func parseJSONArgs(raw string) (map[string]interface{}, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	return args, nil
}
