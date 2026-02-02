package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id,omitempty"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type stepResult struct {
	Name       string          `json:"name"`
	OK         bool            `json:"ok"`
	Skipped    bool            `json:"skipped,omitempty"`
	DurationMs int64           `json:"duration_ms"`
	Error      string          `json:"error,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

type output struct {
	OK           bool                   `json:"ok"`
	Transport    string                 `json:"transport"`
	URL          string                 `json:"url,omitempty"`
	Command      string                 `json:"command,omitempty"`
	Args         []string               `json:"args,omitempty"`
	Protocol     string                 `json:"protocol_version,omitempty"`
	Capabilities map[string]interface{} `json:"capabilities,omitempty"`
	Steps        []stepResult           `json:"steps"`
	StartedAt    string                 `json:"started_at"`
	FinishedAt   string                 `json:"finished_at"`
	DurationMs   int64                  `json:"duration_ms"`
}

type client interface {
	Request(ctx context.Context, method string, params interface{}) (rpcResponse, error)
	Notify(ctx context.Context, method string, params interface{}) error
	Close() error
}

type stdioClient struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   *bytes.Buffer
	reqID    int64
	respMu   sync.Mutex
	waiters  map[int64]chan rpcResponse
	readDone chan error
}

func newStdioClient(command string, args []string) (*stdioClient, error) {
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
		waiters:  make(map[int64]chan rpcResponse),
		readDone: make(chan error, 1),
	}
	go c.readLoop()
	return c, nil
}

func (c *stdioClient) nextID() int64 {
	return atomic.AddInt64(&c.reqID, 1)
}

func (c *stdioClient) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
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
	c.readDone <- scanner.Err()
}

func (c *stdioClient) Request(ctx context.Context, method string, params interface{}) (rpcResponse, error) {
	id := c.nextID()
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return rpcResponse{}, err
	}
	ch := make(chan rpcResponse, 1)
	c.respMu.Lock()
	c.waiters[id] = ch
	c.respMu.Unlock()
	if _, err := c.stdin.Write(append(msg, '\n')); err != nil {
		return rpcResponse{}, err
	}
	select {
	case <-ctx.Done():
		return rpcResponse{}, ctx.Err()
	case resp := <-ch:
		return resp, nil
	}
}

func (c *stdioClient) Notify(ctx context.Context, method string, params interface{}) error {
	req := rpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	msg, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(msg, '\n'))
	return err
}

func (c *stdioClient) Close() error {
	_ = c.stdin.Close()
	err := c.cmd.Process.Kill()
	_ = c.cmd.Wait()
	return err
}

type httpClient struct {
	baseURL   string
	client    *http.Client
	reqID     int64
	sessionID string
}

func newHTTPClient(url string, timeout time.Duration) (*httpClient, error) {
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

func (c *httpClient) nextID() int64 {
	return atomic.AddInt64(&c.reqID, 1)
}

func (c *httpClient) Request(ctx context.Context, method string, params interface{}) (rpcResponse, error) {
	id := c.nextID()
	req := rpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return rpcResponse{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return rpcResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return rpcResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return rpcResponse{}, fmt.Errorf("http status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}
	if session := resp.Header.Get("Mcp-Session-Id"); session != "" {
		c.sessionID = session
	}
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return readSSEForID(resp.Body, id)
	}
	var rpcResp rpcResponse
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&rpcResp); err != nil {
		return rpcResponse{}, err
	}
	return rpcResp, nil
}

func (c *httpClient) Notify(ctx context.Context, method string, params interface{}) error {
	req := rpcRequest{
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

func readSSEForID(r io.Reader, id int64) (rpcResponse, error) {
	scanner := bufio.NewScanner(r)
	var dataBuf []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(dataBuf) == 0 {
				continue
			}
			payload := strings.Join(dataBuf, "\n")
			dataBuf = dataBuf[:0]
			var resp rpcResponse
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
		return rpcResponse{}, err
	}
	return rpcResponse{}, errors.New("no response received for request id")
}

func main() {
	var (
		transport  = flag.String("transport", "", "transport: stdio or http")
		url        = flag.String("url", "", "streamable HTTP endpoint url")
		command    = flag.String("command", "", "stdio command to run")
		timeout    = flag.Duration("timeout", 15*time.Second, "timeout per request")
		noCall     = flag.Bool("no-call", false, "skip tool/prompt/resource calls")
		protocol   = flag.String("protocol", "2025-06-18", "client protocol version")
		toolArgs   = flag.String("tool-args", "{}", "json object passed to tools/call (if required args are not present in schema)")
		promptArgs = flag.String("prompt-args", "{}", "json object passed to prompts/get")
		resourceUR = flag.String("resource-uri", "", "specific resource uri to read")
	)
	flag.Parse()

	started := time.Now().UTC()
	out := output{
		StartedAt: started.Format(time.RFC3339),
		Transport: *transport,
		URL:       *url,
		Command:   *command,
		Args:      flag.Args(),
	}

	var c client
	var err error
	switch {
	case *transport == "http" || (*transport == "" && *url != ""):
		out.Transport = "http"
		c, err = newHTTPClient(*url, *timeout)
	case *transport == "stdio" || (*transport == "" && *command != ""):
		out.Transport = "stdio"
		c, err = newStdioClient(*command, flag.Args())
	default:
		err = errors.New("specify --transport=http with --url or --transport=stdio with --command")
	}
	if err != nil {
		failAndPrint(out, err)
		return
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	initStep, caps, protocolVer := doInitialize(ctx, c, *protocol)
	out.Steps = append(out.Steps, initStep)
	out.Capabilities = caps
	if protocolVer != "" {
		out.Protocol = protocolVer
	}
	_ = c.Notify(ctx, "notifications/initialized", map[string]interface{}{})

	steps := make([]stepResult, 0, 6)
	steps = append(steps, listTools(ctx, c, caps))
	steps = append(steps, listPrompts(ctx, c, caps))
	steps = append(steps, listResources(ctx, c, caps))

	if !*noCall {
		if s := callFirstTool(ctx, c, caps, *toolArgs); s.Name != "" {
			steps = append(steps, s)
		}
		if s := getFirstPrompt(ctx, c, caps, *promptArgs); s.Name != "" {
			steps = append(steps, s)
		}
		if s := readFirstResource(ctx, c, caps, *resourceUR); s.Name != "" {
			steps = append(steps, s)
		}
	}
	out.Steps = append(out.Steps, steps...)

	out.OK = allStepsOK(out.Steps)
	finished := time.Now().UTC()
	out.FinishedAt = finished.Format(time.RFC3339)
	out.DurationMs = finished.Sub(started).Milliseconds()
	printJSON(out)
}

func failAndPrint(out output, err error) {
	out.OK = false
	out.Steps = append(out.Steps, stepResult{
		Name:       "startup",
		OK:         false,
		Error:      err.Error(),
		DurationMs: 0,
	})
	out.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	printJSON(out)
}

func printJSON(out output) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func allStepsOK(steps []stepResult) bool {
	for _, s := range steps {
		if !s.OK && !s.Skipped {
			return false
		}
	}
	return true
}

func doInitialize(ctx context.Context, c client, protocol string) (stepResult, map[string]interface{}, string) {
	start := time.Now()
	params := map[string]interface{}{
		"protocolVersion": protocol,
		"clientInfo": map[string]interface{}{
			"name":    "mcp-smoke",
			"version": "0.1.0",
		},
		"capabilities": map[string]interface{}{},
	}
	resp, err := c.Request(ctx, "initialize", params)
	if err != nil {
		return stepResult{Name: "initialize", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil, ""
	}
	if resp.Error != nil {
		return stepResult{Name: "initialize", OK: false, Error: resp.Error.Message, DurationMs: time.Since(start).Milliseconds()}, nil, ""
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return stepResult{Name: "initialize", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}, nil, ""
	}
	caps, _ := result["capabilities"].(map[string]interface{})
	protocolVer, _ := result["protocolVersion"].(string)
	detail, _ := json.Marshal(result)
	return stepResult{Name: "initialize", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}, caps, protocolVer
}

func listTools(ctx context.Context, c client, caps map[string]interface{}) stepResult {
	if caps == nil || caps["tools"] == nil {
		return stepResult{Name: "tools/list", OK: true, Skipped: true}
	}
	start := time.Now()
	all := make([]interface{}, 0)
	var cursor interface{} = nil
	for {
		params := map[string]interface{}{}
		if cursor != nil {
			params["cursor"] = cursor
		}
		resp, err := c.Request(ctx, "tools/list", params)
		if err != nil {
			return stepResult{Name: "tools/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if resp.Error != nil {
			return stepResult{Name: "tools/list", OK: false, Error: resp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return stepResult{Name: "tools/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if items, ok := result["tools"].([]interface{}); ok {
			all = append(all, items...)
		}
		if next, ok := result["nextCursor"]; ok && next != nil && next != "" {
			cursor = next
			continue
		}
		break
	}
	detail, _ := json.Marshal(map[string]interface{}{"count": len(all)})
	return stepResult{Name: "tools/list", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
}

func listPrompts(ctx context.Context, c client, caps map[string]interface{}) stepResult {
	if caps == nil || caps["prompts"] == nil {
		return stepResult{Name: "prompts/list", OK: true, Skipped: true}
	}
	start := time.Now()
	all := make([]interface{}, 0)
	var cursor interface{} = nil
	for {
		params := map[string]interface{}{}
		if cursor != nil {
			params["cursor"] = cursor
		}
		resp, err := c.Request(ctx, "prompts/list", params)
		if err != nil {
			return stepResult{Name: "prompts/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if resp.Error != nil {
			return stepResult{Name: "prompts/list", OK: false, Error: resp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return stepResult{Name: "prompts/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if items, ok := result["prompts"].([]interface{}); ok {
			all = append(all, items...)
		}
		if next, ok := result["nextCursor"]; ok && next != nil && next != "" {
			cursor = next
			continue
		}
		break
	}
	detail, _ := json.Marshal(map[string]interface{}{"count": len(all)})
	return stepResult{Name: "prompts/list", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
}

func listResources(ctx context.Context, c client, caps map[string]interface{}) stepResult {
	if caps == nil || caps["resources"] == nil {
		return stepResult{Name: "resources/list", OK: true, Skipped: true}
	}
	start := time.Now()
	all := make([]interface{}, 0)
	var cursor interface{} = nil
	for {
		params := map[string]interface{}{}
		if cursor != nil {
			params["cursor"] = cursor
		}
		resp, err := c.Request(ctx, "resources/list", params)
		if err != nil {
			return stepResult{Name: "resources/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if resp.Error != nil {
			return stepResult{Name: "resources/list", OK: false, Error: resp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return stepResult{Name: "resources/list", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if items, ok := result["resources"].([]interface{}); ok {
			all = append(all, items...)
		}
		if next, ok := result["nextCursor"]; ok && next != nil && next != "" {
			cursor = next
			continue
		}
		break
	}
	detail, _ := json.Marshal(map[string]interface{}{"count": len(all)})
	return stepResult{Name: "resources/list", OK: true, Detail: detail, DurationMs: time.Since(start).Milliseconds()}
}

func callFirstTool(ctx context.Context, c client, caps map[string]interface{}, rawArgs string) stepResult {
	if caps == nil || caps["tools"] == nil {
		return stepResult{Name: "tools/call", OK: true, Skipped: true}
	}
	start := time.Now()
	resp, err := c.Request(ctx, "tools/list", map[string]interface{}{})
	if err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if resp.Error != nil {
		return stepResult{Name: "tools/call", OK: false, Error: resp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	tools, _ := result["tools"].([]interface{})
	if len(tools) == 0 {
		return stepResult{Name: "tools/call", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}
	tool, _ := tools[0].(map[string]interface{})
	name, _ := tool["name"].(string)
	if name == "" {
		return stepResult{Name: "tools/call", OK: false, Error: "tool name missing", DurationMs: time.Since(start).Milliseconds()}
	}
	if schema, ok := tool["inputSchema"].(map[string]interface{}); ok {
		if req, ok := schema["required"].([]interface{}); ok && len(req) > 0 {
			return stepResult{Name: "tools/call", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
		}
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: "invalid --tool-args json", DurationMs: time.Since(start).Milliseconds()}
	}
	callParams := map[string]interface{}{
		"name":      name,
		"arguments": args,
	}
	callResp, err := c.Request(ctx, "tools/call", callParams)
	if err != nil {
		return stepResult{Name: "tools/call", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if callResp.Error != nil {
		return stepResult{Name: "tools/call", OK: false, Error: callResp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
	}
	return stepResult{Name: "tools/call", OK: true, DurationMs: time.Since(start).Milliseconds()}
}

func getFirstPrompt(ctx context.Context, c client, caps map[string]interface{}, rawArgs string) stepResult {
	if caps == nil || caps["prompts"] == nil {
		return stepResult{Name: "prompts/get", OK: true, Skipped: true}
	}
	start := time.Now()
	resp, err := c.Request(ctx, "prompts/list", map[string]interface{}{})
	if err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if resp.Error != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: resp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
	}
	var result map[string]interface{}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	prompts, _ := result["prompts"].([]interface{})
	if len(prompts) == 0 {
		return stepResult{Name: "prompts/get", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}
	prompt, _ := prompts[0].(map[string]interface{})
	name, _ := prompt["name"].(string)
	if name == "" {
		return stepResult{Name: "prompts/get", OK: false, Error: "prompt name missing", DurationMs: time.Since(start).Milliseconds()}
	}
	if args, ok := prompt["arguments"].([]interface{}); ok {
		for _, a := range args {
			arg, _ := a.(map[string]interface{})
			if required, ok := arg["required"].(bool); ok && required {
				return stepResult{Name: "prompts/get", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
			}
		}
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: "invalid --prompt-args json", DurationMs: time.Since(start).Milliseconds()}
	}
	callParams := map[string]interface{}{
		"name":      name,
		"arguments": args,
	}
	callResp, err := c.Request(ctx, "prompts/get", callParams)
	if err != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if callResp.Error != nil {
		return stepResult{Name: "prompts/get", OK: false, Error: callResp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
	}
	return stepResult{Name: "prompts/get", OK: true, DurationMs: time.Since(start).Milliseconds()}
}

func readFirstResource(ctx context.Context, c client, caps map[string]interface{}, resourceURI string) stepResult {
	if caps == nil || caps["resources"] == nil {
		return stepResult{Name: "resources/read", OK: true, Skipped: true}
	}
	start := time.Now()
	uri := resourceURI
	if uri == "" {
		resp, err := c.Request(ctx, "resources/list", map[string]interface{}{})
		if err != nil {
			return stepResult{Name: "resources/read", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		if resp.Error != nil {
			return stepResult{Name: "resources/read", OK: false, Error: resp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return stepResult{Name: "resources/read", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
		}
		resources, _ := result["resources"].([]interface{})
		if len(resources) == 0 {
			return stepResult{Name: "resources/read", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
		}
		res, _ := resources[0].(map[string]interface{})
		uri, _ = res["uri"].(string)
	}
	if uri == "" {
		return stepResult{Name: "resources/read", OK: true, Skipped: true, DurationMs: time.Since(start).Milliseconds()}
	}
	callParams := map[string]interface{}{
		"uri": uri,
	}
	callResp, err := c.Request(ctx, "resources/read", callParams)
	if err != nil {
		return stepResult{Name: "resources/read", OK: false, Error: err.Error(), DurationMs: time.Since(start).Milliseconds()}
	}
	if callResp.Error != nil {
		return stepResult{Name: "resources/read", OK: false, Error: callResp.Error.Message, DurationMs: time.Since(start).Milliseconds()}
	}
	return stepResult{Name: "resources/read", OK: true, DurationMs: time.Since(start).Milliseconds()}
}
