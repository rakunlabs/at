package service

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"
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

// StdioProcessStatus describes one managed stdio MCP subprocess.
type StdioProcessStatus struct {
	Command   string    `json:"command"`
	PID       int       `json:"pid"`
	Alive     bool      `json:"alive"`
	StartedAt time.Time `json:"started_at"`
	ExitError string    `json:"exit_error,omitempty"`
}

func statusOf(c *StdioMCPClient) StdioProcessStatus {
	st := StdioProcessStatus{
		Command:   c.command,
		PID:       c.PID(),
		Alive:     c.Alive(),
		StartedAt: c.StartedAt(),
	}
	if err := c.ExitError(); err != nil {
		st.ExitError = err.Error()
	}
	return st
}

// GetOrCreate returns a cached stdio client or starts a new subprocess.
// The returned MCPClient has a no-op Close(); the process lifecycle is
// managed by the StdioProcessManager.
//
// A cached process whose configured env no longer matches the requested env
// is replaced: the cache key is command+args only, so without this an env
// edit (or a rotated {{var:...}} secret) silently kept feeding the old
// process its stale environment until server restart.
func (m *StdioProcessManager) GetOrCreate(upstream MCPUpstream) (MCPClient, error) {
	key := stdioKey(upstream)

	m.mu.Lock()
	defer m.mu.Unlock()

	if c, ok := m.clients[key]; ok && c.Alive() {
		if maps.Equal(c.env, upstream.Env) {
			return &managedStdioClient{c}, nil
		}
		slog.Info("stdio MCP env changed; restarting subprocess", "command", upstream.Command)
		_ = c.Close()
		delete(m.clients, key)
	}

	c, err := NewStdioMCPClient(m.ctx, upstream.Command, upstream.Args, upstream.Env)
	if err != nil {
		return nil, fmt.Errorf("start stdio MCP %q: %w", upstream.Command, err)
	}

	m.clients[key] = c
	return &managedStdioClient{c}, nil
}

// StatusFor reports the status of the subprocess matching the upstream, or
// nil when none has been started.
func (m *StdioProcessManager) StatusFor(upstream MCPUpstream) *StdioProcessStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	c, ok := m.clients[stdioKey(upstream)]
	if !ok {
		return nil
	}
	st := statusOf(c)
	return &st
}

// Statuses reports every managed subprocess, running or exited. Args and env
// are deliberately not included: they can carry resolved {{var:...}} secrets.
func (m *StdioProcessManager) Statuses() []StdioProcessStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]StdioProcessStatus, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, statusOf(c))
	}
	return out
}

// Stop kills the subprocess matching the upstream and removes it from the
// cache. Returns true when a process existed. The next GetOrCreate spawns a
// fresh one.
func (m *StdioProcessManager) Stop(upstream MCPUpstream) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := stdioKey(upstream)
	c, ok := m.clients[key]
	if !ok {
		return false
	}
	_ = c.Close()
	delete(m.clients, key)
	return true
}

// Restart stops any process matching the upstream and starts a fresh one,
// surfacing spawn/handshake failures immediately instead of on the next tool
// call.
func (m *StdioProcessManager) Restart(upstream MCPUpstream) (*StdioProcessStatus, error) {
	m.Stop(upstream)
	if _, err := m.GetOrCreate(upstream); err != nil {
		return nil, err
	}
	return m.StatusFor(upstream), nil
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
