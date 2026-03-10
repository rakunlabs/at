package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/rag"
	"github.com/rakunlabs/query"
)

// sha256Hex returns the hex-encoded SHA-256 hash of s.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// ─── Collection CRUD ───

// ragCollectionsResponse wraps a list of RAG collection records for JSON output.
type ragCollectionsResponse struct {
	Collections []service.RAGCollection `json:"collections"`
}

// ListRAGCollectionsAPI handles GET /api/v1/rag/collections.
func (s *Server) ListRAGCollectionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragCollectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.ragCollectionStore.ListRAGCollections(r.Context(), q)
	if err != nil {
		slog.Error("list rag collections failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list rag collections: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.RAGCollection]{Data: []service.RAGCollection{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetRAGCollectionAPI handles GET /api/v1/rag/collections/{id}.
func (s *Server) GetRAGCollectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragCollectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	record, err := s.ragCollectionStore.GetRAGCollection(r.Context(), id)
	if err != nil {
		slog.Error("get rag collection failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get rag collection: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("rag collection %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateRAGCollectionAPI handles POST /api/v1/rag/collections.
func (s *Server) CreateRAGCollectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragCollectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.RAGCollection
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Config.VectorStore.Type == "" {
		httpResponse(w, "vector_store.type is required", http.StatusBadRequest)
		return
	}

	if req.Config.EmbeddingProvider == "" {
		httpResponse(w, "embedding_provider is required", http.StatusBadRequest)
		return
	}

	if req.Config.EmbeddingModel == "" && req.Config.EmbeddingURL == "" {
		httpResponse(w, "embedding_model is required when embedding_url is not set", http.StatusBadRequest)
		return
	}

	// Set defaults.
	if req.Config.ChunkSize <= 0 {
		req.Config.ChunkSize = 512
	}
	if req.Config.ChunkOverlap < 0 {
		req.Config.ChunkOverlap = 100
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.ragCollectionStore.CreateRAGCollection(r.Context(), req)
	if err != nil {
		slog.Error("create rag collection failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create rag collection: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateRAGCollectionAPI handles PUT /api/v1/rag/collections/{id}.
func (s *Server) UpdateRAGCollectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragCollectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	var req service.RAGCollection
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	if req.Config.VectorStore.Type == "" {
		httpResponse(w, "vector_store.type is required", http.StatusBadRequest)
		return
	}

	if req.Config.EmbeddingProvider == "" {
		httpResponse(w, "embedding_provider is required", http.StatusBadRequest)
		return
	}

	if req.Config.EmbeddingModel == "" && req.Config.EmbeddingURL == "" {
		httpResponse(w, "embedding_model is required when embedding_url is not set", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.ragCollectionStore.UpdateRAGCollection(r.Context(), id, req)
	if err != nil {
		slog.Error("update rag collection failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update rag collection: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("rag collection %q not found", id), http.StatusNotFound)
		return
	}

	// Invalidate cached vector store connection on config change.
	if s.ragService != nil {
		s.ragService.InvalidateCache(id)
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteRAGCollectionAPI handles DELETE /api/v1/rag/collections/{id}.
func (s *Server) DeleteRAGCollectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragCollectionStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	// Invalidate cached vector store connection before deleting.
	if s.ragService != nil {
		s.ragService.InvalidateCache(id)
	}

	if err := s.ragCollectionStore.DeleteRAGCollection(r.Context(), id); err != nil {
		slog.Error("delete rag collection failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete rag collection: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Document Upload ───

// uploadRAGDocumentResponse is returned after a document upload.
type uploadRAGDocumentResponse struct {
	ChunksStored int    `json:"chunks_stored"`
	Source       string `json:"source"`
}

// UploadRAGDocumentAPI handles POST /api/v1/rag/collections/{id}/documents.
// Accepts multipart/form-data with a "file" field, or raw body with
// Content-Type and X-Source-Filename headers.
func (s *Server) UploadRAGDocumentAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragService == nil {
		httpResponse(w, "rag service not configured", http.StatusServiceUnavailable)
		return
	}

	collectionID := r.PathValue("id")
	if collectionID == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	var (
		content     io.Reader
		contentType string
		source      string
	)

	// Check if multipart.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(64 << 20); err != nil { // 64 MB max
			httpResponse(w, fmt.Sprintf("parse multipart form: %v", err), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			httpResponse(w, fmt.Sprintf("file field required: %v", err), http.StatusBadRequest)
			return
		}
		defer file.Close()

		content = file
		source = header.Filename
		contentType = header.Header.Get("Content-Type")

		// If Content-Type wasn't set by the client, detect from filename.
		if contentType == "" || contentType == "application/octet-stream" {
			contentType = rag.DetectContentType(source)
		}

		// Allow overriding content type via form field.
		if ct := r.FormValue("content_type"); ct != "" {
			contentType = ct
		}
	} else {
		// Raw body upload — headers provide metadata.
		content = r.Body
		contentType = r.Header.Get("Content-Type")
		source = r.Header.Get("X-Source-Filename")
		if source == "" {
			source = "upload"
		}
	}

	// Buffer content so we can both ingest and store as a page.
	contentBytes, err := io.ReadAll(content)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to read content: %v", err), http.StatusBadRequest)
		return
	}

	result, err := s.ragService.Ingest(r.Context(), collectionID, bytes.NewReader(contentBytes), contentType, source, nil)
	if err != nil {
		slog.Error("rag ingest failed", "collection_id", collectionID, "source", source, "error", err)
		httpResponse(w, fmt.Sprintf("ingest failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Store original content in rag_pages.
	if s.ragPageStore != nil {
		_, pageErr := s.ragPageStore.UpsertRAGPage(r.Context(), service.RAGPage{
			CollectionID: collectionID,
			Source:       result.Source,
			Path:         source,
			Content:      string(contentBytes),
			ContentType:  contentType,
			ContentHash:  sha256Hex(string(contentBytes)),
		})
		if pageErr != nil {
			slog.Warn("store page after upload failed", "source", result.Source, "error", pageErr)
		}
	}

	httpResponseJSON(w, uploadRAGDocumentResponse{
		ChunksStored: result.ChunksStored,
		Source:       result.Source,
	}, http.StatusCreated)
}

// ─── URL Import ───

// importRAGFromURLRequest is the request body for URL import.
type importRAGFromURLRequest struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type,omitempty"` // optional override
}

// ImportRAGFromURLAPI handles POST /api/v1/rag/collections/{id}/import/url.
// Fetches content from a URL and ingests it into the collection.
func (s *Server) ImportRAGFromURLAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragService == nil {
		httpResponse(w, "rag service not configured", http.StatusServiceUnavailable)
		return
	}

	collectionID := r.PathValue("id")
	if collectionID == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	var req importRAGFromURLRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.URL == "" {
		httpResponse(w, "url is required", http.StatusBadRequest)
		return
	}

	// Fetch the URL.
	httpReq, err := http.NewRequestWithContext(r.Context(), http.MethodGet, req.URL, nil)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid url: %v", err), http.StatusBadRequest)
		return
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		slog.Error("fetch url failed", "url", req.URL, "error", err)
		httpResponse(w, fmt.Sprintf("failed to fetch url: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		httpResponse(w, fmt.Sprintf("url returned status %d", resp.StatusCode), http.StatusBadGateway)
		return
	}

	contentType := req.ContentType
	if contentType == "" {
		contentType = resp.Header.Get("Content-Type")
	}
	if contentType == "" {
		// Try to detect from URL path.
		contentType = rag.DetectContentType(req.URL)
	}

	// Buffer content so we can both ingest and store as a page.
	contentBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to read response body: %v", err), http.StatusBadGateway)
		return
	}

	result, err := s.ragService.Ingest(r.Context(), collectionID, bytes.NewReader(contentBytes), contentType, req.URL, nil)
	if err != nil {
		slog.Error("rag url import failed", "collection_id", collectionID, "url", req.URL, "error", err)
		httpResponse(w, fmt.Sprintf("import failed: %v", err), http.StatusInternalServerError)
		return
	}

	// Store original content in rag_pages.
	if s.ragPageStore != nil {
		_, pageErr := s.ragPageStore.UpsertRAGPage(r.Context(), service.RAGPage{
			CollectionID: collectionID,
			Source:       result.Source,
			Path:         req.URL,
			Content:      string(contentBytes),
			ContentType:  contentType,
			ContentHash:  sha256Hex(string(contentBytes)),
		})
		if pageErr != nil {
			slog.Warn("store page after url import failed", "source", result.Source, "error", pageErr)
		}
	}

	httpResponseJSON(w, uploadRAGDocumentResponse{
		ChunksStored: result.ChunksStored,
		Source:       result.Source,
	}, http.StatusCreated)
}

// ─── Search ───

// SearchRAGAPI handles POST /api/v1/rag/search.
func (s *Server) SearchRAGAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragService == nil {
		httpResponse(w, "rag service not configured", http.StatusServiceUnavailable)
		return
	}

	var req rag.SearchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	results, err := s.ragService.Search(r.Context(), req)
	if err != nil {
		slog.Error("rag search failed", "query", req.Query, "error", err)
		httpResponse(w, fmt.Sprintf("search failed: %v", err), http.StatusInternalServerError)
		return
	}

	if results == nil {
		results = []rag.SearchResult{}
	}

	httpResponseJSON(w, map[string]any{
		"results": results,
	}, http.StatusOK)
}

// ─── Embedding Model Discovery ───

// discoverEmbeddingModelsRequest is the JSON body for POST /api/v1/rag/discover-embedding-models.
type discoverEmbeddingModelsRequest struct {
	// EmbeddingProvider is the key of the AT provider whose credentials and
	// base URL are used to list available embedding models.
	EmbeddingProvider string `json:"embedding_provider"`

	// EmbeddingAPIType selects the embedding API format: "openai" or "gemini".
	// When empty, defaults to the provider's type.
	EmbeddingAPIType string `json:"embedding_api_type,omitempty"`

	// EmbeddingURL is an optional explicit embedding endpoint URL. When set,
	// its scheme+host is used as the base URL for model discovery instead of
	// the provider's base URL.
	EmbeddingURL string `json:"embedding_url,omitempty"`

	// EmbeddingBearerAuth sends the provider API key as a Bearer token instead
	// of the provider-native header (e.g. x-goog-api-key for Gemini).
	EmbeddingBearerAuth bool `json:"embedding_bearer_auth,omitempty"`
}

// DiscoverEmbeddingModelsAPI handles POST /api/v1/rag/discover-embedding-models.
// It looks up the given provider's config and calls the upstream model listing
// API, returning only embedding-capable models.
func (s *Server) DiscoverEmbeddingModelsAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req discoverEmbeddingModelsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.EmbeddingProvider == "" {
		httpResponse(w, "embedding_provider is required", http.StatusBadRequest)
		return
	}

	// Look up provider config from store.
	rec, err := s.store.GetProvider(r.Context(), req.EmbeddingProvider)
	if err != nil {
		slog.Error("discover embedding models: lookup provider failed", "key", req.EmbeddingProvider, "error", err)
		httpResponse(w, fmt.Sprintf("failed to lookup provider: %v", err), http.StatusInternalServerError)
		return
	}
	if rec == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", req.EmbeddingProvider), http.StatusNotFound)
		return
	}

	cfg := rec.Config

	// If an explicit embedding URL is provided, use its origin (scheme+host)
	// as the base URL for model discovery instead of the provider's base URL.
	if req.EmbeddingURL != "" {
		if base, err := extractBaseURL(req.EmbeddingURL); err == nil {
			cfg.BaseURL = base
		}
	}

	// Determine the effective API type.
	apiType := strings.ToLower(req.EmbeddingAPIType)
	if apiType == "" {
		apiType = strings.ToLower(cfg.Type)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	var models []string

	switch apiType {
	case "gemini":
		models, err = discoverGeminiEmbeddingModels(ctx, cfg, req.EmbeddingBearerAuth)
	case "openai":
		// OpenAI /v1/models returns all models; include all since there is no
		// standard method-based filter for embedding models.
		models, err = discoverOpenAIModels(ctx, cfg)
	default:
		httpResponse(w, fmt.Sprintf("embedding model discovery is not supported for API type %q", apiType), http.StatusBadRequest)
		return
	}

	if err != nil {
		slog.Error("discover embedding models failed", "provider", req.EmbeddingProvider, "api_type", apiType, "error", err)
		httpResponse(w, fmt.Sprintf("failed to discover embedding models: %v", err), http.StatusBadGateway)
		return
	}

	httpResponseJSON(w, discoverResponse{Models: models}, http.StatusOK)
}

// ─── Test Embedding ───

// testEmbeddingRequest is the JSON body for POST /api/v1/rag/test-embedding.
type testEmbeddingRequest struct {
	EmbeddingProvider   string `json:"embedding_provider"`
	EmbeddingModel      string `json:"embedding_model,omitempty"`
	EmbeddingURL        string `json:"embedding_url,omitempty"`
	EmbeddingAPIType    string `json:"embedding_api_type,omitempty"`
	EmbeddingBearerAuth bool   `json:"embedding_bearer_auth,omitempty"`
}

// testEmbeddingResponse is returned by the test-embedding endpoint on success.
type testEmbeddingResponse struct {
	Success    bool   `json:"success"`
	Model      string `json:"model,omitempty"`
	Dimensions int    `json:"dimensions"`
}

// TestEmbeddingAPI handles POST /api/v1/rag/test-embedding.
// It creates a temporary embedder from the provided config and sends a single
// test embedding request to validate the configuration works.
func (s *Server) TestEmbeddingAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req testEmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.EmbeddingProvider == "" {
		httpResponse(w, "embedding_provider is required", http.StatusBadRequest)
		return
	}

	if req.EmbeddingModel == "" && req.EmbeddingURL == "" {
		httpResponse(w, "embedding_model or embedding_url is required", http.StatusBadRequest)
		return
	}

	// Look up provider config from store.
	rec, err := s.store.GetProvider(r.Context(), req.EmbeddingProvider)
	if err != nil {
		slog.Error("test embedding: lookup provider failed", "key", req.EmbeddingProvider, "error", err)
		httpResponse(w, fmt.Sprintf("failed to lookup provider: %v", err), http.StatusInternalServerError)
		return
	}
	if rec == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", req.EmbeddingProvider), http.StatusNotFound)
		return
	}

	cfg := rec.Config

	client, err := rag.NewATEmbedderClient(rag.ATEmbedderConfig{
		BaseURL:            cfg.BaseURL,
		EmbeddingURL:       req.EmbeddingURL,
		APIType:            req.EmbeddingAPIType,
		Model:              req.EmbeddingModel,
		APIKey:             cfg.APIKey,
		BearerAuth:         req.EmbeddingBearerAuth,
		Proxy:              cfg.Proxy,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	})
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid embedding config: %v", err), http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	// Send a minimal test embedding request.
	embeddings, err := client.CreateEmbedding(ctx, []string{"test"})
	if err != nil {
		slog.Error("test embedding failed", "provider", req.EmbeddingProvider, "model", req.EmbeddingModel, "error", err)
		httpResponse(w, fmt.Sprintf("embedding test failed: %v", err), http.StatusBadGateway)
		return
	}

	dimensions := 0
	if len(embeddings) > 0 {
		dimensions = len(embeddings[0])
	}

	httpResponseJSON(w, testEmbeddingResponse{
		Success:    true,
		Model:      req.EmbeddingModel,
		Dimensions: dimensions,
	}, http.StatusOK)
}

// extractBaseURL extracts the base URL from an embedding endpoint URL by
// stripping known API path suffixes. This handles URLs with path prefixes
// (e.g. behind a gateway proxy).
//
// Examples:
//
//	"https://generativelanguage.googleapis.com/v1beta/models/text-embedding-004:batchEmbedContents"
//	  → "https://generativelanguage.googleapis.com"
//
//	"https://proxy.example.com/at/gateway/proxy/google-ai/v1beta/models/text-embedding-005:batchEmbedContents"
//	  → "https://proxy.example.com/at/gateway/proxy/google-ai"
//
//	"https://api.openai.com/v1/embeddings"
//	  → "https://api.openai.com/v1/embeddings"  (returned as-is, discoverOpenAIModels handles derivation)
func extractBaseURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("missing scheme or host in %q", rawURL)
	}

	path := u.Path

	// Gemini pattern: strip from "/v1beta/models/..." or "/v1/models/..."
	for _, prefix := range []string{"/v1beta/models", "/v1/models"} {
		if idx := strings.Index(path, prefix); idx != -1 {
			u.Path = strings.TrimSuffix(path[:idx], "/")
			u.RawQuery = ""
			u.Fragment = ""

			return u.String(), nil
		}
	}

	// OpenAI pattern: strip "/v1/embeddings" suffix
	if idx := strings.Index(path, "/v1/embeddings"); idx != -1 {
		u.Path = path[:idx] + "/v1/chat/completions"
		u.RawQuery = ""
		u.Fragment = ""

		return u.String(), nil
	}

	// Fallback: return the full URL without query/fragment; let the caller
	// deal with path derivation.
	u.RawQuery = ""
	u.Fragment = ""

	return u.String(), nil
}

// ─── RAG MCP Server CRUD ───

// ListRAGMCPServersAPI handles GET /api/v1/rag/mcp-servers.
func (s *Server) ListRAGMCPServersAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragMCPServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.ragMCPServerStore.ListRAGMCPServers(r.Context(), q)
	if err != nil {
		slog.Error("list rag mcp servers failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list rag mcp servers: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.RAGMCPServer]{Data: []service.RAGMCPServer{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetRAGMCPServerAPI handles GET /api/v1/rag/mcp-servers/{id}.
func (s *Server) GetRAGMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragMCPServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp server id is required", http.StatusBadRequest)
		return
	}

	record, err := s.ragMCPServerStore.GetRAGMCPServer(r.Context(), id)
	if err != nil {
		slog.Error("get rag mcp server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get rag mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("rag mcp server %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateRAGMCPServerAPI handles POST /api/v1/rag/mcp-servers.
func (s *Server) CreateRAGMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragMCPServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.RAGMCPServer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.ragMCPServerStore.CreateRAGMCPServer(r.Context(), req)
	if err != nil {
		slog.Error("create rag mcp server failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create rag mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateRAGMCPServerAPI handles PUT /api/v1/rag/mcp-servers/{id}.
func (s *Server) UpdateRAGMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragMCPServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp server id is required", http.StatusBadRequest)
		return
	}

	var req service.RAGMCPServer
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.ragMCPServerStore.UpdateRAGMCPServer(r.Context(), id, req)
	if err != nil {
		slog.Error("update rag mcp server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update rag mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("rag mcp server %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteRAGMCPServerAPI handles DELETE /api/v1/rag/mcp-servers/{id}.
func (s *Server) DeleteRAGMCPServerAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragMCPServerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "mcp server id is required", http.StatusBadRequest)
		return
	}

	if err := s.ragMCPServerStore.DeleteRAGMCPServer(r.Context(), id); err != nil {
		slog.Error("delete rag mcp server failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete rag mcp server: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

// ─── RAG Sync ───

// SyncRAGCollectionAPI handles POST /api/v1/rag/collections/{id}/sync.
// Triggers a git-based sync for a RAG collection that has a git source configured.
// The sync runs asynchronously and returns immediately with status "syncing".
// Pass ?sync=true to block until the sync completes.
func (s *Server) SyncRAGCollectionAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragService == nil {
		httpResponse(w, "rag service not configured", http.StatusServiceUnavailable)
		return
	}

	collectionID := r.PathValue("id")
	if collectionID == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	collection, err := s.ragCollectionStore.GetRAGCollection(r.Context(), collectionID)
	if err != nil {
		slog.Error("sync rag collection: get collection failed", "id", collectionID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get collection: %v", err), http.StatusInternalServerError)
		return
	}
	if collection == nil {
		httpResponse(w, fmt.Sprintf("collection %q not found", collectionID), http.StatusNotFound)
		return
	}

	if collection.Config.GitSource == nil {
		httpResponse(w, "collection has no git source configured", http.StatusBadRequest)
		return
	}

	deps := rag.SyncDeps{
		RAGService: s.ragService,
		PageStore:  s.ragPageStore,
		StateStore: s.ragStateStore,
		VarStore:   s.variableStore,
	}

	syncMode := r.URL.Query().Get("sync") == "true"

	if syncMode {
		result, err := rag.SyncCollection(r.Context(), deps, collection)
		if err != nil {
			slog.Error("sync rag collection failed", "collection_id", collectionID, "error", err)
			httpResponse(w, fmt.Sprintf("sync failed: %v", err), http.StatusInternalServerError)
			return
		}

		httpResponseJSON(w, result, http.StatusOK)
		return
	}

	// Async mode: run in background.
	go func() {
		ctx := context.Background()
		result, err := rag.SyncCollection(ctx, deps, collection)
		if err != nil {
			slog.Error("sync rag collection failed (async)", "collection_id", collectionID, "error", err)
			return
		}
		slog.Info("sync rag collection completed (async)",
			"collection_id", collectionID,
			"files_processed", result.FilesProcessed,
			"chunks_added", result.ChunksAdded,
		)
	}()

	httpResponseJSON(w, map[string]string{
		"status":        "syncing",
		"collection_id": collectionID,
	}, http.StatusAccepted)
}

// ─── RAG Pages CRUD ───

// ListRAGPagesAPI handles GET /api/v1/rag/collections/{id}/pages.
func (s *Server) ListRAGPagesAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragPageStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	collectionID := r.PathValue("id")
	if collectionID == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.ragPageStore.ListRAGPages(r.Context(), collectionID, q)
	if err != nil {
		slog.Error("list rag pages failed", "collection_id", collectionID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list pages: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.RAGPage]{Data: []service.RAGPage{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetRAGPageAPI handles GET /api/v1/rag/pages/{id}.
func (s *Server) GetRAGPageAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragPageStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "page id is required", http.StatusBadRequest)
		return
	}

	record, err := s.ragPageStore.GetRAGPage(r.Context(), id)
	if err != nil {
		slog.Error("get rag page failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get page: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("page %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteRAGPageAPI handles DELETE /api/v1/rag/pages/{id}.
// Also deletes the corresponding chunks from the vector store.
func (s *Server) DeleteRAGPageAPI(w http.ResponseWriter, r *http.Request) {
	if s.ragPageStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "page id is required", http.StatusBadRequest)
		return
	}

	// Look up the page to get source and collection ID for vector store cleanup.
	page, err := s.ragPageStore.GetRAGPage(r.Context(), id)
	if err != nil {
		slog.Error("delete rag page: get page failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get page: %v", err), http.StatusInternalServerError)
		return
	}
	if page == nil {
		httpResponse(w, fmt.Sprintf("page %q not found", id), http.StatusNotFound)
		return
	}

	// Delete chunks from vector store.
	if s.ragService != nil {
		if err := s.ragService.DeleteDocumentsBySource(r.Context(), page.CollectionID, page.Source); err != nil {
			slog.Warn("delete rag page: failed to delete chunks from vector store", "page_id", id, "source", page.Source, "error", err)
		}
	}

	if err := s.ragPageStore.DeleteRAGPage(r.Context(), id); err != nil {
		slog.Error("delete rag page failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete page: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── RAG Collection Triggers ───

// ListRAGTriggersAPI handles GET /api/v1/rag/collections/{id}/triggers.
// Returns all triggers whose target_type is "rag_sync" and target_id matches the collection.
func (s *Server) ListRAGTriggersAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	collectionID := r.PathValue("id")
	if collectionID == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	allTriggers, err := s.triggerStore.ListAllTriggers(r.Context())
	if err != nil {
		slog.Error("list rag triggers failed", "collection_id", collectionID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list triggers: %v", err), http.StatusInternalServerError)
		return
	}

	// Filter to rag_sync triggers for this collection.
	var filtered []service.Trigger
	for _, t := range allTriggers {
		if t.TargetType == service.TriggerTargetRAGSync && t.TargetID == collectionID {
			filtered = append(filtered, t)
		}
	}
	if filtered == nil {
		filtered = []service.Trigger{}
	}

	httpResponseJSON(w, triggersResponse{Triggers: filtered}, http.StatusOK)
}

// CreateRAGTriggerAPI handles POST /api/v1/rag/collections/{id}/triggers.
// Creates a trigger with target_type=rag_sync and target_id=collection ID.
func (s *Server) CreateRAGTriggerAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	collectionID := r.PathValue("id")
	if collectionID == "" {
		httpResponse(w, "collection id is required", http.StatusBadRequest)
		return
	}

	// Verify the collection exists.
	if s.ragCollectionStore != nil {
		col, err := s.ragCollectionStore.GetRAGCollection(r.Context(), collectionID)
		if err != nil {
			slog.Error("create rag trigger: get collection failed", "collection_id", collectionID, "error", err)
			httpResponse(w, fmt.Sprintf("failed to get collection: %v", err), http.StatusInternalServerError)
			return
		}
		if col == nil {
			httpResponse(w, fmt.Sprintf("collection %q not found", collectionID), http.StatusNotFound)
			return
		}
	}

	var req service.Trigger
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Type != "http" && req.Type != "cron" {
		httpResponse(w, "type must be 'http' or 'cron'", http.StatusBadRequest)
		return
	}

	if req.Type == "cron" {
		schedule, _ := req.Config["schedule"].(string)
		if schedule == "" {
			httpResponse(w, "cron trigger requires 'schedule' in config", http.StatusBadRequest)
			return
		}
	}

	// Validate alias uniqueness.
	if req.Alias != "" {
		existing, err := s.triggerStore.GetTriggerByAlias(r.Context(), req.Alias)
		if err != nil {
			slog.Error("create rag trigger: check alias uniqueness failed", "alias", req.Alias, "error", err)
			httpResponse(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing != nil {
			httpResponse(w, fmt.Sprintf("alias %q is already in use", req.Alias), http.StatusConflict)
			return
		}
	}

	userEmail := s.getUserEmail(r)
	req.TargetType = service.TriggerTargetRAGSync
	req.TargetID = collectionID
	req.WorkflowID = "" // Not a workflow trigger.
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.triggerStore.CreateTrigger(r.Context(), req)
	if err != nil {
		slog.Error("create rag trigger failed", "collection_id", collectionID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create trigger: %v", err), http.StatusInternalServerError)
		return
	}

	// Reload scheduler if it's a cron trigger.
	if req.Type == "cron" && req.Enabled && s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Error("scheduler reload failed after rag trigger create", "error", err)
		}
	}

	httpResponseJSON(w, record, http.StatusCreated)
}
