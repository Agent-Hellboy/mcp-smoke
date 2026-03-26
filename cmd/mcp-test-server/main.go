package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
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

type toolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

type promptGetParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := readRPCMessage(reader)
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			return
		}
		if len(payload) == 0 {
			continue
		}

		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue
		}

		resp := handle(req)
		body, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		if err := writeRPCMessage(os.Stdout, body); err != nil {
			fmt.Fprintf(os.Stderr, "write error: %v\n", err)
			return
		}
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
					Name:        "echo",
					Description: "Echoes back the provided text.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"text": map[string]interface{}{
								"type":        "string",
								"description": "Text to echo back.",
							},
						},
						"required": []string{"text"},
					},
				},
				{
					Name:        "add",
					Description: "Adds two numbers together.",
					InputSchema: map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"a": map[string]interface{}{
								"type":        "number",
								"description": "First number.",
							},
							"b": map[string]interface{}{
								"type":        "number",
								"description": "Second number.",
							},
						},
						"required": []string{"a", "b"},
					},
				},
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
		resp.Result, resp.Error = handleToolCall(req.Params)
	case "prompts/list":
		resp.Result = map[string]interface{}{
			"prompts": []promptDef{
				{
					Name:        "hello-name",
					Description: "Returns a greeting for the provided name.",
					Arguments: []map[string]interface{}{
						{
							"name":        "name",
							"description": "Name to greet.",
							"required":    true,
						},
					},
				},
				{
					Name:        "hello",
					Description: "Returns a simple greeting.",
					Arguments:   []map[string]interface{}{},
				},
			},
		}
	case "prompts/get":
		resp.Result, resp.Error = handlePromptGet(req.Params)
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

func handleToolCall(raw json.RawMessage) (interface{}, *rpcError) {
	var params toolCallParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid tools/call params"}
	}
	if params.Arguments == nil {
		params.Arguments = map[string]interface{}{}
	}

	switch params.Name {
	case "ping":
		return textToolResult("pong"), nil
	case "echo":
		text, _ := params.Arguments["text"].(string)
		if strings.TrimSpace(text) == "" {
			return nil, &rpcError{Code: -32602, Message: "echo.text is required"}
		}
		return textToolResult(text), nil
	case "add":
		a, okA := asFloat(params.Arguments["a"])
		b, okB := asFloat(params.Arguments["b"])
		if !okA || !okB {
			return nil, &rpcError{Code: -32602, Message: "add.a and add.b must be numbers"}
		}
		return textToolResult(formatNumber(a + b)), nil
	default:
		return nil, &rpcError{Code: -32601, Message: fmt.Sprintf("tool not found: %s", params.Name)}
	}
}

func handlePromptGet(raw json.RawMessage) (interface{}, *rpcError) {
	var params promptGetParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid prompts/get params"}
	}
	if params.Arguments == nil {
		params.Arguments = map[string]interface{}{}
	}

	var text string
	switch params.Name {
	case "hello":
		text = "Hello from prompt"
	case "hello-name":
		name, _ := params.Arguments["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, &rpcError{Code: -32602, Message: "hello-name.name is required"}
		}
		text = "Hello, " + name
	default:
		return nil, &rpcError{Code: -32601, Message: fmt.Sprintf("prompt not found: %s", params.Name)}
	}

	return map[string]interface{}{
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": map[string]interface{}{
					"type": "text",
					"text": text,
				},
			},
		},
	}, nil
}

func textToolResult(text string) map[string]interface{} {
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		},
	}
}

func asFloat(value interface{}) (float64, bool) {
	switch n := value.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		v, err := n.Float64()
		return v, err == nil
	default:
		return 0, false
	}
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func readRPCMessage(reader *bufio.Reader) ([]byte, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(strings.TrimSpace(line)) > 0 {
				return []byte(strings.TrimSpace(line)), nil
			}
			return nil, err
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(strings.ToLower(trimmed), "content-length:") {
			length, err := parseContentLength(trimmed)
			if err != nil {
				return nil, err
			}
			for {
				header, err := reader.ReadString('\n')
				if err != nil {
					return nil, err
				}
				if strings.TrimSpace(header) == "" {
					break
				}
			}

			payload := make([]byte, length)
			if _, err := io.ReadFull(reader, payload); err != nil {
				return nil, err
			}
			return payload, nil
		}

		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			return []byte(trimmed), nil
		}
	}
}

func parseContentLength(line string) (int, error) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid content-length header: %s", line)
	}
	length, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || length < 0 {
		return 0, fmt.Errorf("invalid content-length header: %s", line)
	}
	return length, nil
}

func writeRPCMessage(w io.Writer, payload []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}
