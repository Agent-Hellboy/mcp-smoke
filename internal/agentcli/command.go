package agentcli

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
	DefaultProtocol       = "2025-06-18"
	DefaultOpenAIModel    = "gpt-4.1-mini"
	DefaultAnthropicModel = "claude-3-5-haiku-latest"
	DefaultEnvFile        = ".env"
	ClientVersion         = "0.3.0"
)

type config struct {
	server    string
	envFile   string
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

func Run(args []string, stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		failf(stderr, err.Error())
		return 2
	}

	client, err := mcp.NewClient(cfg.transport, cfg.url, cfg.command, cfg.args, cfg.timeout)
	if err != nil {
		failf(stderr, err.Error())
		return 1
	}
	defer client.Close()

	startupCtx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()

	initResult, err := mcp.Initialize(startupCtx, client, cfg.protocol, "mcp-smoke-agent", ClientVersion)
	if err != nil {
		failf(stderr, "initialize failed: %v", err)
		return 1
	}
	_ = mcp.NotifyInitialized(startupCtx, client)

	if !mcp.HasCapability(initResult.Capabilities, "tools") {
		failf(stderr, "server does not advertise tools capability")
		return 1
	}

	tools, err := loadTools(startupCtx, client)
	if err != nil {
		failf(stderr, err.Error())
		return 1
	}

	runner, err := agent.New(agent.Config{
		Provider: cfg.provider,
		Model:    cfg.model,
		APIKey:   cfg.apiKey,
		MaxSteps: cfg.maxSteps,
		Log:      stderr,
	})
	if err != nil {
		failf(stderr, err.Error())
		return 1
	}

	if prompt := strings.TrimSpace(cfg.prompt); prompt != "" {
		answer, err := runPrompt(cfg.timeout, runner, client, prompt)
		if err != nil {
			failf(stderr, err.Error())
			return 1
		}
		fmt.Fprintln(stdout, answer)
		return 0
	}

	if piped, prompt := readPromptFromPipe(os.Stdin); piped {
		if prompt == "" {
			failf(stderr, "stdin was piped but no prompt was provided")
			return 1
		}
		answer, err := runPrompt(cfg.timeout, runner, client, prompt)
		if err != nil {
			failf(stderr, err.Error())
			return 1
		}
		fmt.Fprintln(stdout, answer)
		return 0
	}

	printBanner(stderr, cfg, initResult, tools)
	runREPL(stdout, stderr, cfg.timeout, runner, client)
	return 0
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	var cfg config

	fs := flag.NewFlagSet("mcp-smoke-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.server, "server", "", "server spec: http URL, stdio command string, or JSON array command")
	fs.StringVar(&cfg.envFile, "env-file", DefaultEnvFile, "dotenv file to read for API keys and model names; empty disables")
	fs.StringVar(&cfg.transport, "transport", "", "transport: stdio or http")
	fs.StringVar(&cfg.url, "url", "", "streamable HTTP endpoint url")
	fs.StringVar(&cfg.command, "command", "", "stdio command to run")
	fs.StringVar(&cfg.protocol, "protocol", DefaultProtocol, "client protocol version")
	fs.StringVar(&cfg.provider, "provider", "", "llm provider: openai or anthropic")
	fs.StringVar(&cfg.model, "model", "", "llm model name")
	fs.StringVar(&cfg.apiKey, "api-key", "", "provider API key")
	fs.StringVar(&cfg.prompt, "prompt", "", "single prompt to run; omit for stdin or interactive mode")
	fs.DurationVar(&cfg.timeout, "timeout", 60*time.Second, "timeout per prompt")
	fs.IntVar(&cfg.maxSteps, "max-steps", 8, "maximum model/tool turns per prompt")

	if err := fs.Parse(args); err != nil {
		return cfg, err
	}

	rest := fs.Args()
	if cfg.prompt == "" && len(rest) > 0 && canUsePositionalPrompt(cfg) {
		cfg.prompt = strings.TrimSpace(strings.Join(rest, " "))
	} else {
		cfg.args = rest
	}
	if cfg.protocol == "" {
		cfg.protocol = DefaultProtocol
	}
	if err := applyServerSpec(&cfg); err != nil {
		return cfg, err
	}
	if cfg.url == "" && cfg.command == "" {
		return cfg, errors.New("missing MCP server; pass --server or use --transport with --url/--command")
	}

	fileEnv, err := readEnvFile(cfg.envFile)
	if err != nil {
		return cfg, err
	}

	provider, err := resolveProvider(cfg.provider, fileEnv)
	if err != nil {
		return cfg, err
	}
	cfg.provider = provider

	if cfg.apiKey == "" {
		cfg.apiKey = providerAPIKey(cfg.provider, fileEnv)
	}
	if cfg.apiKey == "" {
		return cfg, fmt.Errorf("missing API key for provider %q; set it in %s", cfg.provider, apiKeyLocationHint(cfg.envFile))
	}

	if cfg.model == "" {
		cfg.model = providerModel(cfg.provider, fileEnv)
	}
	if cfg.model == "" {
		return cfg, fmt.Errorf("missing model for provider %q", cfg.provider)
	}

	return cfg, nil
}

func canUsePositionalPrompt(cfg config) bool {
	if cfg.server != "" {
		return true
	}
	if cfg.command != "" || cfg.transport == "stdio" {
		return false
	}
	return true
}

func applyServerSpec(cfg *config) error {
	spec := strings.TrimSpace(cfg.server)
	if spec == "" {
		return nil
	}

	if cfg.transport != "" || cfg.url != "" || cfg.command != "" || len(cfg.args) > 0 {
		return errors.New("--server cannot be combined with --transport, --url, --command, or trailing stdio args")
	}

	transport, url, command, args, err := ParseServerSpec(spec)
	if err != nil {
		return err
	}

	cfg.transport = transport
	cfg.url = url
	cfg.command = command
	cfg.args = args
	return nil
}

func ParseServerSpec(spec string) (transport, url, command string, args []string, err error) {
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

func resolveProvider(explicit string, fileEnv map[string]string) (string, error) {
	if explicit != "" {
		switch explicit {
		case "openai", "anthropic":
			return explicit, nil
		default:
			return "", fmt.Errorf("unsupported provider %q", explicit)
		}
	}

	hasOpenAI := envValue("OPENAI_API_KEY", fileEnv) != ""
	hasAnthropic := envValue("ANTHROPIC_API_KEY", fileEnv) != ""

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

func providerAPIKey(provider string, fileEnv map[string]string) string {
	switch provider {
	case "openai":
		return envValue("OPENAI_API_KEY", fileEnv)
	case "anthropic":
		return envValue("ANTHROPIC_API_KEY", fileEnv)
	default:
		return ""
	}
}

func providerModel(provider string, fileEnv map[string]string) string {
	switch provider {
	case "openai":
		if model := envValue("OPENAI_MODEL", fileEnv); model != "" {
			return model
		}
		return DefaultOpenAIModel
	case "anthropic":
		if model := envValue("ANTHROPIC_MODEL", fileEnv); model != "" {
			return model
		}
		return DefaultAnthropicModel
	default:
		return ""
	}
}

func readEnvFile(path string) (map[string]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]string{}, nil
	}

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) && path == DefaultEnvFile {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	values, err := ParseDotEnv(f)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return values, nil
}

func ParseDotEnv(r io.Reader) (map[string]string, error) {
	values := make(map[string]string)
	scanner := bufio.NewScanner(r)

	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}

		key, rawValue, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected KEY=VALUE", lineNo)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo)
		}

		value := strings.TrimSpace(rawValue)
		if len(value) >= 2 {
			switch {
			case value[0] == '"' && value[len(value)-1] == '"':
				value = value[1 : len(value)-1]
			case value[0] == '\'' && value[len(value)-1] == '\'':
				value = value[1 : len(value)-1]
			}
		}

		values[key] = value
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func envValue(key string, fileEnv map[string]string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return strings.TrimSpace(fileEnv[key])
}

func apiKeyLocationHint(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "the shell environment or --api-key"
	}
	return fmt.Sprintf("%s, the shell environment, or --api-key", path)
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

func printBanner(stderr io.Writer, cfg config, initResult mcp.InitializeResult, tools []mcp.Tool) {
	serverName := "mcp-server"
	if name, ok := initResult.ServerInfo["name"].(string); ok && name != "" {
		serverName = name
	}

	fmt.Fprintf(stderr, "connected to %s via %s using %s/%s\n", serverName, transportLabel(cfg), cfg.provider, cfg.model)
	fmt.Fprintln(stderr, "type plain English, `tools` to inspect the MCP tools, or `quit` to exit")
	fmt.Fprintln(stderr, "available tools:")
	printTools(stderr, tools)
}

func runREPL(stdout, stderr io.Writer, timeout time.Duration, runner *agent.Runner, client mcp.Client) {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Fprint(stderr, "> ")
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
				fmt.Fprintf(stderr, "error: %v\n", err)
				continue
			}
			printTools(stderr, tools)
			continue
		}

		answer, err := runPrompt(timeout, runner, client, line)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			continue
		}
		fmt.Fprintln(stdout, answer)
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

func failf(w io.Writer, format string, args ...interface{}) {
	fmt.Fprintf(w, format+"\n", args...)
}
