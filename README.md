# mcp-smoke

![CI](https://github.com/Agent-Hellboy/mcp-smoke/actions/workflows/ci.yml/badge.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/Agent-Hellboy/mcp-smoke.svg)](https://pkg.go.dev/github.com/Agent-Hellboy/mcp-smoke)
[![Go Report Card](https://goreportcard.com/badge/github.com/Agent-Hellboy/mcp-smoke)](https://goreportcard.com/report/github.com/Agent-Hellboy/mcp-smoke)

Single MCP CLI for plain-English MCP smoke tests, plus an optional low-level protocol smoke mode.

## Why use it in e2e harnesses

`mcp-smoke-agent` is useful when you are building or testing MCP server infrastructure and want one realistic client in your end-to-end test harness.

Instead of hand-writing raw MCP requests for every check, you can drive the server through the CLI with a plain-English request, let it invoke the real MCP tool path, and verify the surrounding infrastructure behavior. That is useful for checking things like routing, auth, logging, tracing, and whether analytics are generated correctly for real tool-invocation flows.

## Install

```bash
go install github.com/Agent-Hellboy/mcp-smoke/cmd/mcp-smoke-agent@latest
```

Install a tagged release:

```bash
go install github.com/Agent-Hellboy/mcp-smoke/cmd/mcp-smoke-agent@v0.3.0
```

Prebuilt release assets are also published for:

- `linux/amd64`
- `linux/arm64`
- `darwin/amd64`
- `darwin/arm64`
- `windows/amd64`
- `windows/arm64`

Asset names follow this pattern:

- `mcp-smoke-agent_vX.Y.Z_<os>_<arch>.tar.gz`
- `mcp-smoke-agent_vX.Y.Z_windows_<arch>.zip`
- `checksums.txt`

Example CI download:

```bash
VERSION=v0.3.0
curl -fsSL -o mcp-smoke-agent.tar.gz \
  "https://github.com/Agent-Hellboy/mcp-smoke/releases/download/${VERSION}/mcp-smoke-agent_${VERSION}_linux_amd64.tar.gz"
tar -xzf mcp-smoke-agent.tar.gz
./mcp-smoke-agent --help
```

## Usage

Default mode:

- `mcp-smoke-agent [flags] [plain English request]`

Optional raw protocol report:

- `mcp-smoke-agent smoke [flags]`

### Plain-English MCP smoke requests

The default command connects to an MCP server, lists its tools, and lets an OpenAI or Anthropic model decide which MCP tool to call from plain-English input.

OpenAI with a single server flag:

```bash
cat > .env <<'EOF'
OPENAI_API_KEY=your-key-here
EOF

go run ./cmd/mcp-smoke-agent --server "go run ./cmd/mcp-test-server"
```

Anthropic with a single server flag:

```bash
export ANTHROPIC_API_KEY=...
go run ./cmd/mcp-smoke-agent --provider anthropic --server "go run ./cmd/mcp-test-server"
```

One-shot prompt:

```bash
export OPENAI_API_KEY=...
go run ./cmd/mcp-smoke-agent \
  --server "go run ./cmd/mcp-test-server" \
  --prompt "add 41 and 1"
```

HTTP server:

```bash
export OPENAI_API_KEY=...
go run ./cmd/mcp-smoke-agent --server http://localhost:3000/mcp
```

One-shot prompt as trailing text:

```bash
export OPENAI_API_KEY=...
go run ./cmd/mcp-smoke-agent --server "go run ./cmd/mcp-test-server" "add 41 and 1"
```

Piped stdin:

```bash
export OPENAI_API_KEY=...
printf 'add 41 and 1\n' | go run ./cmd/mcp-smoke-agent --server "go run ./cmd/mcp-test-server"
```

`--server` accepts:

- An HTTP URL like `http://localhost:3000/mcp`
- A stdio command string like `go run ./cmd/mcp-test-server`
- A JSON array command like `["go","run","./cmd/mcp-test-server"]`

The default mode connects once, lists the MCP tools, and for each plain-English prompt refreshes the tool list and keeps doing the model/tool roundtrips until the model finishes the response or hits `--max-steps`.

By default the CLI reads:

- `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`
- `OPENAI_MODEL` or `ANTHROPIC_MODEL`

It will auto-load those values from `.env` in the current working directory. Real shell environment variables still take precedence, and you can point to a different file with `--env-file path/to/file.env`.

Default models are `gpt-4.1-mini` for OpenAI and `claude-3-5-haiku-latest` for Anthropic. Override them with `--model` if needed.

### GitHub E2E

If you set the repo secret `OPENAI_API_KEY`, CI can run a live e2e check for `mcp-smoke-agent` against the bundled `mcp-test-server`. The workflow writes a temporary `.env`, runs a plain-English request, and asserts both the MCP tool call and the final `42` response.

### GitHub Release Automation

Tags matching `v*` trigger a release workflow that builds archives for `mcp-smoke-agent`, uploads them to the GitHub release, and publishes `checksums.txt`. You can also run the release workflow manually for an existing tag.

### Smoke Usage

If you want the raw protocol checklist without an LLM, use the `smoke` subcommand.

Streamable HTTP:

```bash
mcp-smoke-agent smoke --transport=http --url http://localhost:3000/mcp
```

Stdio:

```bash
mcp-smoke-agent smoke --transport=stdio --command ./your-mcp-server -- <args>
```

The `smoke` subcommand is more flexible when tools or prompts require arguments:

- If you pass `--tool-args` or `--prompt-args`, it will scan the advertised entries and call the first one whose required top-level args are satisfied.
- You can force a specific entry with `--tool-name` or `--prompt-name`.
- If a named tool or prompt is missing required args, smoke fails instead of silently skipping it.

### Local smoke server

The bundled `mcp-test-server` exposes a few simple tools for quick smoke checks:

- `echo`
- `add`
- `ping`
- `hello-name`
- `hello`

## Docker smoke test

Builds a tiny MCP test server inside the image and runs the CLI against it.

```bash
docker build -t mcp-smoke .
docker run --rm mcp-smoke
```

## What it checks

- Initializes MCP and records protocol/capabilities
- Lists tools, prompts, and resources (if advertised)
- Optionally calls a matching tool/prompt/resource when required args are satisfied
