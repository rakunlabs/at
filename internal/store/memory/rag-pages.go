package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func (m *Memory) ListRAGPages(_ context.Context, collectionID string, q *query.Query) (*service.ListResult[service.RAGPage], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []service.RAGPage
	for _, p := range m.ragPages {
		if p.CollectionID == collectionID {
			result = append(result, p)
		}
	}

	slices.SortFunc(result, func(a, b service.RAGPage) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})

	total := uint64(len(result))

	// Apply pagination.
	var offset, limit uint64
	if q != nil {
		offset = q.GetOffset()
		limit = q.GetLimit()
	}

	if offset > 0 && offset < total {
		result = result[offset:]
	} else if offset >= total {
		result = nil
	}

	if limit > 0 && uint64(len(result)) > limit {
		result = result[:limit]
	}

	return &service.ListResult[service.RAGPage]{
		Data: result,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, nil
}

func (m *Memory) GetRAGPage(_ context.Context, id string) (*service.RAGPage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.ragPages[id]
	if !ok {
		return nil, nil
	}

	return &p, nil
}

func (m *Memory) GetRAGPageBySource(_ context.Context, collectionID, source string) (*service.RAGPage, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, p := range m.ragPages {
		if p.CollectionID == collectionID && p.Source == source {
			return &p, nil
		}
	}

	return nil, nil
}

func (m *Memory) UpsertRAGPage(_ context.Context, page service.RAGPage) (*service.RAGPage, error) {
	// Round-trip metadata through JSON to normalize.
	raw, err := json.Marshal(page.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal rag page metadata: %w", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal rag page metadata: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	contentHash := page.ContentHash
	if contentHash == "" {
		h := sha256.Sum256([]byte(page.Content))
		contentHash = hex.EncodeToString(h[:])
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check for existing by (collectionID, source).
	for id, existing := range m.ragPages {
		if existing.CollectionID == page.CollectionID && existing.Source == page.Source {
			existing.Path = page.Path
			existing.Content = page.Content
			existing.ContentType = page.ContentType
			existing.Metadata = normalized
			existing.ContentHash = contentHash
			existing.UpdatedAt = now
			m.ragPages[id] = existing

			return &existing, nil
		}
	}

	// Insert new.
	id := ulid.Make().String()
	rec := service.RAGPage{
		ID:           id,
		CollectionID: page.CollectionID,
		Source:       page.Source,
		Path:         page.Path,
		Content:      page.Content,
		ContentType:  page.ContentType,
		Metadata:     normalized,
		ContentHash:  contentHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	m.ragPages[id] = rec

	return &rec, nil
}

func (m *Memory) DeleteRAGPage(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.ragPages, id)
	m.mu.Unlock()

	return nil
}

func (m *Memory) DeleteRAGPagesByCollectionID(_ context.Context, collectionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, p := range m.ragPages {
		if p.CollectionID == collectionID {
			delete(m.ragPages, id)
		}
	}

	return nil
}

func (m *Memory) DeleteRAGPageBySource(_ context.Context, collectionID, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, p := range m.ragPages {
		if p.CollectionID == collectionID && p.Source == source {
			delete(m.ragPages, id)
			break
		}
	}

	return nil
}
