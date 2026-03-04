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

func (m *Memory) ListRAGMCPServers(_ context.Context, q *query.Query) (*service.ListResult[service.RAGMCPServer], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.RAGMCPServer, 0, len(m.ragMCPServers))
	for _, s := range m.ragMCPServers {
		result = append(result, s)
	}

	slices.SortFunc(result, func(a, b service.RAGMCPServer) int {
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

func (m *Memory) GetRAGMCPServer(_ context.Context, id string) (*service.RAGMCPServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.ragMCPServers[id]
	if !ok {
		return nil, nil
	}

	return &s, nil
}

func (m *Memory) GetRAGMCPServerByName(_ context.Context, name string) (*service.RAGMCPServer, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.ragMCPServers {
		if s.Name == name {
			return &s, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateRAGMCPServer(_ context.Context, srv service.RAGMCPServer) (*service.RAGMCPServer, error) {
	// Round-trip through JSON to normalize.
	raw, err := json.Marshal(srv.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal rag mcp server config: %w", err)
	}
	var normalized service.RAGMCPServerConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal rag mcp server config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.RAGMCPServer{
		ID:        id,
		Name:      srv.Name,
		Config:    normalized,
		CreatedAt: now,
		UpdatedAt: now,
		CreatedBy: srv.CreatedBy,
		UpdatedBy: srv.UpdatedBy,
	}

	m.mu.Lock()
	m.ragMCPServers[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateRAGMCPServer(_ context.Context, id string, srv service.RAGMCPServer) (*service.RAGMCPServer, error) {
	raw, err := json.Marshal(srv.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal rag mcp server config: %w", err)
	}
	var normalized service.RAGMCPServerConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal rag mcp server config: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.ragMCPServers[id]
	if !ok {
		return nil, nil
	}

	existing.Name = srv.Name
	existing.Config = normalized
	existing.UpdatedAt = now
	existing.UpdatedBy = srv.UpdatedBy
	m.ragMCPServers[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteRAGMCPServer(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.ragMCPServers, id)
	m.mu.Unlock()

	return nil
}
