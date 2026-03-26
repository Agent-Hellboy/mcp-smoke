package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Agent-Hellboy/mcp-smoke/internal/agent"
	"github.com/Agent-Hellboy/mcp-smoke/internal/mcp"
)

const (
	defaultProtocol       = "2025-06-18"
	defaultOpenAIModel    = "gpt-4.1-mini"
	defaultAnthropicModel = "claude-3-5-haiku-latest"
)

type config struct {
	server    string
	transport string
	url       string
	command   string
	protocol  string
	provider  string
	model     string
	apiKey    string
	prompt    string
	timeout   time.Duration
	maxSteps  int
	args      []string
}

func main() {
	cfg := parseConfig()

	client, err := mcp.NewClient(cfg.transport, cfg.url, cfg.command, cfg.args, cfg.timeout)
	if err != nil {
		fail(err.Error())
	}
	defer client.Close()

	startupCtx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	initResult, err := mcp.Initialize(startupCtx, client, cfg.protocol, "mcp-agent", "0.1.0")
	if err != nil {
		fail("initialize failed: %v", err)
	}
	_ = mcp.NotifyInitialized(startupCtx, client)

	if !mcp.HasCapability(initResult.Capabilities, "tools") {
		fail("server does not advertise tools capability")
	}

	tools, err := loadTools(startupCtx, client)
	if err != nil {
		fail(err.Error())
	}

	runner, err := agent.New(agent.Config{
		Provider: cfg.provider,
		Model:    cfg.model,
		APIKey:   cfg.apiKey,
		MaxSteps: cfg.maxSteps,
		Log:      os.Stderr,
	})
	if err != nil {
		fail(err.Error())
	}

	if prompt := strings.TrimSpace(cfg.prompt); prompt != "" {
		answer, err := runPrompt(cfg.timeout, runner, client, prompt)
		if err != nil {
			fail(err.Error())
		}
		fmt.Println(answer)
		return
	}

	if piped, prompt := readPromptFromPipe(os.Stdin); piped {
		if prompt == "" {
			fail("stdin was piped but no prompt was provided")
		}
		answer, err := runPrompt(cfg.timeout, runner, client, prompt)
		if err != nil {
			fail(err.Error())
		}
		fmt.Println(answer)
		return
	}

	printBanner(cfg, initResult, tools)
	runREPL(cfg.timeout, runner, client)
}

func parseConfig() config {
	var cfg config
	flag.StringVar(&cfg.server, "server", "", "server spec: http URL, stdio command string, or JSON array command")
	flag.StringVar(&cfg.transport, "transport", "", "transport: stdio or http")
	flag.StringVar(&cfg.url, "url", "", "streamable HTTP endpoint url")
	flag.StringVar(&cfg.command, "command", "", "stdio command to run")
	flag.StringVar(&cfg.protocol, "protocol", defaultProtocol, "client protocol version")
	flag.StringVar(&cfg.provider, "provider", "", "llm provider: openai or anthropic")
	flag.StringVar(&cfg.model, "model", "", "llm model name")
	flag.StringVar(&cfg.apiKey, "api-key", "", "provider API key")
	flag.StringVar(&cfg.prompt, "prompt", "", "single prompt to run; omit for interactive mode")
	flag.DurationVar(&cfg.timeout, "timeout", 60*time.Second, "timeout per prompt")
	flag.IntVar(&cfg.maxSteps, "max-steps", 8, "maximum model/tool turns per prompt")
	flag.Parse()

	cfg.args = flag.Args()
	if cfg.protocol == "" {
		cfg.protocol = defaultProtocol
	}
	if err := applyServerSpec(&cfg); err != nil {
		fail(err.Error())
	}

	provider, err := resolveProvider(cfg.provider)
	if err != nil {
		fail(err.Error())
	}
	cfg.provider = provider

	if cfg.apiKey == "" {
		cfg.apiKey = providerAPIKey(cfg.provider)
	}
	if cfg.apiKey == "" {
		fail("missing API key for provider %q", cfg.provider)
	}

	if cfg.model == "" {
		cfg.model = providerModel(cfg.provider)
	}
	if cfg.model == "" {
		fail("missing model for provider %q", cfg.provider)
	}

	return cfg
}

func applyServerSpec(cfg *config) error {
	spec := strings.TrimSpace(cfg.server)
	if spec == "" {
		return nil
	}

	if cfg.transport != "" || cfg.url != "" || cfg.command != "" || len(cfg.args) > 0 {
		return errors.New("--server cannot be combined with --transport, --url, --command, or trailing stdio args")
	}

	transport, url, command, args, err := parseServerSpec(spec)
	if err != nil {
		return err
	}

	cfg.transport = transport
	cfg.url = url
	cfg.command = command
	cfg.args = args
	return nil
}

func parseServerSpec(spec string) (transport, url, command string, args []string, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", "", nil, errors.New("empty --server value")
	}

	switch {
	case strings.HasPrefix(spec, "http://"), strings.HasPrefix(spec, "https://"):
		return "http", spec, "", nil, nil
	case strings.HasPrefix(spec, "stdio:"):
		spec = strings.TrimSpace(strings.TrimPrefix(spec, "stdio:"))
	case strings.HasPrefix(spec, "cmd:"):
		spec = strings.TrimSpace(strings.TrimPrefix(spec, "cmd:"))
	}

	if spec == "" {
		return "", "", "", nil, errors.New("stdio --server value is empty")
	}

	if strings.HasPrefix(spec, "[") {
		var parts []string
		if err := json.Unmarshal([]byte(spec), &parts); err != nil {
			return "", "", "", nil, fmt.Errorf("invalid --server JSON array: %w", err)
		}
		if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
			return "", "", "", nil, errors.New("stdio --server JSON array must include a command")
		}
		return "stdio", "", parts[0], parts[1:], nil
	}

	parts := strings.Fields(spec)
	if len(parts) == 0 {
		return "", "", "", nil, errors.New("stdio --server value is empty")
	}
	return "stdio", "", parts[0], parts[1:], nil
}

func resolveProvider(explicit string) (string, error) {
	if explicit != "" {
		switch explicit {
		case "openai", "anthropic":
			return explicit, nil
		default:
			return "", fmt.Errorf("unsupported provider %q", explicit)
		}
	}

	hasOpenAI := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")) != ""
	hasAnthropic := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""

	switch {
	case hasOpenAI && !hasAnthropic:
		return "openai", nil
	case hasAnthropic && !hasOpenAI:
		return "anthropic", nil
	case hasOpenAI && hasAnthropic:
		return "", errors.New("both OPENAI_API_KEY and ANTHROPIC_API_KEY are set; pass --provider")
	default:
		return "", errors.New("set OPENAI_API_KEY or ANTHROPIC_API_KEY, or pass --api-key with --provider")
	}
}

func providerAPIKey(provider string) string {
	switch provider {
	case "openai":
		return strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	case "anthropic":
		return strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	default:
		return ""
	}
}

func providerModel(provider string) string {
	switch provider {
	case "openai":
		if model := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); model != "" {
			return model
		}
		return defaultOpenAIModel
	case "anthropic":
		if model := strings.TrimSpace(os.Getenv("ANTHROPIC_MODEL")); model != "" {
			return model
		}
		return defaultAnthropicModel
	default:
		return ""
	}
}

func readPromptFromPipe(r *os.File) (bool, string) {
	info, err := r.Stat()
	if err != nil {
		return false, ""
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return false, ""
	}

	body, err := io.ReadAll(r)
	if err != nil {
		return true, ""
	}
	return true, strings.TrimSpace(string(body))
}

func printBanner(cfg config, initResult mcp.InitializeResult, tools []mcp.Tool) {
	serverName := "mcp-server"
	if name, ok := initResult.ServerInfo["name"].(string); ok && name != "" {
		serverName = name
	}

	fmt.Fprintf(os.Stderr, "connected to %s via %s using %s/%s\n", serverName, transportLabel(cfg), cfg.provider, cfg.model)
	fmt.Fprintln(os.Stderr, "type plain English, `tools` to inspect the MCP tools, or `quit` to exit")
	fmt.Fprintln(os.Stderr, "available tools:")
	printTools(os.Stderr, tools)
}

func runREPL(timeout time.Duration, runner *agent.Runner, client mcp.Client) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Fprint(os.Stderr, "> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		switch line {
		case "quit", "exit":
			return
		case "tools", "help":
			tools, err := loadToolsWithTimeout(timeout, client)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				continue
			}
			printTools(os.Stderr, tools)
			continue
		}

		answer, err := runPrompt(timeout, runner, client, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}
		fmt.Println(answer)
	}
}

func runPrompt(timeout time.Duration, runner *agent.Runner, client mcp.Client, prompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	tools, err := loadTools(ctx, client)
	if err != nil {
		return "", err
	}

	return runner.Run(ctx, prompt, tools, func(ctx context.Context, toolName string, arguments map[string]interface{}) (agent.ExecutionResult, error) {
		raw, err := mcp.CallTool(ctx, client, toolName, arguments)
		if err != nil {
			return agent.ExecutionResult{}, err
		}
		return agent.ExecutionResult{
			Model: prettyJSON(raw),
		}, nil
	})
}

func loadToolsWithTimeout(timeout time.Duration, client mcp.Client) ([]mcp.Tool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return loadTools(ctx, client)
}

func loadTools(ctx context.Context, client mcp.Client) ([]mcp.Tool, error) {
	tools, err := mcp.ListTools(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("tools/list failed: %w", err)
	}
	if len(tools) == 0 {
		return nil, errors.New("server returned no tools")
	}
	return tools, nil
}

func transportLabel(cfg config) string {
	switch {
	case cfg.transport == "http" || (cfg.transport == "" && cfg.url != ""):
		return "http"
	default:
		return "stdio"
	}
}

func printTools(w io.Writer, tools []mcp.Tool) {
	for _, tool := range tools {
		line := tool.Name
		if desc := strings.TrimSpace(tool.Description); desc != "" {
			line += " - " + desc
		}
		fmt.Fprintln(w, line)
	}
}

func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err == nil {
		return buf.String()
	}
	return strings.TrimSpace(string(raw))
}

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
