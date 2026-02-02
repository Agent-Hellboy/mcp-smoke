# mcp-smoke

Simple MCP server smoke-test CLI for CI.

## Install

```bash
go install github.com/Agent-Hellboy/mcp-smoke/cmd/mcp-smoke@latest
```

## Usage

### Streamable HTTP

```bash
mcp-smoke --transport=http --url http://localhost:3000/mcp
```

### Stdio

```bash
mcp-smoke --transport=stdio --command ./your-mcp-server -- <args>
```

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
