package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// StdioProcessManager manages stdio MCP subprocess lifecycles.
type StdioProcessManager struct {
	ctx     context.Context
	mu      sync.Mutex
	clients map[string]*StdioMCPClient
}

// NewStdioProcessManager creates a new manager. The provided context is used
// as the parent for all subprocess contexts.
func NewStdioProcessManager(ctx context.Context) *StdioProcessManager {
	return &StdioProcessManager{
		ctx:     ctx,
		clients: make(map[string]*StdioMCPClient),
	}
}

// stdioKey returns a cache key for a given upstream config.
func stdioKey(upstream MCPUpstream) string {
	return upstream.Command + "|" + strings.Join(upstream.Args, "|")
}

// managedStdioClient wraps a StdioMCPClient so that Close() is a no-op.
// The underlying process is managed by StdioProcessManager and should not
// be killed when individual callers are done.
type managedStdioClient struct {
	*StdioMCPClient
}

func (m *managedStdioClient) Close() error { return nil }

// GetOrCreate returns a cached stdio client or starts a new subprocess.
// The returned MCPClient has a no-op Close(); the process lifecycle is
// managed by the StdioProcessManager.
func (m *StdioProcessManager) GetOrCreate(upstream MCPUpstream) (MCPClient, error) {
	key := stdioKey(upstream)

	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.clients[key]; ok && c.Alive() {
		return &managedStdioClient{c}, nil
	}

	c, err := NewStdioMCPClient(m.ctx, upstream.Command, upstream.Args, upstream.Env)
	if err != nil {
		return nil, fmt.Errorf("start stdio MCP %q: %w", upstream.Command, err)
	}

	m.clients[key] = c
	return &managedStdioClient{c}, nil
}

// Close kills all managed subprocesses.
func (m *StdioProcessManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for key, c := range m.clients {
		_ = c.Close()
		delete(m.clients, key)
	}
}
