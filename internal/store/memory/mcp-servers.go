package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListMCPServers(_ context.Context, q *query.Query) (*service.ListResult[service.MCPServer], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.MCPServer, 0, len(m.mcpServers))
	for _, s := range m.mcpServers {
		result = append(result, s)
	}

	slices.SortFunc(result, func(a, b service.MCPServer) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetMCPServer(_ context.Context, id string) (*service.MCPServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.mcpServers[id]
	if !ok {
		return nil, nil
	}

	return &s, nil
}

func (m *Memory) GetMCPServerByName(_ context.Context, name string) (*service.MCPServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.mcpServers {
		if s.Name == name {
			return &s, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateMCPServer(_ context.Context, srv service.MCPServer) (*service.MCPServer, error) {
	raw, err := json.Marshal(srv.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server config: %w", err)
	}
	var normalized service.MCPServerConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal mcp server config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.MCPServer{
		ID:          id,
		Name:        srv.Name,
		Description: srv.Description,
		Config:      normalized,
		Servers:     srv.Servers,
		URLs:        srv.URLs,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   srv.CreatedBy,
		UpdatedBy:   srv.UpdatedBy,
	}

	m.mu.Lock()
	m.mcpServers[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateMCPServer(_ context.Context, id string, srv service.MCPServer) (*service.MCPServer, error) {
	raw, err := json.Marshal(srv.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server config: %w", err)
	}
	var normalized service.MCPServerConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal mcp server config: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.mcpServers[id]
	if !ok {
		return nil, nil
	}

	existing.Name = srv.Name
	existing.Description = srv.Description
	existing.Config = normalized
	existing.Servers = srv.Servers
	existing.URLs = srv.URLs
	existing.UpdatedAt = now
	existing.UpdatedBy = srv.UpdatedBy
	m.mcpServers[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteMCPServer(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.mcpServers, id)
	m.mu.Unlock()

	return nil
}
