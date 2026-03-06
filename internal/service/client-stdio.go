package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"sync/atomic"
)

// StdioMCPClient communicates with an MCP server over stdin/stdout of a subprocess.
type StdioMCPClient struct {
	cmd    *exec.Cmd
	stdin  interface{ Write([]byte) (int, error) }
	dec    *json.Decoder
	mu     sync.Mutex
	nextID int32
}

// NewStdioMCPClient starts a subprocess and performs the MCP initialize handshake.
func NewStdioMCPClient(ctx context.Context, command string, args []string, env map[string]string) (*StdioMCPClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)

	// Inherit parent env, then overlay custom env vars.
	if len(env) > 0 {
		cmd.Env = cmd.Environ()
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	// Discard stderr so the subprocess doesn't block.
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process %q: %w", command, err)
	}

	c := &StdioMCPClient{
		cmd:    cmd,
		stdin:  stdinPipe,
		dec:    json.NewDecoder(stdoutPipe),
		nextID: 1,
	}

	if err := c.initialize(ctx); err != nil {
		// Kill the process on init failure.
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}

	return c, nil
}

func (c *StdioMCPClient) getNextID() int {
	return int(atomic.AddInt32(&c.nextID, 1) - 1)
}

// sendRequest writes a JSON-RPC request to stdin and reads one response from stdout.
func (c *StdioMCPClient) sendRequest(ctx context.Context, req MCPRequest) (*MCPResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	// JSON-RPC over stdio uses newline-delimited JSON.
	data = append(data, '\n')
	if _, err := c.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("write to stdin: %w", err)
	}

	// Notifications don't expect a response.
	if req.ID == 0 {
		return &MCPResponse{}, nil
	}

	// Read responses, skipping any that don't match our request ID
	// (server notifications, log messages, or stale replies).
	for {
		var resp MCPResponse
		if err := c.dec.Decode(&resp); err != nil {
			return nil, fmt.Errorf("read from stdout: %w", err)
		}
		if resp.ID != req.ID {
			continue
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("MCP error [%d]: %s", resp.Error.Code, resp.Error.Message)
		}
		return &resp, nil
	}
}

func (c *StdioMCPClient) initialize(ctx context.Context) error {
	req := MCPRequest{
		Jsonrpc: "2.0",
		ID:      c.getNextID(),
		Method:  "initialize",
		Params: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]string{
				"name":    "go-stdio-mcp-client",
				"version": "1.0.0",
			},
		},
	}

	resp, err := c.sendRequest(ctx, req)
	if err != nil {
		return fmt.Errorf("initialization failed: %w", err)
	}

	var initResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &initResult); err != nil {
		return fmt.Errorf("parse init response: %w", err)
	}

	slog.Info("MCP stdio initialized", "server_name", initResult.ServerInfo.Name, "server_version", initResult.ServerInfo.Version)

	// Send initialized notification (no id field per JSON-RPC 2.0 spec).
	notif, _ := json.Marshal(struct {
		Jsonrpc string `json:"jsonrpc"`
		Method  string `json:"method"`
	}{"2.0", "notifications/initialized"})
	notif = append(notif, '\n')
	_, _ = c.stdin.Write(notif)

	return nil
}

func (c *StdioMCPClient) ListTools(ctx context.Context) ([]Tool, error) {
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
		return nil, fmt.Errorf("parse tools: %w", err)
	}

	return result.Tools, nil
}

func (c *StdioMCPClient) CallTool(ctx context.Context, name string, arguments map[string]any) (string, error) {
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

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("parse tool result: %w", err)
	}

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}

	return "", nil
}

func (c *StdioMCPClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}

// Alive returns true if the subprocess is still running.
func (c *StdioMCPClient) Alive() bool {
	return c.cmd.ProcessState == nil // not yet exited
}
