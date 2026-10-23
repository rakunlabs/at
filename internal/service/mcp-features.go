package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// MCPResource is one entry of an MCP server's resources/list.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

// MCPResourceTemplate is one entry of resources/templates/list.
type MCPResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPResourceContents is one item of a resources/read result: text, or a
// base64 blob.
type MCPResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// MCPPrompt is one entry of prompts/list.
type MCPPrompt struct {
	Name        string              `json:"name"`
	Title       string              `json:"title,omitempty"`
	Description string              `json:"description,omitempty"`
	Arguments   []MCPPromptArgument `json:"arguments,omitempty"`
}

// MCPPromptArgument describes one prompt argument.
type MCPPromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPPromptMessage is one message of a prompts/get result.
type MCPPromptMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// MCPServerCapabilities is the part of the initialize result AT acts on.
type MCPServerCapabilities struct {
	Resources bool
	Prompts   bool
}

// MCPServerFeatures is implemented by MCP clients that expose the server's
// initialize instructions, resources and prompts in addition to tools.
// Callers must type-assert: not every client (or server) supports them.
type MCPServerFeatures interface {
	ServerName() string
	Instructions() string
	Capabilities() MCPServerCapabilities
	ListResources(ctx context.Context) ([]MCPResource, []MCPResourceTemplate, error)
	ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error)
	ListPrompts(ctx context.Context) ([]MCPPrompt, error)
	GetPrompt(ctx context.Context, name string, args map[string]string) (string, []MCPPromptMessage, error)
}

// mcpInitResult is the initialize result shared by the HTTP and stdio clients.
type mcpInitResult struct {
	ProtocolVersion string `json:"protocolVersion"`
	Instructions    string `json:"instructions"`
	Capabilities    struct {
		Resources json.RawMessage `json:"resources"`
		Prompts   json.RawMessage `json:"prompts"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

func (r mcpInitResult) capabilities() MCPServerCapabilities {
	present := func(raw json.RawMessage) bool {
		s := strings.TrimSpace(string(raw))
		return s != "" && s != "null" && s != "false"
	}
	return MCPServerCapabilities{Resources: present(r.Capabilities.Resources), Prompts: present(r.Capabilities.Prompts)}
}

// mcpMaxPages bounds cursor pagination of list calls.
const mcpMaxPages = 20

type mcpRequester func(ctx context.Context, method string, params any) (json.RawMessage, error)

func mcpListResources(ctx context.Context, call mcpRequester) ([]MCPResource, []MCPResourceTemplate, error) {
	var resources []MCPResource
	cursor := ""
	for page := 0; page < mcpMaxPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		raw, err := call(ctx, "resources/list", params)
		if err != nil {
			return nil, nil, err
		}
		var result struct {
			Resources  []MCPResource `json:"resources"`
			NextCursor string        `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, nil, fmt.Errorf("parse resources: %w", err)
		}
		resources = append(resources, result.Resources...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	var templates []MCPResourceTemplate
	if raw, err := call(ctx, "resources/templates/list", nil); err == nil {
		var result struct {
			ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates"`
		}
		if json.Unmarshal(raw, &result) == nil {
			templates = result.ResourceTemplates
		}
	}
	return resources, templates, nil
}

func mcpReadResource(ctx context.Context, call mcpRequester, uri string) ([]MCPResourceContents, error) {
	raw, err := call(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return nil, err
	}
	var result struct {
		Contents []MCPResourceContents `json:"contents"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("parse resource: %w", err)
	}
	return result.Contents, nil
}

func mcpListPrompts(ctx context.Context, call mcpRequester) ([]MCPPrompt, error) {
	var prompts []MCPPrompt
	cursor := ""
	for page := 0; page < mcpMaxPages; page++ {
		var params any
		if cursor != "" {
			params = map[string]any{"cursor": cursor}
		}
		raw, err := call(ctx, "prompts/list", params)
		if err != nil {
			return nil, err
		}
		var result struct {
			Prompts    []MCPPrompt `json:"prompts"`
			NextCursor string      `json:"nextCursor"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("parse prompts: %w", err)
		}
		prompts = append(prompts, result.Prompts...)
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return prompts, nil
}

func mcpGetPrompt(ctx context.Context, call mcpRequester, name string, args map[string]string) (string, []MCPPromptMessage, error) {
	params := map[string]any{"name": name}
	if len(args) > 0 {
		params["arguments"] = args
	}
	raw, err := call(ctx, "prompts/get", params)
	if err != nil {
		return "", nil, err
	}
	var result struct {
		Description string             `json:"description"`
		Messages    []MCPPromptMessage `json:"messages"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", nil, fmt.Errorf("parse prompt: %w", err)
	}
	return result.Description, result.Messages, nil
}

// MCPPromptText flattens prompt messages to text: text content verbatim,
// embedded resources by their text, other blocks by a short placeholder.
func MCPPromptText(messages []MCPPromptMessage) string {
	var parts []string
	for _, m := range messages {
		var block struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			MimeType string `json:"mimeType"`
			Resource struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"resource"`
		}
		if json.Unmarshal(m.Content, &block) != nil {
			continue
		}
		var text string
		switch block.Type {
		case "text":
			text = block.Text
		case "resource":
			text = block.Resource.Text
			if text == "" {
				text = "[resource " + block.Resource.URI + "]"
			}
		default:
			text = "[" + block.Type + " content omitted]"
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		if len(messages) > 1 {
			text = "[" + m.Role + "] " + text
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}

// HTTP client implementation.

func (c *HTTPMCPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	resp, err := c.sendRequest(ctx, MCPRequest{Jsonrpc: "2.0", ID: c.getNextID(), Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("%s returned no response", method)
	}
	return resp.Result, nil
}

func (c *HTTPMCPClient) ServerName() string                  { return c.serverName }
func (c *HTTPMCPClient) Instructions() string                { return c.instructions }
func (c *HTTPMCPClient) Capabilities() MCPServerCapabilities { return c.capabilities }
func (c *HTTPMCPClient) ListResources(ctx context.Context) ([]MCPResource, []MCPResourceTemplate, error) {
	return mcpListResources(ctx, c.request)
}
func (c *HTTPMCPClient) ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error) {
	return mcpReadResource(ctx, c.request, uri)
}
func (c *HTTPMCPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	return mcpListPrompts(ctx, c.request)
}
func (c *HTTPMCPClient) GetPrompt(ctx context.Context, name string, args map[string]string) (string, []MCPPromptMessage, error) {
	return mcpGetPrompt(ctx, c.request, name, args)
}

// Stdio client implementation.

func (c *StdioMCPClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	resp, err := c.sendRequest(ctx, MCPRequest{Jsonrpc: "2.0", ID: c.getNextID(), Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	return resp.Result, nil
}

func (c *StdioMCPClient) ServerName() string                  { return c.serverName }
func (c *StdioMCPClient) Instructions() string                { return c.instructions }
func (c *StdioMCPClient) Capabilities() MCPServerCapabilities { return c.capabilities }
func (c *StdioMCPClient) ListResources(ctx context.Context) ([]MCPResource, []MCPResourceTemplate, error) {
	return mcpListResources(ctx, c.request)
}
func (c *StdioMCPClient) ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error) {
	return mcpReadResource(ctx, c.request, uri)
}
func (c *StdioMCPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	return mcpListPrompts(ctx, c.request)
}
func (c *StdioMCPClient) GetPrompt(ctx context.Context, name string, args map[string]string) (string, []MCPPromptMessage, error) {
	return mcpGetPrompt(ctx, c.request, name, args)
}

// Execution-bound client: the same mcp.use admission as tool calls.

func (c *executionMCPClient) features() (MCPServerFeatures, bool) {
	f, ok := c.client.(MCPServerFeatures)
	return f, ok
}

func (c *executionMCPClient) ServerName() string {
	if f, ok := c.features(); ok {
		return f.ServerName()
	}
	return ""
}

func (c *executionMCPClient) Instructions() string {
	if f, ok := c.features(); ok {
		return f.Instructions()
	}
	return ""
}

func (c *executionMCPClient) Capabilities() MCPServerCapabilities {
	if f, ok := c.features(); ok {
		return f.Capabilities()
	}
	return MCPServerCapabilities{}
}

func (c *executionMCPClient) admit(ctx context.Context) (MCPServerFeatures, error) {
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: c.resourceID}); err != nil {
		return nil, err
	}
	f, ok := c.features()
	if !ok {
		return nil, ErrUnsupportedOperation
	}
	return f, nil
}

func (c *executionMCPClient) ListResources(ctx context.Context) ([]MCPResource, []MCPResourceTemplate, error) {
	f, err := c.admit(ctx)
	if err != nil {
		return nil, nil, err
	}
	return f.ListResources(ctx)
}

func (c *executionMCPClient) ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error) {
	f, err := c.admit(ctx)
	if err != nil {
		return nil, err
	}
	return f.ReadResource(ctx, uri)
}

func (c *executionMCPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	f, err := c.admit(ctx)
	if err != nil {
		return nil, err
	}
	return f.ListPrompts(ctx)
}

func (c *executionMCPClient) GetPrompt(ctx context.Context, name string, args map[string]string) (string, []MCPPromptMessage, error) {
	f, err := c.admit(ctx)
	if err != nil {
		return "", nil, err
	}
	return f.GetPrompt(ctx, name, args)
}
