package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/rag"
	"github.com/rakunlabs/query"
)

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

	if req.VectorStore.Type == "" {
		httpResponse(w, "vector_store.type is required", http.StatusBadRequest)
		return
	}

	if req.EmbeddingProvider == "" {
		httpResponse(w, "embedding_provider is required", http.StatusBadRequest)
		return
	}

	if req.EmbeddingModel == "" {
		httpResponse(w, "embedding_model is required", http.StatusBadRequest)
		return
	}

	// Set defaults.
	if req.ChunkSize <= 0 {
		req.ChunkSize = 512
	}
	if req.ChunkOverlap < 0 {
		req.ChunkOverlap = 100
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

	if req.VectorStore.Type == "" {
		httpResponse(w, "vector_store.type is required", http.StatusBadRequest)
		return
	}

	if req.EmbeddingProvider == "" {
		httpResponse(w, "embedding_provider is required", http.StatusBadRequest)
		return
	}

	if req.EmbeddingModel == "" {
		httpResponse(w, "embedding_model is required", http.StatusBadRequest)
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

	result, err := s.ragService.Ingest(r.Context(), collectionID, content, contentType, source, nil)
	if err != nil {
		slog.Error("rag ingest failed", "collection_id", collectionID, "source", source, "error", err)
		httpResponse(w, fmt.Sprintf("ingest failed: %v", err), http.StatusInternalServerError)
		return
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

	result, err := s.ragService.Ingest(r.Context(), collectionID, resp.Body, contentType, req.URL, nil)
	if err != nil {
		slog.Error("rag url import failed", "collection_id", collectionID, "url", req.URL, "error", err)
		httpResponse(w, fmt.Sprintf("import failed: %v", err), http.StatusInternalServerError)
		return
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
