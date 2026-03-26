# mcp-smoke

![CI](https://github.com/Agent-Hellboy/mcp-smoke/actions/workflows/ci.yml/badge.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/Agent-Hellboy/mcp-smoke.svg)](https://pkg.go.dev/github.com/Agent-Hellboy/mcp-smoke)
[![Go Report Card](https://goreportcard.com/badge/github.com/Agent-Hellboy/mcp-smoke)](https://goreportcard.com/report/github.com/Agent-Hellboy/mcp-smoke)

Simple MCP server smoke-test CLI for CI, plus a tiny plain-English MCP agent.

## Install

```bash
go install github.com/Agent-Hellboy/mcp-smoke/cmd/mcp-smoke@latest
go install github.com/Agent-Hellboy/mcp-smoke/cmd/mcp-agent@latest
```

Install a tagged release:

```bash
go install github.com/Agent-Hellboy/mcp-smoke/cmd/mcp-smoke@v0.1.0
```

## Usage

### Plain-English MCP agent

The new `mcp-agent` binary connects to an MCP server, lists its tools, and lets an OpenAI or Anthropic model decide which MCP tool to call from plain-English input.

OpenAI with a single server flag:

```bash
cat > .env <<'EOF'
OPENAI_API_KEY=your-key-here
EOF

go run ./cmd/mcp-agent --server "go run ./cmd/mcp-test-server"
```

Anthropic with a single server flag:

```bash
export ANTHROPIC_API_KEY=...
go run ./cmd/mcp-agent --provider anthropic --server "go run ./cmd/mcp-test-server"
```

One-shot prompt:

```bash
export OPENAI_API_KEY=...
go run ./cmd/mcp-agent \
  --server "go run ./cmd/mcp-test-server" \
  --prompt "add 41 and 1"
```

HTTP server:

```bash
export OPENAI_API_KEY=...
go run ./cmd/mcp-agent --server http://localhost:3000/mcp
```

`--server` accepts:

- An HTTP URL like `http://localhost:3000/mcp`
- A stdio command string like `go run ./cmd/mcp-test-server`
- A JSON array command like `["go","run","./cmd/mcp-test-server"]`

The agent connects once, lists the MCP tools, and for each plain-English prompt refreshes the tool list and keeps doing the model/tool roundtrips until the model finishes the response or hits `--max-steps`.

By default the agent reads:

- `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`
- `OPENAI_MODEL` or `ANTHROPIC_MODEL`

It will auto-load those values from `.env` in the current working directory. Real shell environment variables still take precedence, and you can point to a different file with `--env-file path/to/file.env`.

Default models are `gpt-4.1-mini` for OpenAI and `claude-3-5-haiku-latest` for Anthropic. Override them with `--model` if needed.

### GitHub E2E

If you set the repo secret `OPENAI_API_KEY`, CI will run a live e2e check for `mcp-agent` against the bundled `mcp-test-server`. The workflow writes a temporary `.env`, runs the agent, and asserts both the MCP tool call and the final `42` response.

### Streamable HTTP

```bash
mcp-smoke --transport=http --url http://localhost:3000/mcp
```

### Stdio

```bash
mcp-smoke --transport=stdio --command ./your-mcp-server -- <args>
```

### Local smoke server

The bundled `mcp-test-server` now exposes a few simple tools for quick agent checks:

- `ping`
- `echo`
- `add`

## Docker smoke test

Builds a tiny MCP test server inside the image and runs the CLI against it.

```bash
docker build -t mcp-smoke .
docker run --rm mcp-smoke
```

## What it checks

- Initializes MCP and records protocol/capabilities
- Lists tools, prompts, and resources (if advertised)
- Optionally calls the first tool/prompt/resource when no required args are present
