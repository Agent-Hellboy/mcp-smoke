FROM golang:1.24 AS build

WORKDIR /src
COPY . .

RUN go build -o /out/mcp-smoke-agent ./cmd/mcp-smoke-agent
RUN go build -o /out/mcp-test-server ./cmd/mcp-test-server

FROM debian:bookworm-slim

WORKDIR /app
COPY --from=build /out/mcp-smoke-agent /usr/local/bin/mcp-smoke-agent
COPY --from=build /out/mcp-test-server /usr/local/bin/mcp-test-server

ENTRYPOINT ["mcp-smoke-agent", "smoke", "--transport=stdio", "--command", "mcp-test-server"]
