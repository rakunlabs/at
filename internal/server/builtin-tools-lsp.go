package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── LSP Tool Executor ───
//
// Provides code intelligence by communicating with an LSP server process.
// Supports: goToDefinition, findReferences, hover, documentSymbol,
//           workspaceSymbol, goToImplementation.

// lspClient manages a single LSP server process.
type lspClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	mu     sync.Mutex
	nextID atomic.Int64
}

// lspManager manages per-language LSP server processes.
type lspManager struct {
	mu      sync.Mutex
	clients map[string]*lspClient // key: language server command
}

func newLSPManager() *lspManager {
	return &lspManager{
		clients: make(map[string]*lspClient),
	}
}

// getOrStart returns an existing LSP client or starts a new one.
func (m *lspManager) getOrStart(ctx context.Context, command string, args []string) (*lspClient, error) {
	key := command + " " + strings.Join(args, " ")

	m.mu.Lock()
	defer m.mu.Unlock()

	if client, ok := m.clients[key]; ok {
		// Check if process is still running.
		if client.cmd.ProcessState == nil {
			return client, nil
		}
		// Process exited, remove and restart.
		delete(m.clients, key)
	}

	cmd := exec.CommandContext(ctx, command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start LSP server %q: %w", command, err)
	}

	client := &lspClient{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}

	// Initialize the LSP server.
	if err := client.initialize(ctx); err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("failed to initialize LSP server: %w", err)
	}

	m.clients[key] = client
	return client, nil
}

// close shuts down all LSP server processes.
func (m *lspManager) close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, client := range m.clients {
		_ = client.stdin.Close()
		_ = client.cmd.Process.Kill()
		delete(m.clients, key)
	}
}

// initialize sends the LSP initialize request.
func (c *lspClient) initialize(ctx context.Context) error {
	id := c.nextID.Add(1)

	initReq := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "initialize",
		"params": map[string]any{
			"processId": nil,
			"capabilities": map[string]any{
				"textDocument": map[string]any{
					"hover": map[string]any{
						"contentFormat": []string{"plaintext", "markdown"},
					},
				},
			},
			"rootUri": nil,
		},
	}

	if _, err := c.sendRequest(ctx, initReq); err != nil {
		return err
	}

	// Send initialized notification.
	notif := map[string]any{
		"jsonrpc": "2.0",
		"method":  "initialized",
		"params":  map[string]any{},
	}

	return c.sendNotification(notif)
}

// sendRequest sends a JSON-RPC request and waits for the response.
func (c *lspClient) sendRequest(ctx context.Context, req map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	// Write as Content-Length header + body (LSP base protocol).
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return nil, fmt.Errorf("failed to write header: %w", err)
	}
	if _, err := c.stdin.Write(data); err != nil {
		return nil, fmt.Errorf("failed to write body: %w", err)
	}

	// Read response with timeout.
	type result struct {
		data json.RawMessage
		err  error
	}
	ch := make(chan result, 1)

	go func() {
		resp, err := c.readResponse()
		ch <- result{data: resp, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return nil, fmt.Errorf("LSP response timeout")
	case r := <-ch:
		return r.data, r.err
	}
}

// sendNotification sends a JSON-RPC notification (no response expected).
func (c *lspClient) sendNotification(notif map[string]any) error {
	data, err := json.Marshal(notif)
	if err != nil {
		return err
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	if _, err := c.stdin.Write(data); err != nil {
		return err
	}
	return nil
}

// readResponse reads a single LSP JSON-RPC response.
func (c *lspClient) readResponse() (json.RawMessage, error) {
	// Read headers.
	contentLength := 0
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("failed to read response header: %w", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			_, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength)
			if err != nil {
				return nil, fmt.Errorf("invalid Content-Length: %w", err)
			}
		}
	}

	if contentLength == 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}

	// Read body.
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(c.reader, body); err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse to extract result.
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if resp.Error != nil {
		return nil, fmt.Errorf("LSP error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	return resp.Result, nil
}

// execLSPQuery handles LSP operations.
// Parameters: operation (string, required), file_path (string, required),
//
//	line (int, optional), character (int, optional),
//	query (string, optional — for workspaceSymbol),
//	language (string, optional — to pick LSP server)
func (s *Server) execLSPQuery(ctx context.Context, args map[string]any) (string, error) {
	operation, _ := args["operation"].(string)
	if operation == "" {
		return "", fmt.Errorf("operation is required")
	}

	validOps := map[string]bool{
		"goToDefinition":     true,
		"findReferences":     true,
		"hover":              true,
		"documentSymbol":     true,
		"workspaceSymbol":    true,
		"goToImplementation": true,
	}

	if !validOps[operation] {
		return "", fmt.Errorf("invalid operation %q. Valid operations: goToDefinition, findReferences, hover, documentSymbol, workspaceSymbol, goToImplementation", operation)
	}

	filePath, _ := args["file_path"].(string)
	if filePath == "" && operation != "workspaceSymbol" {
		return "", fmt.Errorf("file_path is required for operation %q", operation)
	}

	line := 0
	if l, ok := args["line"].(float64); ok {
		line = int(l)
	}

	character := 0
	if c, ok := args["character"].(float64); ok {
		character = int(c)
	}

	// Determine LSP server command based on language.
	language, _ := args["language"].(string)
	if language == "" {
		// Infer from file extension.
		language = inferLanguage(filePath)
	}

	command, cmdArgs := lspServerCommand(language)
	if command == "" {
		return "", fmt.Errorf("no LSP server configured for language %q. Supported: go, typescript, javascript, python, rust", language)
	}

	if s.lspManager == nil {
		return "", fmt.Errorf("LSP manager not initialized")
	}

	client, err := s.lspManager.getOrStart(ctx, command, cmdArgs)
	if err != nil {
		return "", fmt.Errorf("failed to start LSP server: %w", err)
	}

	// Build the LSP request based on operation.
	id := client.nextID.Add(1)
	var method string
	var params map[string]any

	textDocPos := map[string]any{
		"textDocument": map[string]any{
			"uri": "file://" + filePath,
		},
		"position": map[string]any{
			"line":      line,
			"character": character,
		},
	}

	switch operation {
	case "goToDefinition":
		method = "textDocument/definition"
		params = textDocPos
	case "findReferences":
		method = "textDocument/references"
		params = textDocPos
		params["context"] = map[string]any{"includeDeclaration": true}
	case "hover":
		method = "textDocument/hover"
		params = textDocPos
	case "documentSymbol":
		method = "textDocument/documentSymbol"
		params = map[string]any{
			"textDocument": map[string]any{
				"uri": "file://" + filePath,
			},
		}
	case "workspaceSymbol":
		method = "workspace/symbol"
		query, _ := args["query"].(string)
		params = map[string]any{"query": query}
	case "goToImplementation":
		method = "textDocument/implementation"
		params = textDocPos
	}

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	result, err := client.sendRequest(ctx, req)
	if err != nil {
		return "", fmt.Errorf("LSP request failed: %w", err)
	}

	if result == nil {
		return "No results found.", nil
	}

	// Format the result as readable JSON.
	var formatted any
	if err := json.Unmarshal(result, &formatted); err != nil {
		return string(result), nil
	}

	prettyJSON, err := json.MarshalIndent(formatted, "", "  ")
	if err != nil {
		return string(result), nil
	}

	return string(prettyJSON), nil
}

// inferLanguage guesses the language from a file path.
func inferLanguage(filePath string) string {
	if filePath == "" {
		return "unknown"
	}

	lower := strings.ToLower(filePath)

	switch {
	case strings.HasSuffix(lower, ".go"):
		return "go"
	case strings.HasSuffix(lower, ".ts"), strings.HasSuffix(lower, ".tsx"):
		return "typescript"
	case strings.HasSuffix(lower, ".js"), strings.HasSuffix(lower, ".jsx"):
		return "javascript"
	case strings.HasSuffix(lower, ".py"):
		return "python"
	case strings.HasSuffix(lower, ".rs"):
		return "rust"
	case strings.HasSuffix(lower, ".java"):
		return "java"
	case strings.HasSuffix(lower, ".c"), strings.HasSuffix(lower, ".h"):
		return "c"
	case strings.HasSuffix(lower, ".cpp"), strings.HasSuffix(lower, ".hpp"), strings.HasSuffix(lower, ".cc"):
		return "cpp"
	default:
		return "unknown"
	}
}

// lspServerCommand returns the command and args for a language's LSP server.
func lspServerCommand(language string) (string, []string) {
	switch language {
	case "go":
		return "gopls", []string{"serve"}
	case "typescript", "javascript":
		return "typescript-language-server", []string{"--stdio"}
	case "python":
		return "pyright-langserver", []string{"--stdio"}
	case "rust":
		return "rust-analyzer", nil
	case "java":
		return "jdtls", nil
	case "c", "cpp":
		return "clangd", nil
	default:
		return "", nil
	}
}
