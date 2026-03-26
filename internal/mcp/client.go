package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type RPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type RPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type Client interface {
	Request(ctx context.Context, method string, params interface{}) (RPCResponse, error)
	Notify(ctx context.Context, method string, params interface{}) error
	Close() error
}

type InitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      map[string]interface{} `json:"serverInfo,omitempty"`
}

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

type Prompt struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Arguments   []PromptArgument `json:"arguments,omitempty"`
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

type stdioClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   *bytes.Buffer
	reqID    int64
	respMu   sync.Mutex
	waiters  map[int64]chan RPCResponse
	readDone chan error
}

type httpClient struct {
	baseURL   string
	client    *http.Client
	reqID     int64
	sessionID string
}

func NewClient(transport, url, command string, args []string, timeout time.Duration) (Client, error) {
	switch {
	case transport == "http" || (transport == "" && url != ""):
		return NewHTTPClient(url, timeout)
	case transport == "stdio" || (transport == "" && command != ""):
		return NewStdioClient(command, args)
	default:
		return nil, errors.New("specify --transport=http with --url or --transport=stdio with --command")
	}
}

func NewStdioClient(command string, args []string) (*stdioClient, error) {
	if command == "" {
		return nil, errors.New("missing command for stdio transport")
	}

	cmd := exec.Command(command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := &stdioClient{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		stderr:   &stderr,
		waiters:  make(map[int64]chan RPCResponse),
		readDone: make(chan error, 1),
	}
	go c.readLoop()
	return c, nil
}

func NewHTTPClient(url string, timeout time.Duration) (*httpClient, error) {
	if url == "" {
		return nil, errors.New("missing url for http transport")
	}

	return &httpClient{
		baseURL: strings.TrimRight(url, "/"),
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *stdioClient) nextID() int64 {
	return atomic.AddInt64(&c.reqID, 1)
}

func (c *stdioClient) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		payload, err := readRPCMessage(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				c.readDone <- nil
				return
			}
			c.readDone <- err
			return
		}
		if len(payload) == 0 {
			continue
		}

		var resp RPCResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue
		}

		c.respMu.Lock()
		ch := c.waiters[resp.ID]
		if ch != nil {
			delete(c.waiters, resp.ID)
		}
		c.respMu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *stdioClient) Request(ctx context.Context, method string, params interface{}) (RPCResponse, error) {
	id := c.nextID()
	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	msg, err := json.Marshal(req)
	if err != nil {
		return RPCResponse{}, err
	}

	ch := make(chan RPCResponse, 1)
	c.respMu.Lock()
	c.waiters[id] = ch
	c.respMu.Unlock()

	if err := writeRPCMessage(c.stdin, msg); err != nil {
		return RPCResponse{}, err
	}

	select {
	case <-ctx.Done():
		return RPCResponse{}, ctx.Err()
	case err := <-c.readDone:
		if err != nil {
			return RPCResponse{}, err
		}
		if stderr := strings.TrimSpace(c.stderr.String()); stderr != "" {
			return RPCResponse{}, fmt.Errorf("stdio closed: %s", stderr)
		}
		return RPCResponse{}, errors.New("stdio closed")
	case resp := <-ch:
		return resp, nil
	}
}

func (c *stdioClient) Notify(ctx context.Context, method string, params interface{}) error {
	req := RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return err
	}
	return writeRPCMessage(c.stdin, msg)
}

func (c *stdioClient) Close() error {
	_ = c.stdin.Close()
	if c.cmd.Process == nil {
		return nil
	}
	err := c.cmd.Process.Kill()
	_ = c.cmd.Wait()
	return err
}

func (c *httpClient) nextID() int64 {
	return atomic.AddInt64(&c.reqID, 1)
}

func (c *httpClient) Request(ctx context.Context, method string, params interface{}) (RPCResponse, error) {
	id := c.nextID()
	req := RPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	body, err := json.Marshal(req)
	if err != nil {
		return RPCResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return RPCResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return RPCResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return RPCResponse{}, fmt.Errorf("http status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		c.sessionID = session
	}

	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return readSSEForID(resp.Body, id)
	}

	var rpcResp RPCResponse
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&rpcResp); err != nil {
		return RPCResponse{}, err
	}
	return rpcResp, nil
}

func (c *httpClient) Notify(ctx context.Context, method string, params interface{}) error {
	req := RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		c.sessionID = session
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	return nil
}

func (c *httpClient) Close() error {
	return nil
}

func Initialize(ctx context.Context, c Client, protocol, clientName, clientVersion string) (InitializeResult, error) {
	params := map[string]interface{}{
		"protocolVersion": protocol,
		"clientInfo": map[string]interface{}{
			"name":    clientName,
			"version": clientVersion,
		},
		"capabilities": map[string]interface{}{},
	}

	resp, err := c.Request(ctx, "initialize", params)
	if err != nil {
		return InitializeResult{}, err
	}
	if resp.Error != nil {
		return InitializeResult{}, errors.New(resp.Error.Message)
	}

	var result InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return InitializeResult{}, err
	}
	return result, nil
}

func NotifyInitialized(ctx context.Context, c Client) error {
	return c.Notify(ctx, "notifications/initialized", map[string]interface{}{})
}

func ListTools(ctx context.Context, c Client) ([]Tool, error) {
	var tools []Tool
	var cursor interface{}

	for {
		params := map[string]interface{}{}
		if cursor != nil {
			params["cursor"] = cursor
		}

		resp, err := c.Request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Message)
		}

		var result struct {
			Tools      []Tool      `json:"tools"`
			NextCursor interface{} `json:"nextCursor"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, err
		}

		tools = append(tools, result.Tools...)
		if result.NextCursor == nil || result.NextCursor == "" {
			return tools, nil
		}
		cursor = result.NextCursor
	}
}

func ListPrompts(ctx context.Context, c Client) ([]Prompt, error) {
	var prompts []Prompt
	var cursor interface{}

	for {
		params := map[string]interface{}{}
		if cursor != nil {
			params["cursor"] = cursor
		}

		resp, err := c.Request(ctx, "prompts/list", params)
		if err != nil {
			return nil, err
		}
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Message)
		}

		var result struct {
			Prompts    []Prompt    `json:"prompts"`
			NextCursor interface{} `json:"nextCursor"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, err
		}

		prompts = append(prompts, result.Prompts...)
		if result.NextCursor == nil || result.NextCursor == "" {
			return prompts, nil
		}
		cursor = result.NextCursor
	}
}

func GetPrompt(ctx context.Context, c Client, name string, arguments map[string]interface{}) (json.RawMessage, error) {
	resp, err := c.Request(ctx, "prompts/get", map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}
	return resp.Result, nil
}

func ListResources(ctx context.Context, c Client) ([]Resource, error) {
	var resources []Resource
	var cursor interface{}

	for {
		params := map[string]interface{}{}
		if cursor != nil {
			params["cursor"] = cursor
		}

		resp, err := c.Request(ctx, "resources/list", params)
		if err != nil {
			return nil, err
		}
		if resp.Error != nil {
			return nil, errors.New(resp.Error.Message)
		}

		var result struct {
			Resources  []Resource  `json:"resources"`
			NextCursor interface{} `json:"nextCursor"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, err
		}

		resources = append(resources, result.Resources...)
		if result.NextCursor == nil || result.NextCursor == "" {
			return resources, nil
		}
		cursor = result.NextCursor
	}
}

func ReadResource(ctx context.Context, c Client, uri string) (json.RawMessage, error) {
	resp, err := c.Request(ctx, "resources/read", map[string]interface{}{"uri": uri})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}
	return resp.Result, nil
}

func CallTool(ctx context.Context, c Client, name string, arguments map[string]interface{}) (json.RawMessage, error) {
	if arguments == nil {
		arguments = map[string]interface{}{}
	}

	resp, err := c.Request(ctx, "tools/call", map[string]interface{}{
		"name":      name,
		"arguments": arguments,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != nil {
		return nil, errors.New(resp.Error.Message)
	}
	return resp.Result, nil
}

func TextFromResult(raw json.RawMessage) string {
	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return strings.TrimSpace(string(raw))
	}

	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		pretty, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return strings.TrimSpace(string(raw))
		}
		return string(pretty)
	}

	var parts []string
	for _, item := range content {
		block, _ := item.(map[string]interface{})
		if block == nil {
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "\n")
	}

	pretty, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return strings.TrimSpace(string(raw))
	}
	return string(pretty)
}

func HasCapability(capabilities map[string]interface{}, name string) bool {
	if capabilities == nil {
		return false
	}
	_, ok := capabilities[name]
	return ok
}

func readRPCMessage(reader *bufio.Reader) ([]byte, error) {
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && len(strings.TrimSpace(line)) > 0 {
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

func readSSEForID(r io.Reader, id int64) (RPCResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var dataBuf []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(dataBuf) == 0 {
				continue
			}

			payload := strings.Join(dataBuf, "\n")
			dataBuf = dataBuf[:0]

			var resp RPCResponse
			if err := json.Unmarshal([]byte(payload), &resp); err != nil {
				continue
			}
			if resp.ID == id {
				return resp, nil
			}
			continue
		}

		if strings.HasPrefix(line, "data:") {
			dataBuf = append(dataBuf, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}

	if err := scanner.Err(); err != nil {
		return RPCResponse{}, err
	}
	return RPCResponse{}, errors.New("no response received for request id")
}
