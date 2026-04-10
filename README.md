# mcp-smoke

![CI](https://github.com/Agent-Hellboy/mcp-smoke/actions/workflows/ci.yml/badge.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/Agent-Hellboy/mcp-smoke.svg)](https://pkg.go.dev/github.com/Agent-Hellboy/mcp-smoke)
[![Go Report Card](https://goreportcard.com/badge/github.com/Agent-Hellboy/mcp-smoke)](https://goreportcard.com/report/github.com/Agent-Hellboy/mcp-smoke)

`mcp-smoke-agent` is a small CLI for smoke-testing MCP servers through real plain-English requests.

## Why use it in e2e harnesses

If you are building MCP server infrastructure, this gives you one realistic client for end-to-end tests.

Instead of hand-writing raw MCP requests for every case, you can send a plain-English request through the CLI, let it invoke the real MCP tool path, and verify the surrounding infrastructure behavior. That is useful for checking routing, auth, logging, tracing, and whether analytics are generated correctly for real tool-invocation flows.

## Install

```bash
go install github.com/Agent-Hellboy/mcp-smoke/cmd/mcp-smoke-agent@latest
```

## Quick Start

```bash
cat > .env <<'EOF'
OPENAI_API_KEY=your-key-here
EOF

go run ./cmd/mcp-smoke-agent \
  --server "go run ./cmd/mcp-test-server" \
  "add 41 and 1"
```

## Usage

Default mode:

```bash
mcp-smoke-agent [flags] [plain English request]
```

This mode connects to an MCP server, lists its tools, and lets an OpenAI or Anthropic model decide which MCP tool to call from the request.

Useful patterns:

- Pass the request as trailing text.
- Use `--prompt "..."` for a one-shot call.
- Pipe stdin for one-shot usage.
- Omit the prompt to use interactive mode.

`--server` accepts:

- An HTTP URL like `http://localhost:3000/mcp`
- A stdio command string like `go run ./cmd/mcp-test-server`
- A JSON array command like `["go","run","./cmd/mcp-test-server"]`

The CLI reads provider config from the shell environment or `.env`:

- `OPENAI_API_KEY` or `ANTHROPIC_API_KEY`
- `OPENAI_MODEL` or `ANTHROPIC_MODEL`

Default models are `gpt-4.1-mini` for OpenAI and `claude-3-5-haiku-latest` for Anthropic.

## Low-Level Smoke Mode

If you want raw MCP protocol checks without an LLM, use:

```bash
mcp-smoke-agent smoke [flags]
```

Examples:

```bash
mcp-smoke-agent smoke --transport=http --url http://localhost:3000/mcp
mcp-smoke-agent smoke --transport=stdio --command ./your-mcp-server -- <args>
```

This mode initializes MCP, lists capabilities, and can optionally call tools, prompts, or resources directly.
