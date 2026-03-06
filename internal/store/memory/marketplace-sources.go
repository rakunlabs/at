package memory

import (
	"context"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Marketplace Source CRUD ───

func (m *Memory) ListMarketplaceSources(_ context.Context) ([]service.MarketplaceSource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.MarketplaceSource, 0, len(m.marketplaceSources))
	for _, src := range m.marketplaceSources {
		result = append(result, src)
	}

	slices.SortFunc(result, func(a, b service.MarketplaceSource) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return result, nil
}

func (m *Memory) GetMarketplaceSource(_ context.Context, id string) (*service.MarketplaceSource, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	src, ok := m.marketplaceSources[id]
	if !ok {
		return nil, nil
	}

	return &src, nil
}

func (m *Memory) CreateMarketplaceSource(_ context.Context, src service.MarketplaceSource) (*service.MarketplaceSource, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	rec := service.MarketplaceSource{
		ID:        id,
		Name:      src.Name,
		Type:      src.Type,
		SearchURL: src.SearchURL,
		TopURL:    src.TopURL,
		Enabled:   src.Enabled,
		CreatedAt: now,
		UpdatedAt: now,
	}

	m.mu.Lock()
	m.marketplaceSources[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateMarketplaceSource(_ context.Context, id string, src service.MarketplaceSource) (*service.MarketplaceSource, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.marketplaceSources[id]
	if !ok {
		return nil, nil
	}

	existing.Name = src.Name
	existing.Type = src.Type
	existing.SearchURL = src.SearchURL
	existing.TopURL = src.TopURL
	existing.Enabled = src.Enabled
	existing.UpdatedAt = now
	m.marketplaceSources[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteMarketplaceSource(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.marketplaceSources, id)
	m.mu.Unlock()

	return nil
}
