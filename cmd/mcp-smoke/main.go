package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Agent-Hellboy/mcp-smoke/internal/mcp"
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

func main() {
	var (
		transport  = flag.String("transport", "", "transport: stdio or http")
		url        = flag.String("url", "", "streamable HTTP endpoint url")
		command    = flag.String("command", "", "stdio command to run")
		timeout    = flag.Duration("timeout", 15*time.Second, "timeout per request")
		noCall     = flag.Bool("no-call", false, "skip tool/prompt/resource calls")
		protocol   = flag.String("protocol", "2025-06-18", "client protocol version")
		toolArgs   = flag.String("tool-args", "{}", "json object passed to tools/call (if required args are not present in schema)")
		promptArgs = flag.String("prompt-args", "{}", "json object passed to prompts/get")
		resourceUR = flag.String("resource-uri", "", "specific resource uri to read")
	)
	flag.Parse()

	started := time.Now().UTC()
	out := output{
		StartedAt: started.Format(time.RFC3339),
		Transport: *transport,
		URL:       *url,
		Command:   *command,
		Args:      flag.Args(),
	}

	c, err := mcp.NewClient(*transport, *url, *command, flag.Args(), *timeout)
	if err != nil {
		failAndPrint(out, err)
		return
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	initStep, caps, protocolVer := doInitialize(ctx, c, *protocol)
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

	if !*noCall {
		if s := callFirstTool(ctx, c, caps, *toolArgs); s.Name != "" {
			steps = append(steps, s)
		}
		if s := getFirstPrompt(ctx, c, caps, *promptArgs); s.Name != "" {
			steps = append(steps, s)
		}
		if s := readFirstResource(ctx, c, caps, *resourceUR); s.Name != "" {
			steps = append(steps, s)
		}
	}

	out.Steps = append(out.Steps, steps...)
	out.OK = allStepsOK(out.Steps)

	finished := time.Now().UTC()
	out.FinishedAt = finished.Format(time.RFC3339)
	out.DurationMs = finished.Sub(started).Milliseconds()
	printJSON(out)
}

func failAndPrint(out output, err error) {
	out.OK = false
	out.Steps = append(out.Steps, stepResult{
		Name:       "startup",
		OK:         false,
		Error:      err.Error(),
		DurationMs: 0,
	})
	out.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	printJSON(out)
}

func printJSON(out output) {
	enc := json.NewEncoder(os.Stdout)
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
	result, err := mcp.Initialize(ctx, c, protocol, "mcp-smoke", "0.1.0")
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

func callFirstTool(ctx context.Context, c mcp.Client, caps map[string]interface{}, rawArgs string) stepResult {
	if !mcp.HasCapability(caps, "tools") {
		return stepResult{Name: "tools/call", OK: true, Skipped: true}
	}

	start := time.Now()
	tools, err := mcp.ListTools(ctx, c)
	if err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if len(tools) == 0 {
		return stepResult{Name: "tools/call", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}

	tool := tools[0]
	if hasRequiredFields(tool.InputSchema) {
		return stepResult{Name: "tools/call", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}

	args, err := parseJSONArgs(rawArgs)
	if err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: "invalid --tool-args json", DurationMs: time.Since(start).Milliseconds()}
	}
	if _, err := mcp.CallTool(ctx, c, tool.Name, args); err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	return stepResult{Name: "tools/call", OK: true, DurationMs: time.Since(start).Milliseconds()}
}

func getFirstPrompt(ctx context.Context, c mcp.Client, caps map[string]interface{}, rawArgs string) stepResult {
	if !mcp.HasCapability(caps, "prompts") {
		return stepResult{Name: "prompts/get", OK: true, Skipped: true}
	}

	start := time.Now()
	prompts, err := mcp.ListPrompts(ctx, c)
	if err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if len(prompts) == 0 {
		return stepResult{Name: "prompts/get", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}

	prompt := prompts[0]
	for _, arg := range prompt.Arguments {
		if arg.Required {
			return stepResult{Name: "prompts/get", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
		}
	}

	args, err := parseJSONArgs(rawArgs)
	if err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: "invalid --prompt-args json", DurationMs: time.Since(start).Milliseconds()}
	}
	if _, err := mcp.GetPrompt(ctx, c, prompt.Name, args); err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	return stepResult{Name: "prompts/get", OK: true, DurationMs: time.Since(start).Milliseconds()}
}

func readFirstResource(ctx context.Context, c mcp.Client, caps map[string]interface{}, resourceURI string) stepResult {
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
	return stepResult{Name: "resources/read", OK: true, DurationMs: time.Since(start).Milliseconds()}
}

func hasRequiredFields(schema map[string]interface{}) bool {
	if schema == nil {
		return false
	}
	required, ok := schema["required"].([]interface{})
	return ok && len(required) > 0
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

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
