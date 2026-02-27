package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Info API ───

// infoResponse is returned by GET /api/v1/info.
type infoResponse struct {
	Providers []infoProvider `json:"providers"`
	StoreType string         `json:"store_type"` // "postgres", "sqlite", or "none"
	User      string         `json:"user,omitempty"`
	Version   string         `json:"version"`
}

type infoProvider struct {
	Key          string   `json:"key"`
	Type         string   `json:"type"`
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
}

// InfoAPI handles GET /api/v1/info.
// Returns gateway status: registered providers, model counts, store type.
func (s *Server) InfoAPI(w http.ResponseWriter, r *http.Request) {
	s.providerMu.RLock()
	providerList := make([]infoProvider, 0, len(s.providers))
	for key, info := range s.providers {
		models := info.models
		if models == nil {
			models = []string{}
		}
		providerList = append(providerList, infoProvider{
			Key:          key,
			Type:         info.providerType,
			DefaultModel: info.defaultModel,
			Models:       models,
		})
	}
	s.providerMu.RUnlock()

	storeType := s.storeType

	httpResponseJSON(w, infoResponse{
		Providers: providerList,
		StoreType: storeType,
		User:      s.getUserEmail(r),
		Version:   s.version,
	}, http.StatusOK)
}

// ─── Provider CRUD API ───

// providerRequest is the JSON body for creating/updating a provider.
type providerRequest struct {
	Config config.LLMConfig `json:"config"`
}

// providerResponse wraps a single provider record for JSON output.
type providerResponse struct {
	service.ProviderRecord
}

// providersResponse wraps a list of provider records for JSON output.
type providersResponse struct {
	Providers []service.ProviderRecord `json:"providers"`
}

// ListProvidersAPI handles GET /api/v1/providers.
func (s *Server) ListProvidersAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	records, err := s.store.ListProviders(r.Context())
	if err != nil {
		slog.Error("list providers failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list providers: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.ProviderRecord{}
	}

	// Redact secrets before sending to the client.
	for i := range records {
		redactProviderRecord(&records[i])
	}

	httpResponseJSON(w, providersResponse{Providers: records}, http.StatusOK)
}

// GetProviderAPI handles GET /api/v1/providers/:key.
func (s *Server) GetProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		httpResponse(w, "provider key is required", http.StatusBadRequest)
		return
	}

	record, err := s.store.GetProvider(r.Context(), key)
	if err != nil {
		slog.Error("get provider failed", "key", key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get provider: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", key), http.StatusNotFound)
		return
	}

	// Redact secrets before sending to the client.
	redactProviderRecord(record)

	httpResponseJSON(w, providerResponse{ProviderRecord: *record}, http.StatusOK)
}

// CreateProviderAPI handles POST /api/v1/providers.
func (s *Server) CreateProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Key    string           `json:"key"`
		Config config.LLMConfig `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	if req.Config.Type == "" {
		httpResponse(w, "config.type is required", http.StatusBadRequest)
		return
	}

	// Check if provider already exists.
	existing, err := s.store.GetProvider(r.Context(), req.Key)
	if err != nil {
		slog.Error("check existing provider failed", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to check existing provider: %v", err), http.StatusInternalServerError)
		return
	}
	if existing != nil {
		httpResponse(w, fmt.Sprintf("provider %q already exists", req.Key), http.StatusConflict)
		return
	}

	userEmail := s.getUserEmail(r)
	record, err := s.store.CreateProvider(r.Context(), service.ProviderRecord{
		Key:       req.Key,
		Config:    req.Config,
		CreatedBy: userEmail,
		UpdatedBy: userEmail,
	})
	if err != nil {
		slog.Error("create provider failed", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create provider: %v", err), http.StatusInternalServerError)
		return
	}

	// Hot reload: register the new provider in the live registry.
	if err := s.reloadProvider(req.Key, req.Config); err != nil {
		slog.Warn("provider created in DB but failed to hot-reload", "key", req.Key, "error", err)
	}

	httpResponseJSON(w, providerResponse{ProviderRecord: *record}, http.StatusCreated)
}

// UpdateProviderAPI handles PUT /api/v1/providers/:key.
func (s *Server) UpdateProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		httpResponse(w, "provider key is required", http.StatusBadRequest)
		return
	}

	var req providerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Config.Type == "" {
		httpResponse(w, "config.type is required", http.StatusBadRequest)
		return
	}

	// Preserve the existing api_key when the request doesn't provide one.
	// This prevents the UI (which redacts/hides secrets) from accidentally
	// wiping the stored token on every update.
	if req.Config.APIKey == "" {
		existing, err := s.store.GetProvider(r.Context(), key)
		if err != nil {
			slog.Error("update provider: failed to read existing config", "key", key, "error", err)
			httpResponse(w, fmt.Sprintf("failed to read existing provider: %v", err), http.StatusInternalServerError)
			return
		}
		if existing != nil {
			req.Config.APIKey = existing.Config.APIKey
		}
	}

	userEmail := s.getUserEmail(r)
	record, err := s.store.UpdateProvider(r.Context(), key, service.ProviderRecord{
		Key:       key,
		Config:    req.Config,
		UpdatedBy: userEmail,
	})
	if err != nil {
		slog.Error("update provider failed", "key", key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update provider: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", key), http.StatusNotFound)
		return
	}

	// Hot reload: update the provider in the live registry.
	if err := s.reloadProvider(key, req.Config); err != nil {
		slog.Warn("provider updated in DB but failed to hot-reload", "key", key, "error", err)
	}

	httpResponseJSON(w, providerResponse{ProviderRecord: *record}, http.StatusOK)
}

// DeleteProviderAPI handles DELETE /api/v1/providers/:key.
func (s *Server) DeleteProviderAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	key := r.PathValue("key")
	if key == "" {
		httpResponse(w, "provider key is required", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteProvider(r.Context(), key); err != nil {
		slog.Error("delete provider failed", "key", key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete provider: %v", err), http.StatusInternalServerError)
		return
	}

	// Hot reload: remove the provider from the live registry.
	s.removeProvider(key)

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Helpers ───

// redactProviderRecord replaces secret fields with a sentinel value so the
// UI can tell whether a key is set without exposing the actual secret.
// The sentinel value "***" is recognized by the UI.
func redactProviderRecord(rec *service.ProviderRecord) {
	if rec.Config.APIKey != "" {
		rec.Config.APIKey = "***"
	}
}
