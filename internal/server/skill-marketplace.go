package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Remote marketplace source CRUD ───

func (s *Server) ListMarketplaceSourcesAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	sources, err := s.marketplaceSourceStore.ListMarketplaceSources(r.Context())
	if err != nil {
		slog.Error("list marketplace sources failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list marketplace sources: %v", err), http.StatusInternalServerError)
		return
	}

	if sources == nil {
		sources = []service.MarketplaceSource{}
	}

	httpResponseJSON(w, sources, http.StatusOK)
}

func (s *Server) CreateMarketplaceSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.MarketplaceSource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if req.Type == service.MarketplaceSourceGeneric {
		httpResponse(w, "generic search sources are no longer supported; add a marketplace.json URL or repository", http.StatusBadRequest)
		return
	}
	if err := normalizeMarketplaceSource(&req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	record, err := s.marketplaceSourceStore.CreateMarketplaceSource(r.Context(), req)
	if err != nil {
		slog.Error("create marketplace source failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create marketplace source: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

func (s *Server) UpdateMarketplaceSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "id is required", http.StatusBadRequest)
		return
	}

	var req service.MarketplaceSource
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if err := normalizeMarketplaceSource(&req); err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	record, err := s.marketplaceSourceStore.UpdateMarketplaceSource(r.Context(), id, req)
	if err != nil {
		slog.Error("update marketplace source failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update marketplace source: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("marketplace source %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

func (s *Server) DeleteMarketplaceSourceAPI(w http.ResponseWriter, r *http.Request) {
	if s.marketplaceSourceStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "id is required", http.StatusBadRequest)
		return
	}

	if err := s.marketplaceSourceStore.DeleteMarketplaceSource(r.Context(), id); err != nil {
		slog.Error("delete marketplace source failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete marketplace source: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
