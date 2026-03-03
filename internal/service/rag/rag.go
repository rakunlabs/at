package rag

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/vectorstores"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// ProviderLookup resolves a provider key to its persisted config.
// This is injected by the server so the RAG service can build embedders
// from any configured AT provider.
type ProviderLookup func(ctx context.Context, key string) (*config.LLMConfig, error)

// Service is the core RAG engine. It orchestrates document ingestion
// (load → split → embed → store) and similarity search against collections.
//
// The service is stateless — it creates embedders and vector store connections
// on demand from the collection configuration stored in the DB. For long-lived
// operations (e.g. bulk ingestion) vector store connections are cached per
// collection for the duration of the operation.
type Service struct {
	collectionStore service.RAGCollectionStorer
	providerLookup  ProviderLookup

	// vectorStoreCache caches open vector store connections keyed by collection ID.
	// Entries are created on first access and closed when evicted.
	vectorStoreMu    sync.RWMutex
	vectorStoreCache map[string]VectorStoreCloser
}

// NewService creates a new RAG service.
func NewService(collectionStore service.RAGCollectionStorer, providerLookup ProviderLookup) *Service {
	return &Service{
		collectionStore:  collectionStore,
		providerLookup:   providerLookup,
		vectorStoreCache: make(map[string]VectorStoreCloser),
	}
}

// Close releases all cached vector store connections.
func (s *Service) Close() {
	s.vectorStoreMu.Lock()
	defer s.vectorStoreMu.Unlock()

	for id, vs := range s.vectorStoreCache {
		if err := vs.Close(); err != nil {
			slog.Error("close vector store", "collection_id", id, "error", err)
		}
	}
	s.vectorStoreCache = make(map[string]VectorStoreCloser)
}

// InvalidateCache removes a collection's cached vector store connection,
// closing it if open. Call this after a collection is updated or deleted.
func (s *Service) InvalidateCache(collectionID string) {
	s.vectorStoreMu.Lock()
	defer s.vectorStoreMu.Unlock()

	if vs, ok := s.vectorStoreCache[collectionID]; ok {
		if err := vs.Close(); err != nil {
			slog.Error("close vector store on invalidate", "collection_id", collectionID, "error", err)
		}
		delete(s.vectorStoreCache, collectionID)
	}
}

// ─── Ingest ───

// IngestResult contains statistics about an ingestion operation.
type IngestResult struct {
	// ChunksStored is the number of document chunks stored in the vector store.
	ChunksStored int `json:"chunks_stored"`
	// Source is the original filename or URL.
	Source string `json:"source"`
}

// Ingest loads a document, splits it into chunks, and stores the embeddings
// in the collection's vector store. The contentType should be a MIME type
// (e.g. "text/markdown"). If empty, it will be detected from the source filename.
// Any entries in extraMetadata are merged into every chunk's metadata.
func (s *Service) Ingest(ctx context.Context, collectionID string, content io.Reader, contentType string, source string, extraMetadata map[string]any) (*IngestResult, error) {
	collection, err := s.collectionStore.GetRAGCollection(ctx, collectionID)
	if err != nil {
		return nil, fmt.Errorf("get collection: %w", err)
	}
	if collection == nil {
		return nil, fmt.Errorf("collection %q not found", collectionID)
	}

	// Auto-detect content type from source filename if not provided.
	if contentType == "" {
		contentType = DetectContentType(source)
		if contentType == "" {
			return nil, fmt.Errorf("cannot detect content type for %q — provide content_type explicitly", source)
		}
	}

	// Load and split the document.
	chunks, err := LoadDocuments(ctx, content, contentType, source, collection.ChunkSize, collection.ChunkOverlap, extraMetadata)
	if err != nil {
		return nil, fmt.Errorf("load documents: %w", err)
	}

	if len(chunks) == 0 {
		return &IngestResult{ChunksStored: 0, Source: source}, nil
	}

	// Get or create a vector store for this collection.
	vs, err := s.getVectorStore(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("get vector store: %w", err)
	}

	// Store the chunks.
	_, err = vs.AddDocuments(ctx, chunks)
	if err != nil {
		return nil, fmt.Errorf("add documents to vector store: %w", err)
	}

	slog.Info("ingested document",
		"collection", collection.Name,
		"source", source,
		"chunks", len(chunks),
	)

	return &IngestResult{
		ChunksStored: len(chunks),
		Source:       source,
	}, nil
}

// IngestChunks stores pre-split document chunks directly into the collection's
// vector store. Useful when the caller has already loaded and split documents
// (e.g. git import processing multiple files).
func (s *Service) IngestChunks(ctx context.Context, collectionID string, chunks []schema.Document) (int, error) {
	if len(chunks) == 0 {
		return 0, nil
	}

	collection, err := s.collectionStore.GetRAGCollection(ctx, collectionID)
	if err != nil {
		return 0, fmt.Errorf("get collection: %w", err)
	}
	if collection == nil {
		return 0, fmt.Errorf("collection %q not found", collectionID)
	}

	vs, err := s.getVectorStore(ctx, collection)
	if err != nil {
		return 0, fmt.Errorf("get vector store: %w", err)
	}

	_, err = vs.AddDocuments(ctx, chunks)
	if err != nil {
		return 0, fmt.Errorf("add documents to vector store: %w", err)
	}

	return len(chunks), nil
}

// DeleteDocumentsBySource removes all document chunks from a collection whose
// "source" metadata matches the given value. This is used by RAG ingest workflows
// to remove stale chunks before re-ingesting updated files.
//
// Returns ErrDeleteNotSupported if the collection's vector store backend
// does not support metadata-based deletion.
func (s *Service) DeleteDocumentsBySource(ctx context.Context, collectionID, source string) error {
	collection, err := s.collectionStore.GetRAGCollection(ctx, collectionID)
	if err != nil {
		return fmt.Errorf("get collection: %w", err)
	}
	if collection == nil {
		return fmt.Errorf("collection %q not found", collectionID)
	}

	vs, err := s.getVectorStore(ctx, collection)
	if err != nil {
		return fmt.Errorf("get vector store: %w", err)
	}

	if err := vs.DeleteByMetadata(ctx, "source", source); err != nil {
		return fmt.Errorf("delete by source %q: %w", source, err)
	}

	slog.Info("deleted documents by source",
		"collection", collection.Name,
		"source", source,
	)

	return nil
}

// ─── Search ───

// SearchRequest describes a similarity search query.
type SearchRequest struct {
	// Query is the natural language search query.
	Query string `json:"query"`

	// CollectionIDs is the list of collections to search. If empty, all
	// collections are searched.
	CollectionIDs []string `json:"collection_ids,omitempty"`

	// NumResults is the maximum number of results to return (default 5).
	NumResults int `json:"num_results,omitempty"`

	// ScoreThreshold is the minimum similarity score (0–1). Results below
	// this threshold are filtered out. 0 means no threshold.
	ScoreThreshold float32 `json:"score_threshold,omitempty"`
}

// SearchResult is a single search hit.
type SearchResult struct {
	Content      string         `json:"content"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	Score        float32        `json:"score"`
	CollectionID string         `json:"collection_id"`
}

// Search performs a similarity search across one or more collections.
func (s *Service) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if req.Query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if req.NumResults <= 0 {
		req.NumResults = 5
	}

	// Determine which collections to search.
	var collections []service.RAGCollection
	if len(req.CollectionIDs) > 0 {
		for _, id := range req.CollectionIDs {
			c, err := s.collectionStore.GetRAGCollection(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("get collection %q: %w", id, err)
			}
			if c == nil {
				return nil, fmt.Errorf("collection %q not found", id)
			}
			collections = append(collections, *c)
		}
	} else {
		var err error
		var result *service.ListResult[service.RAGCollection]
		result, err = s.collectionStore.ListRAGCollections(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("list collections: %w", err)
		}
		collections = result.Data
	}

	if len(collections) == 0 {
		return []SearchResult{}, nil
	}

	// Search each collection and merge results.
	var allResults []SearchResult

	for _, collection := range collections {
		vs, err := s.getVectorStore(ctx, &collection)
		if err != nil {
			slog.Error("get vector store for search", "collection", collection.Name, "error", err)
			continue
		}

		opts := []vectorstores.Option{}
		if req.ScoreThreshold > 0 {
			opts = append(opts, vectorstores.WithScoreThreshold(req.ScoreThreshold))
		}

		docs, err := vs.SimilaritySearch(ctx, req.Query, req.NumResults, opts...)
		if err != nil {
			slog.Error("similarity search failed", "collection", collection.Name, "error", err)
			continue
		}

		for _, doc := range docs {
			allResults = append(allResults, SearchResult{
				Content:      doc.PageContent,
				Metadata:     doc.Metadata,
				Score:        doc.Score,
				CollectionID: collection.ID,
			})
		}
	}

	// Sort by score descending and limit to NumResults.
	sortSearchResults(allResults)
	if len(allResults) > req.NumResults {
		allResults = allResults[:req.NumResults]
	}

	return allResults, nil
}

// ─── Vector Store Management ───

// getVectorStore returns a cached or newly created vector store for the collection.
func (s *Service) getVectorStore(ctx context.Context, collection *service.RAGCollection) (VectorStoreCloser, error) {
	s.vectorStoreMu.RLock()
	vs, ok := s.vectorStoreCache[collection.ID]
	s.vectorStoreMu.RUnlock()

	if ok {
		return vs, nil
	}

	// Create a new vector store connection.
	embedder, err := s.createEmbedder(ctx, collection)
	if err != nil {
		return nil, fmt.Errorf("create embedder for provider %q: %w", collection.EmbeddingProvider, err)
	}

	namespace := collection.Name // Use collection name as namespace.
	vs, err = NewVectorStore(ctx, collection.VectorStore, embedder, namespace)
	if err != nil {
		return nil, fmt.Errorf("create vector store %q: %w", collection.VectorStore.Type, err)
	}

	// Cache it.
	s.vectorStoreMu.Lock()
	// Double-check in case another goroutine created it while we were waiting.
	if existing, ok := s.vectorStoreCache[collection.ID]; ok {
		s.vectorStoreMu.Unlock()
		// Close the one we just created and use the cached one.
		vs.Close()
		return existing, nil
	}
	s.vectorStoreCache[collection.ID] = vs
	s.vectorStoreMu.Unlock()

	return vs, nil
}

// createEmbedder builds a langchaingo Embedder from a RAG collection's provider config.
func (s *Service) createEmbedder(ctx context.Context, collection *service.RAGCollection) (embeddings.Embedder, error) {
	cfg, err := s.providerLookup(ctx, collection.EmbeddingProvider)
	if err != nil {
		return nil, fmt.Errorf("lookup provider %q: %w", collection.EmbeddingProvider, err)
	}

	client, err := NewATEmbedderClient(ATEmbedderConfig{
		BaseURL:            cfg.BaseURL,
		EmbeddingURL:       collection.EmbeddingURL,
		APIType:            collection.EmbeddingAPIType,
		Model:              collection.EmbeddingModel,
		APIKey:             cfg.APIKey,
		Proxy:              cfg.Proxy,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	})
	if err != nil {
		return nil, fmt.Errorf("create embedder client: %w", err)
	}

	embedder, err := embeddings.NewEmbedder(client)
	if err != nil {
		return nil, fmt.Errorf("create embedder: %w", err)
	}

	return embedder, nil
}

// ─── Helpers ───

// sortSearchResults sorts results by score descending (highest first).
func sortSearchResults(results []SearchResult) {
	// Simple insertion sort — result sets are small (typically <50 items).
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}
