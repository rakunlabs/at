package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
)

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

// HTTPMCPClient handles communication with HTTP MCP server
type HTTPMCPClient struct {
	baseURL    string
	httpClient *http.Client
	sessionID  string
	nextID     int32
}

func NewHTTPMCPClient(ctx context.Context, baseURL string) (*HTTPMCPClient, error) {
	client := &HTTPMCPClient{
		baseURL:    baseURL,
		httpClient: &http.Client{},
		nextID:     1,
	}

	if err := client.initialize(ctx); err != nil {
		return nil, err
	}

	return client, nil
}

func (c *HTTPMCPClient) getNextID() int {
	return int(atomic.AddInt32(&c.nextID, 1) - 1)
}

func (c *HTTPMCPClient) sendRequest(ctx context.Context, req MCPRequest) (*MCPResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/mcp", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.sessionID != "" {
		httpReq.Header.Set("X-Session-ID", c.sessionID)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(body))
	}

	// Save session ID if provided
	if sessionID := resp.Header.Get("X-Session-ID"); sessionID != "" {
		c.sessionID = sessionID
	}

	var mcpResp MCPResponse
	if err := json.NewDecoder(resp.Body).Decode(&mcpResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if mcpResp.Error != nil {
		return nil, fmt.Errorf("MCP error [%d]: %s", mcpResp.Error.Code, mcpResp.Error.Message)
	}

	return &mcpResp, nil
}

func (c *HTTPMCPClient) initialize(ctx context.Context) error {
	req := MCPRequest{
		Jsonrpc: "2.0",
		ID:      c.getNextID(),
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "go-http-mcp-client",
				"version": "1.0.0",
			},
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
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

	slog.Info("MCP initialized", "server_name", initResult.ServerInfo.Name, "server_version", initResult.ServerInfo.Version)

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

	var result ListToolsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools: %w", err)
	}

	return result.Tools, nil
}

func (c *HTTPMCPClient) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
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
