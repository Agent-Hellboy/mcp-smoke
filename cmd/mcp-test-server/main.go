package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      *int64      `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type promptDef struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Arguments   []map[string]interface{} `json:"arguments"`
}

type resourceDef struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue
		}
		resp := handle(req)
		_ = enc.Encode(resp)
	}
}

func handle(req rpcRequest) rpcResponse {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	switch req.Method {
	case "initialize":
		resp.Result = map[string]interface{}{
			"protocolVersion": "2025-06-18",
			"serverInfo": map[string]interface{}{
				"name":    "mcp-test-server",
				"version": "0.1.0",
			},
			"capabilities": map[string]interface{}{
				"tools":     map[string]interface{}{},
				"prompts":   map[string]interface{}{},
				"resources": map[string]interface{}{},
			},
		}
	case "tools/list":
		resp.Result = map[string]interface{}{
			"tools": []toolDef{
				{
					Name:        "ping",
					Description: "Returns a simple pong response.",
					InputSchema: map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
			},
		}
	case "tools/call":
		resp.Result = map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": "pong",
				},
			},
		}
	case "prompts/list":
		resp.Result = map[string]interface{}{
			"prompts": []promptDef{
				{
					Name:        "hello",
					Description: "Returns a simple greeting.",
					Arguments:   []map[string]interface{}{},
				},
			},
		}
	case "prompts/get":
		resp.Result = map[string]interface{}{
			"messages": []map[string]interface{}{
				{
					"role": "user",
					"content": map[string]interface{}{
						"type": "text",
						"text": "Hello from prompt",
					},
				},
			},
		}
	case "resources/list":
		resp.Result = map[string]interface{}{
			"resources": []resourceDef{
				{
					URI:         "mcp://example/resource",
					Name:        "example-resource",
					Description: "A sample resource.",
					MimeType:    "text/plain",
				},
			},
		}
	case "resources/read":
		resp.Result = map[string]interface{}{
			"contents": []map[string]interface{}{
				{
					"uri":      "mcp://example/resource",
					"mimeType": "text/plain",
					"text":     "example resource content",
				},
			},
		}
	default:
		resp.Error = &rpcError{
			Code:    -32601,
			Message: fmt.Sprintf("method not found: %s", req.Method),
		}
	}
	return resp
}
