FROM golang:1.24 AS build

WORKDIR /src
COPY . .

RUN go build -o /out/mcp-smoke ./cmd/mcp-smoke
RUN go build -o /out/mcp-test-server ./cmd/mcp-test-server

FROM debian:bookworm-slim

WORKDIR /app
COPY --from=build /out/mcp-smoke /usr/local/bin/mcp-smoke
COPY --from=build /out/mcp-test-server /usr/local/bin/mcp-test-server

ENTRYPOINT ["mcp-smoke", "--transport=stdio", "--command", "mcp-test-server"]
