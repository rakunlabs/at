package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListMCPSets(_ context.Context, q *query.Query) (*service.ListResult[service.MCPSet], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.MCPSet, 0, len(m.mcpSets))
	for _, s := range m.mcpSets {
		result = append(result, s)
	}

	slices.SortFunc(result, func(a, b service.MCPSet) int {
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

func (m *Memory) GetMCPSet(_ context.Context, id string) (*service.MCPSet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s, ok := m.mcpSets[id]
	if !ok {
		return nil, nil
	}

	return &s, nil
}

func (m *Memory) GetMCPSetByName(_ context.Context, name string) (*service.MCPSet, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, s := range m.mcpSets {
		if s.Name == name {
			return &s, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateMCPSet(_ context.Context, set service.MCPSet) (*service.MCPSet, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.MCPSet{
		ID:          id,
		Name:        set.Name,
		Description: set.Description,
		Category:    set.Category,
		Tags:        set.Tags,
		Config:      set.Config,
		Servers:     set.Servers,
		URLs:        set.URLs,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   set.CreatedBy,
		UpdatedBy:   set.UpdatedBy,
	}

	m.mu.Lock()
	m.mcpSets[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateMCPSet(_ context.Context, id string, set service.MCPSet) (*service.MCPSet, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.mcpSets[id]
	if !ok {
		return nil, nil
	}

	existing.Name = set.Name
	existing.Description = set.Description
	existing.Category = set.Category
	existing.Tags = set.Tags
	existing.Config = set.Config
	existing.Servers = set.Servers
	existing.URLs = set.URLs
	existing.UpdatedAt = now
	existing.UpdatedBy = set.UpdatedBy
	m.mcpSets[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteMCPSet(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.mcpSets, id)
	m.mu.Unlock()

	return nil
}
