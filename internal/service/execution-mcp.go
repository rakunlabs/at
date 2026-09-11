package service

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// NewExecutionHTTPMCPClient binds external tools to their configured endpoint.
// Internal gateway calls must use in-process dispatch with the original context
// or an explicitly scoped machine credential, never anonymous HTTP loopback.
func NewExecutionHTTPMCPClient(ctx context.Context, endpoint string, opts ...HTTPMCPClientOption) (MCPClient, error) {
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: endpoint}); err != nil {
		return nil, err
	}
	if err := CheckExecution(ctx, ExecutionAction{Kind: "handler", Name: "javascript"}); err != nil {
		return nil, err
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse MCP endpoint: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, ErrExecutionDenied
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		return nil, fmt.Errorf("internal MCP requires scoped in-process dispatch: %w", ErrExecutionDenied)
	}
	client, err := NewHTTPMCPClient(ctx, endpoint, opts...)
	if err != nil {
		return nil, err
	}
	return &executionMCPClient{client: client, resourceID: endpoint}, nil
}

type executionMCPClient struct {
	client     MCPClient
	resourceID string
}

func (c *executionMCPClient) ListTools(ctx context.Context) ([]Tool, error) {
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: c.resourceID}); err != nil {
		return nil, err
	}
	tools, err := c.client.ListTools(ctx)
	if err != nil {
		return nil, err
	}
	allowed := make([]Tool, 0, len(tools))
	for _, tool := range tools {
		if CheckExecution(ctx, ExecutionAction{Kind: "mcp_tool", Name: tool.Name, ResourceID: c.resourceID}) == nil {
			allowed = append(allowed, tool)
		}
	}
	return allowed, nil
}
func (c *executionMCPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: c.resourceID}); err != nil {
		return "", err
	}
	if err := CheckExecution(ctx, ExecutionAction{Kind: "mcp_tool", Name: name, ResourceID: c.resourceID}); err != nil {
		return "", err
	}
	return c.client.CallTool(ctx, name, args)
}
func (c *executionMCPClient) Close() error { return c.client.Close() }
