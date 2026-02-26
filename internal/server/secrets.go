package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Secret CRUD API ───

// secretsResponse wraps a list of secret records for JSON output.
type secretsResponse struct {
	Secrets []service.Secret `json:"secrets"`
}

// ListSecretsAPI handles GET /api/v1/secrets.
// Values are redacted in the response for security.
func (s *Server) ListSecretsAPI(w http.ResponseWriter, r *http.Request) {
	if s.secretStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	records, err := s.secretStore.ListSecrets(r.Context())
	if err != nil {
		slog.Error("list secrets failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list secrets: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Secret{}
	}

	// Redact values in list responses.
	for i := range records {
		records[i].Value = "***"
	}

	httpResponseJSON(w, secretsResponse{Secrets: records}, http.StatusOK)
}

// GetSecretAPI handles GET /api/v1/secrets/:id.
// The value is returned in full (not redacted) for single-secret retrieval.
func (s *Server) GetSecretAPI(w http.ResponseWriter, r *http.Request) {
	if s.secretStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := extractSecretID(r)
	if id == "" {
		httpResponse(w, "secret id is required", http.StatusBadRequest)
		return
	}

	record, err := s.secretStore.GetSecret(r.Context(), id)
	if err != nil {
		slog.Error("get secret failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get secret: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("secret %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateSecretAPI handles POST /api/v1/secrets.
func (s *Server) CreateSecretAPI(w http.ResponseWriter, r *http.Request) {
	if s.secretStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Secret
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	if req.Value == "" {
		httpResponse(w, "value is required", http.StatusBadRequest)
		return
	}

	record, err := s.secretStore.CreateSecret(r.Context(), req)
	if err != nil {
		slog.Error("create secret failed", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create secret: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateSecretAPI handles PUT /api/v1/secrets/:id.
func (s *Server) UpdateSecretAPI(w http.ResponseWriter, r *http.Request) {
	if s.secretStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := extractSecretID(r)
	if id == "" {
		httpResponse(w, "secret id is required", http.StatusBadRequest)
		return
	}

	var req service.Secret
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	record, err := s.secretStore.UpdateSecret(r.Context(), id, req)
	if err != nil {
		slog.Error("update secret failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update secret: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("secret %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteSecretAPI handles DELETE /api/v1/secrets/:id.
func (s *Server) DeleteSecretAPI(w http.ResponseWriter, r *http.Request) {
	if s.secretStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := extractSecretID(r)
	if id == "" {
		httpResponse(w, "secret id is required", http.StatusBadRequest)
		return
	}

	if err := s.secretStore.DeleteSecret(r.Context(), id); err != nil {
		slog.Error("delete secret failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete secret: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Helpers ───

// extractSecretID extracts the secret ID from the URL path.
// Expected path: /api/v1/secrets/{id}
func extractSecretID(r *http.Request) string {
	path := r.URL.Path
	const prefix = "/api/v1/secrets/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}

	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimSuffix(id, "/")

	return id
}
