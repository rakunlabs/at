package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListPackSources(_ context.Context, q *query.Query) (*service.ListResult[service.PackSource], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.PackSource, 0, len(m.packSources))
	for _, ps := range m.packSources {
		result = append(result, ps)
	}

	slices.SortFunc(result, func(a, b service.PackSource) int {
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

func (m *Memory) GetPackSource(_ context.Context, id string) (*service.PackSource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ps, ok := m.packSources[id]
	if !ok {
		return nil, nil
	}

	return &ps, nil
}

func (m *Memory) CreatePackSource(_ context.Context, ps service.PackSource) (*service.PackSource, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.PackSource{
		ID:        id,
		Name:      ps.Name,
		URL:       ps.URL,
		Branch:    ps.Branch,
		Status:    ps.Status,
		LastSync:  ps.LastSync,
		Error:     ps.Error,
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.mu.Lock()
	m.packSources[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdatePackSource(_ context.Context, id string, ps service.PackSource) (*service.PackSource, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.packSources[id]
	if !ok {
		return nil, nil
	}

	existing.Name = ps.Name
	existing.URL = ps.URL
	existing.Branch = ps.Branch
	existing.Status = ps.Status
	existing.LastSync = ps.LastSync
	existing.Error = ps.Error
	existing.UpdatedAt = now
	m.packSources[id] = existing

	return &existing, nil
}

func (m *Memory) DeletePackSource(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.packSources, id)
	m.mu.Unlock()

	return nil
}
