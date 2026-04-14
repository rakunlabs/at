package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Guide CRUD ───

func (m *Memory) ListGuides(_ context.Context, q *query.Query) (*service.ListResult[service.Guide], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.Guide, 0, len(m.guides))
	for _, g := range m.guides {
		result = append(result, g)
	}

	slices.SortFunc(result, func(a, b service.Guide) int {
		if a.Title < b.Title {
			return -1
		}
		if a.Title > b.Title {
			return 1
		}
		return 0
	})

	return paginate(result, q), nil
}

func (m *Memory) GetGuide(_ context.Context, id string) (*service.Guide, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	g, ok := m.guides[id]
	if !ok {
		return nil, nil
	}

	return &g, nil
}

func (m *Memory) CreateGuide(_ context.Context, g service.Guide) (*service.Guide, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.Guide{
		ID:          id,
		Title:       g.Title,
		Description: g.Description,
		Icon:        g.Icon,
		Content:     g.Content,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   g.CreatedBy,
		UpdatedBy:   g.UpdatedBy,
	}

	m.mu.Lock()
	m.guides[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateGuide(_ context.Context, id string, g service.Guide) (*service.Guide, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.guides[id]
	if !ok {
		return nil, nil
	}

	existing.Title = g.Title
	existing.Description = g.Description
	existing.Icon = g.Icon
	existing.Content = g.Content
	existing.UpdatedAt = now
	existing.UpdatedBy = g.UpdatedBy
	m.guides[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteGuide(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.guides, id)
	m.mu.Unlock()

	return nil
}
