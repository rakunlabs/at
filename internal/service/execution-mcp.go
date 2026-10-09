package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Trusted local MCP endpoints come from installation settings
// (Settings → Execution, AgentRuntimeSettings.TrustedLocalMCP). The server
// installs a loader at startup; the list is cached briefly so a save on any
// replica applies within trustedLocalMCPTTL without a database read per dial.
const trustedLocalMCPTTL = 10 * time.Second

var (
	trustedLocalMCPMu      sync.Mutex
	trustedLocalMCPLoader  func(context.Context) ([]string, error)
	trustedLocalMCPCache   map[string]bool
	trustedLocalMCPExpires time.Time
)

// SetTrustedLocalMCPLoader installs the source of the trusted list.
func SetTrustedLocalMCPLoader(load func(context.Context) ([]string, error)) {
	trustedLocalMCPMu.Lock()
	defer trustedLocalMCPMu.Unlock()
	trustedLocalMCPLoader = load
	trustedLocalMCPCache, trustedLocalMCPExpires = nil, time.Time{}
}

// InvalidateTrustedLocalMCP makes the next dial re-read the list (call after
// saving settings on this replica).
func InvalidateTrustedLocalMCP() {
	trustedLocalMCPMu.Lock()
	trustedLocalMCPExpires = time.Time{}
	trustedLocalMCPMu.Unlock()
}

func localMCPKey(u string) string {
	return strings.TrimSuffix(strings.TrimSpace(u), "/")
}

// Entries are compared after trimming whitespace and one trailing slash;
// a different port, path or host spelling is a different (refused) endpoint.
// A load failure keeps the last known list (or none), so it can only refuse.
func isTrustedLocalMCP(ctx context.Context, endpoint string) bool {
	trustedLocalMCPMu.Lock()
	defer trustedLocalMCPMu.Unlock()
	if trustedLocalMCPLoader != nil && time.Now().After(trustedLocalMCPExpires) {
		if urls, err := trustedLocalMCPLoader(context.WithoutCancel(ctx)); err == nil {
			allowed := make(map[string]bool, len(urls))
			for _, u := range urls {
				if key := localMCPKey(u); key != "" {
					allowed[key] = true
				}
			}
			trustedLocalMCPCache = allowed
		}
		trustedLocalMCPExpires = time.Now().Add(trustedLocalMCPTTL)
	}
	return trustedLocalMCPCache[localMCPKey(endpoint)]
}

// NewExecutionHTTPMCPClient binds external tools to their configured endpoint.
// Internal gateway calls must use in-process dispatch with the original context
// or an explicitly scoped machine credential, never anonymous HTTP loopback.
func NewExecutionHTTPMCPClient(ctx context.Context, endpoint string, opts ...HTTPMCPClientOption) (MCPClient, error) {
	if err := CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: endpoint}); err != nil {
		return nil, fmt.Errorf("MCP upstream access denied; check access to the MCP set in this workspace: %w", err)
	}
	if err := CheckExecution(ctx, ExecutionAction{Kind: "handler", Name: "javascript"}); err != nil {
		if errors.Is(err, ErrExecutionDenied) {
			return nil, fmt.Errorf("MCP upstream execution is not permitted; an installation administrator must enable Trusted host mode and host execution permission under Settings → Execution for this workspace (connecting an OAuth account does not grant execution permission): %w", err)
		}
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
	if (strings.EqualFold(host, "localhost") || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())) && !isTrustedLocalMCP(ctx, endpoint) {
		return nil, fmt.Errorf("internal MCP requires scoped in-process dispatch (or add this exact URL under Settings → Execution → Trusted local MCP endpoints): %w", ErrExecutionDenied)
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
		return "", fmt.Errorf("MCP tool %q execution is not permitted; check its MCP tool permission under Settings → Execution: %w", name, err)
	}
	return c.client.CallTool(ctx, name, args)
}
func (c *executionMCPClient) Close() error { return c.client.Close() }
