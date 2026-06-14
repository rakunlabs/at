package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
)

// mcpProtocolVersion is the latest MCP protocol revision this client
// requests during initialize. Servers negotiate down to the version they
// support; the negotiated value is echoed back via the
// MCP-Protocol-Version header on subsequent requests (2025-06-18 spec).
const mcpProtocolVersion = "2025-03-26"

// MCPClient is the interface implemented by both HTTP and stdio MCP clients.
type MCPClient interface {
	ListTools(ctx context.Context) ([]Tool, error)
	CallTool(ctx context.Context, name string, arguments map[string]any) (string, error)
	Close() error
}

// MCP Protocol Types
type MCPRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type MCPResponse struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type ListToolsResult struct {
	Tools []Tool `json:"tools"`
}

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Handler     string         `json:"handler,omitempty"`      // function body for skill/inline tools
	HandlerType string         `json:"handler_type,omitempty"` // "js" (default) or "bash"
}

type CallToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type CallToolResult struct {
	Content []ToolContent `json:"content"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// HTTPMCPClient handles communication with an MCP server over Streamable
// HTTP (single endpoint, JSON or SSE responses). It also tolerates plain
// JSON-mode servers (including older AT instances).
type HTTPMCPClient struct {
	baseURL         string
	endpointURL     string
	httpClient      *http.Client
	sessionID       string
	protocolVersion string // negotiated during initialize
	nextID          int32
	headers         map[string]string
}

func NewHTTPMCPClient(ctx context.Context, baseURL string, opts ...HTTPMCPClientOption) (*HTTPMCPClient, error) {
	client := &HTTPMCPClient{
		baseURL:     baseURL,
		endpointURL: normalizeMCPEndpointURL(baseURL),
		httpClient:  &http.Client{},
		nextID:      1,
	}

	for _, opt := range opts {
		opt(client)
	}

	if err := client.initialize(ctx); err != nil {
		return nil, err
	}

	return client, nil
}

// HTTPMCPClientOption configures an HTTPMCPClient.
type HTTPMCPClientOption func(*HTTPMCPClient)

// WithHeaders sets extra headers sent with every request.
func WithHeaders(headers map[string]string) HTTPMCPClientOption {
	return func(c *HTTPMCPClient) {
		c.headers = headers
	}
}

func (c *HTTPMCPClient) getNextID() int {
	return int(atomic.AddInt32(&c.nextID, 1) - 1)
}

// normalizeMCPEndpointURL accepts either a server base URL (http://host:8787)
// or a full Streamable HTTP MCP endpoint (http://host:8787/mcp?token=...).
// AT historically stored the base URL and appended /mcp; most MCP registry
// docs publish the full endpoint, so accepting both avoids easy /mcp/mcp
// misconfiguration.
func normalizeMCPEndpointURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(trimmed, "/") + "/mcp"
	}

	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		u.Path = "/mcp"
	} else if !strings.HasSuffix(path, "/mcp") {
		u.Path = path + "/mcp"
	} else {
		u.Path = path
	}
	return u.String()
}

func (c *HTTPMCPClient) sendRequest(ctx context.Context, req MCPRequest) (*MCPResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpointURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	// Streamable HTTP requires clients to accept both JSON and SSE responses.
	httpReq.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.headers {
		httpReq.Header.Set(k, v)
	}
	if c.sessionID != "" {
		// Spec header plus the legacy header for older AT servers.
		httpReq.Header.Set("Mcp-Session-Id", c.sessionID)
		httpReq.Header.Set("X-Session-ID", c.sessionID)
	}
	if c.protocolVersion != "" {
		httpReq.Header.Set("MCP-Protocol-Version", c.protocolVersion)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Save session ID if provided (spec header first, then legacy).
	if sessionID := resp.Header.Get("Mcp-Session-Id"); sessionID != "" {
		c.sessionID = sessionID
	} else if sessionID := resp.Header.Get("X-Session-ID"); sessionID != "" {
		c.sessionID = sessionID
	}

	// 202 Accepted (and bodyless 204) are valid for notifications.
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	mcpResp, err := decodeMCPResponseBody(resp, req.ID)
	if err != nil {
		return nil, err
	}
	if mcpResp == nil {
		// Empty 200 body — treat like an accepted notification.
		return nil, nil
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error [%d]: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	return mcpResp, nil
}

// decodeMCPResponseBody decodes a Streamable HTTP response body, which may
// be a plain JSON-RPC message or an SSE stream carrying one.
func decodeMCPResponseBody(resp *http.Response, wantID int) (*MCPResponse, error) {
	mediaType := resp.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(mediaType); err == nil {
		mediaType = mt
	}

	if mediaType == "text/event-stream" {
		return decodeMCPResponseSSE(resp.Body, wantID)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}

	var mcpResp MCPResponse
	if err := json.Unmarshal(body, &mcpResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &mcpResp, nil
}

// decodeMCPResponseSSE scans an SSE stream for the JSON-RPC response that
// answers request wantID. Server-initiated notifications and unrelated
// messages on the stream are skipped.
func decodeMCPResponseSSE(r io.Reader, wantID int) (*MCPResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var data strings.Builder
	flush := func() (*MCPResponse, bool) {
		if data.Len() == 0 {
			return nil, false
		}
		payload := data.String()
		data.Reset()

		var mcpResp MCPResponse
		if err := json.Unmarshal([]byte(payload), &mcpResp); err != nil {
			return nil, false
		}
		// A response carries result or error; match the request ID when set.
		if mcpResp.Result == nil && mcpResp.Error == nil {
			return nil, false
		}
		if mcpResp.ID != wantID {
			return nil, false
		}
		return &mcpResp, true
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			// Event boundary.
			if resp, ok := flush(); ok {
				return resp, nil
			}
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		default:
			// Ignore other SSE fields (event:, id:, retry:, comments).
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read SSE response: %w", err)
	}
	// Flush a trailing event without a final blank line.
	if resp, ok := flush(); ok {
		return resp, nil
	}

	return nil, fmt.Errorf("SSE stream ended without a response for request %d", wantID)
}

func (c *HTTPMCPClient) initialize(ctx context.Context) error {
	req := MCPRequest{
		Jsonrpc: "2.0",
		ID:      c.getNextID(),
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "at-mcp-client",
				"version": "1.0.0",
			},
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("initialization failed: empty response")
	}

	// Parse initialization response
	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}

	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		return fmt.Errorf("failed to parse init response: %w", err)
	}

	// Remember the negotiated protocol version; it is echoed on every
	// subsequent request via the MCP-Protocol-Version header.
	if initResult.ProtocolVersion != "" {
		c.protocolVersion = initResult.ProtocolVersion
	}

	slog.Info("MCP initialized", "server_name", initResult.ServerInfo.Name, "server_version", initResult.ServerInfo.Version, "protocol_version", c.protocolVersion)

	// Send initialized notification
	notifReq := MCPRequest{
		Jsonrpc: "2.0",
		Method:  "notifications/initialized",
	}
	c.sendRequest(ctx, notifReq)

	return nil
}

func (c *HTTPMCPClient) ListTools(ctx context.Context) ([]Tool, error) {
	req := MCPRequest{
		Jsonrpc: "2.0",
		ID:      c.getNextID(),
		Method:  "tools/list",
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("tools/list returned no response")
	}

	var result ListToolsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools: %w", err)
	}

	return result.Tools, nil
}

func (c *HTTPMCPClient) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
	if arguments == nil {
		arguments = map[string]any{}
	}
	req := MCPRequest{
		Jsonrpc: "2.0",
		ID:      c.getNextID(),
		Method:  "tools/call",
		Params: CallToolParams{
			Name:      name,
			Arguments: arguments,
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("tools/call returned no response")
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("failed to parse tool result: %w", err)
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}

	return "", nil
}

func (c *HTTPMCPClient) Close() error {
	// Optional: send shutdown notification
	req := MCPRequest{
		Jsonrpc: "2.0",
		Method:  "notifications/cancelled",
	}
	c.sendRequest(context.Background(), req)
	return nil
}
