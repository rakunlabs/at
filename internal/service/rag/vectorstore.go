package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/vectorstores"
	"github.com/tmc/langchaingo/vectorstores/chroma"
	"github.com/tmc/langchaingo/vectorstores/milvus"
	"github.com/tmc/langchaingo/vectorstores/pgvector"
	"github.com/tmc/langchaingo/vectorstores/pinecone"
	"github.com/tmc/langchaingo/vectorstores/qdrant"
	"github.com/tmc/langchaingo/vectorstores/weaviate"

	milvusclient "github.com/milvus-io/milvus-sdk-go/v2/client"

	"github.com/rakunlabs/at/internal/service"
)

// VectorStoreCloser extends VectorStore with an optional Close method
// and metadata-based deletion for backends that support it.
// Some backends (e.g. pgvector) hold open connections that should be
// released when the collection is removed or reconfigured.
type VectorStoreCloser interface {
	vectorstores.VectorStore
	Close() error

	// DeleteByMetadata deletes documents whose metadata field matches a value.
	// Returns ErrDeleteNotSupported for backends that don't implement this.
	DeleteByMetadata(ctx context.Context, key, value string) error
}

// ErrDeleteNotSupported is returned by backends that don't support
// metadata-based deletion.
var ErrDeleteNotSupported = fmt.Errorf("delete by metadata not supported for this vector store backend")

// simpleCloser wraps a VectorStore that doesn't need closing or
// metadata-based deletion.
type simpleCloser struct {
	vectorstores.VectorStore
}

func (simpleCloser) Close() error { return nil }
func (simpleCloser) DeleteByMetadata(_ context.Context, _, _ string) error {
	return ErrDeleteNotSupported
}

// pgvectorCloser wraps pgvector.Store to implement VectorStoreCloser.
type pgvectorCloser struct {
	pgvector.Store
}

func (p pgvectorCloser) Close() error { return p.Store.Close() }
func (pgvectorCloser) DeleteByMetadata(_ context.Context, _, _ string) error {
	return ErrDeleteNotSupported
}

// chromaCloser wraps a chroma Store and retains connection metadata
// so we can call the Chroma REST API for delete-by-metadata.
type chromaCloser struct {
	vectorstores.VectorStore
	chromaURL  string // e.g. "http://localhost:8000"
	namespace  string // collection name
	httpClient *http.Client
}

func (c *chromaCloser) Close() error { return nil }

// DeleteByMetadata deletes documents from Chroma whose metadata key matches value.
// Uses the Chroma REST API: POST /api/v1/collections/{collection_id}/delete
// with {"where": {key: value}}.
func (c *chromaCloser) DeleteByMetadata(ctx context.Context, key, value string) error {
	// Step 1: Get the collection ID by name.
	collectionID, err := c.getCollectionID(ctx)
	if err != nil {
		return fmt.Errorf("chroma: get collection ID: %w", err)
	}

	// Step 2: Delete by metadata filter.
	deleteURL := fmt.Sprintf("%s/api/v1/collections/%s/delete", c.chromaURL, collectionID)

	body := map[string]any{
		"where": map[string]any{
			key: value,
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("chroma: marshal delete request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deleteURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("chroma: create delete request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("chroma: delete request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chroma: delete failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// getCollectionID resolves the collection name to its Chroma ID.
func (c *chromaCloser) getCollectionID(ctx context.Context) (string, error) {
	listURL := fmt.Sprintf("%s/api/v1/collections?name=%s", c.chromaURL, url.QueryEscape(c.namespace))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("list collections (status %d): %s", resp.StatusCode, string(respBody))
	}

	var collections []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&collections); err != nil {
		return "", fmt.Errorf("decode collections: %w", err)
	}

	for _, col := range collections {
		if col.Name == c.namespace {
			return col.ID, nil
		}
	}

	return "", fmt.Errorf("collection %q not found", c.namespace)
}

// qdrantCloser wraps a qdrant Store and retains connection metadata
// so we can call the Qdrant REST API for delete-by-metadata.
type qdrantCloser struct {
	vectorstores.VectorStore
	qdrantURL      string // e.g. "http://localhost:6333"
	collectionName string
	apiKey         string
	httpClient     *http.Client
}

func (q *qdrantCloser) Close() error { return nil }

// DeleteByMetadata deletes points from Qdrant whose payload key matches value.
// Uses the Qdrant REST API: POST /collections/{name}/points/delete
// with {"filter": {"must": [{"key": key, "match": {"value": value}}]}}.
func (q *qdrantCloser) DeleteByMetadata(ctx context.Context, key, value string) error {
	deleteURL := fmt.Sprintf("%s/collections/%s/points/delete", q.qdrantURL, url.PathEscape(q.collectionName))

	body := map[string]any{
		"filter": map[string]any{
			"must": []any{
				map[string]any{
					"key": key,
					"match": map[string]any{
						"value": value,
					},
				},
			},
		},
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("qdrant: marshal delete request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deleteURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("qdrant: create delete request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if q.apiKey != "" {
		req.Header.Set("api-key", q.apiKey)
	}

	resp, err := q.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: delete request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: delete failed (status %d): %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// NewVectorStore creates a langchaingo VectorStore from the collection's
// vector store configuration and embedder. The returned VectorStoreCloser
// should be closed when no longer needed.
func NewVectorStore(ctx context.Context, vsCfg service.RAGVectorStoreConfig, embedder embeddings.Embedder, namespace string) (VectorStoreCloser, error) {
	switch vsCfg.Type {
	case "pgvector":
		return newPgvectorStore(ctx, vsCfg.Config, embedder, namespace)
	case "chroma":
		return newChromaStore(vsCfg.Config, embedder, namespace)
	case "qdrant":
		return newQdrantStore(vsCfg.Config, embedder, namespace)
	case "weaviate":
		return newWeaviateStore(vsCfg.Config, embedder, namespace)
	case "pinecone":
		return newPineconeStore(vsCfg.Config, embedder, namespace)
	case "milvus":
		return newMilvusStore(ctx, vsCfg.Config, embedder, namespace)
	default:
		return nil, fmt.Errorf("unsupported vector store type: %q", vsCfg.Type)
	}
}

// SupportedVectorStoreTypes returns the list of supported vector store backend types.
func SupportedVectorStoreTypes() []string {
	return []string{"pgvector", "chroma", "qdrant", "weaviate", "pinecone", "milvus"}
}

// ─── Backend Constructors ───

func newPgvectorStore(ctx context.Context, cfg map[string]any, embedder embeddings.Embedder, namespace string) (VectorStoreCloser, error) {
	connURL := getString(cfg, "connection_url")
	if connURL == "" {
		return nil, fmt.Errorf("pgvector: connection_url is required")
	}

	opts := []pgvector.Option{
		pgvector.WithConnectionURL(connURL),
		pgvector.WithEmbedder(embedder),
	}

	if namespace != "" {
		opts = append(opts, pgvector.WithCollectionName(namespace))
	}
	if v := getInt(cfg, "vector_dimensions"); v > 0 {
		opts = append(opts, pgvector.WithVectorDimensions(v))
	}

	store, err := pgvector.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("pgvector: %w", err)
	}

	return pgvectorCloser{store}, nil
}

func newChromaStore(cfg map[string]any, embedder embeddings.Embedder, namespace string) (VectorStoreCloser, error) {
	chromaURL := getString(cfg, "url")
	if chromaURL == "" {
		return nil, fmt.Errorf("chroma: url is required")
	}

	opts := []chroma.Option{
		chroma.WithChromaURL(chromaURL),
		chroma.WithEmbedder(embedder),
	}

	if namespace != "" {
		opts = append(opts, chroma.WithNameSpace(namespace))
	}

	store, err := chroma.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("chroma: %w", err)
	}

	return &chromaCloser{
		VectorStore: store,
		chromaURL:   chromaURL,
		namespace:   namespace,
		httpClient:  &http.Client{},
	}, nil
}

func newQdrantStore(cfg map[string]any, embedder embeddings.Embedder, namespace string) (VectorStoreCloser, error) {
	qdrantURL := getString(cfg, "url")
	if qdrantURL == "" {
		return nil, fmt.Errorf("qdrant: url is required")
	}

	parsedURL, err := url.Parse(qdrantURL)
	if err != nil {
		return nil, fmt.Errorf("qdrant: parse url: %w", err)
	}

	apiKey := getString(cfg, "api_key")

	opts := []qdrant.Option{
		qdrant.WithURL(*parsedURL),
		qdrant.WithEmbedder(embedder),
	}

	collectionName := namespace
	if collectionName != "" {
		opts = append(opts, qdrant.WithCollectionName(collectionName))
	}
	if apiKey != "" {
		opts = append(opts, qdrant.WithAPIKey(apiKey))
	}

	store, err := qdrant.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("qdrant: %w", err)
	}

	return &qdrantCloser{
		VectorStore:    store,
		qdrantURL:      qdrantURL,
		collectionName: collectionName,
		apiKey:         apiKey,
		httpClient:     &http.Client{},
	}, nil
}

func newWeaviateStore(cfg map[string]any, embedder embeddings.Embedder, namespace string) (VectorStoreCloser, error) {
	host := getString(cfg, "host")
	if host == "" {
		return nil, fmt.Errorf("weaviate: host is required")
	}

	scheme := getString(cfg, "scheme")
	if scheme == "" {
		scheme = "https"
	}

	opts := []weaviate.Option{
		weaviate.WithHost(host),
		weaviate.WithScheme(scheme),
		weaviate.WithEmbedder(embedder),
	}

	if namespace != "" {
		opts = append(opts, weaviate.WithNameSpace(namespace))
	}
	if v := getString(cfg, "api_key"); v != "" {
		opts = append(opts, weaviate.WithAPIKey(v))
	}
	if v := getString(cfg, "index_name"); v != "" {
		opts = append(opts, weaviate.WithIndexName(v))
	}

	store, err := weaviate.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("weaviate: %w", err)
	}

	return simpleCloser{store}, nil
}

func newPineconeStore(cfg map[string]any, embedder embeddings.Embedder, namespace string) (VectorStoreCloser, error) {
	apiKey := getString(cfg, "api_key")
	if apiKey == "" {
		return nil, fmt.Errorf("pinecone: api_key is required")
	}

	host := getString(cfg, "host")
	if host == "" {
		return nil, fmt.Errorf("pinecone: host is required")
	}

	opts := []pinecone.Option{
		pinecone.WithAPIKey(apiKey),
		pinecone.WithHost(host),
		pinecone.WithEmbedder(embedder),
	}

	if namespace != "" {
		opts = append(opts, pinecone.WithNameSpace(namespace))
	}

	store, err := pinecone.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("pinecone: %w", err)
	}

	return simpleCloser{store}, nil
}

func newMilvusStore(ctx context.Context, cfg map[string]any, embedder embeddings.Embedder, namespace string) (VectorStoreCloser, error) {
	address := getString(cfg, "address")
	if address == "" {
		return nil, fmt.Errorf("milvus: address is required")
	}

	milvusCfg := milvusclient.Config{
		Address: address,
	}

	opts := []milvus.Option{
		milvus.WithEmbedder(embedder),
	}

	if namespace != "" {
		opts = append(opts, milvus.WithCollectionName(namespace))
	}

	store, err := milvus.New(ctx, milvusCfg, opts...)
	if err != nil {
		return nil, fmt.Errorf("milvus: %w", err)
	}

	return simpleCloser{store}, nil
}

// ─── Config Helpers ───

func getString(cfg map[string]any, key string) string {
	if cfg == nil {
		return ""
	}
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return fmt.Sprintf("%v", v)
	}
	return s
}

func getInt(cfg map[string]any, key string) int {
	if cfg == nil {
		return 0
	}
	v, ok := cfg[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}
