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

func (m *Memory) ListRAGCollections(_ context.Context, q *query.Query) (*service.ListResult[service.RAGCollection], error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]service.RAGCollection, 0, len(m.ragCollections))
	for _, c := range m.ragCollections {
		result = append(result, c)
	}

	slices.SortFunc(result, func(a, b service.RAGCollection) int {
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

func (m *Memory) GetRAGCollection(_ context.Context, id string) (*service.RAGCollection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	c, ok := m.ragCollections[id]
	if !ok {
		return nil, nil
	}

	return &c, nil
}

func (m *Memory) GetRAGCollectionByName(_ context.Context, name string) (*service.RAGCollection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, c := range m.ragCollections {
		if c.Name == name {
			return &c, nil
		}
	}

	return nil, nil
}

func (m *Memory) CreateRAGCollection(_ context.Context, c service.RAGCollection) (*service.RAGCollection, error) {
	// Round-trip through JSON to normalize.
	raw, err := json.Marshal(c.VectorStore)
	if err != nil {
		return nil, fmt.Errorf("marshal vector store config: %w", err)
	}
	var normalized service.RAGVectorStoreConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal vector store config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	// Default chunk settings.
	chunkSize := c.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	chunkOverlap := c.ChunkOverlap
	if chunkOverlap < 0 {
		chunkOverlap = 200
	}

	rec := service.RAGCollection{
		ID:                id,
		Name:              c.Name,
		Description:       c.Description,
		VectorStore:       normalized,
		EmbeddingProvider: c.EmbeddingProvider,
		EmbeddingModel:    c.EmbeddingModel,
		EmbeddingURL:      c.EmbeddingURL,
		EmbeddingAPIType:  c.EmbeddingAPIType,
		ChunkSize:         chunkSize,
		ChunkOverlap:      chunkOverlap,
		CreatedAt:         now,
		UpdatedAt:         now,
		CreatedBy:         c.CreatedBy,
		UpdatedBy:         c.UpdatedBy,
	}

	m.mu.Lock()
	m.ragCollections[id] = rec
	m.mu.Unlock()

	return &rec, nil
}

func (m *Memory) UpdateRAGCollection(_ context.Context, id string, c service.RAGCollection) (*service.RAGCollection, error) {
	raw, err := json.Marshal(c.VectorStore)
	if err != nil {
		return nil, fmt.Errorf("marshal vector store config: %w", err)
	}
	var normalized service.RAGVectorStoreConfig
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("unmarshal vector store config: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.ragCollections[id]
	if !ok {
		return nil, nil
	}

	existing.Name = c.Name
	existing.Description = c.Description
	existing.VectorStore = normalized
	existing.EmbeddingProvider = c.EmbeddingProvider
	existing.EmbeddingModel = c.EmbeddingModel
	existing.EmbeddingURL = c.EmbeddingURL
	existing.EmbeddingAPIType = c.EmbeddingAPIType
	existing.ChunkSize = c.ChunkSize
	existing.ChunkOverlap = c.ChunkOverlap
	existing.UpdatedAt = now
	existing.UpdatedBy = c.UpdatedBy
	m.ragCollections[id] = existing

	return &existing, nil
}

func (m *Memory) DeleteRAGCollection(_ context.Context, id string) error {
	m.mu.Lock()
	delete(m.ragCollections, id)
	m.mu.Unlock()

	return nil
}
