package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	str2duration "github.com/xhit/go-str2duration/v2"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── API Token Management ───

// createTokenRequest is the JSON body for POST /api/v1/api-tokens.
type createTokenRequest struct {
	Name                 string   `json:"name"`
	AllowedProvidersMode string   `json:"allowed_providers_mode,omitempty"` // "all" (default/""), "none", or "list"
	AllowedProviders     []string `json:"allowed_providers,omitempty"`      // used when mode = "list"
	AllowedModelsMode    string   `json:"allowed_models_mode,omitempty"`    // "all" (default/""), "none", or "list"
	AllowedModels        []string `json:"allowed_models,omitempty"`         // used when mode = "list"
	AllowedWebhooksMode  string   `json:"allowed_webhooks_mode,omitempty"`  // "all" (default/""), "none", or "list"
	AllowedWebhooks      []string `json:"allowed_webhooks,omitempty"`       // used when mode = "list"
	AllowedRAGMCPsMode   string   `json:"allowed_rag_mcps_mode,omitempty"`  // "all" (default/""), "none", or "list"
	AllowedRAGMCPs       []string `json:"allowed_rag_mcps,omitempty"`       // used when mode = "list"
	ExpiresAt            *string  `json:"expires_at,omitempty"`             // RFC3339 timestamp, nil/empty = no expiry
	TotalTokenLimit      *int64   `json:"total_token_limit,omitempty"`      // max total tokens; nil = unlimited
	LimitResetInterval   *string  `json:"limit_reset_interval,omitempty"`   // duration string (e.g. "24h", "7d", "30d"), or nil = manual
}

// updateTokenRequest is the JSON body for PUT /api/v1/api-tokens/{id}.
type updateTokenRequest struct {
	Name                 string   `json:"name"`
	AllowedProvidersMode string   `json:"allowed_providers_mode,omitempty"` // "all" (default/""), "none", or "list"
	AllowedProviders     []string `json:"allowed_providers,omitempty"`      // used when mode = "list"
	AllowedModelsMode    string   `json:"allowed_models_mode,omitempty"`    // "all" (default/""), "none", or "list"
	AllowedModels        []string `json:"allowed_models,omitempty"`         // used when mode = "list"
	AllowedWebhooksMode  string   `json:"allowed_webhooks_mode,omitempty"`  // "all" (default/""), "none", or "list"
	AllowedWebhooks      []string `json:"allowed_webhooks,omitempty"`       // used when mode = "list"
	AllowedRAGMCPsMode   string   `json:"allowed_rag_mcps_mode,omitempty"`  // "all" (default/""), "none", or "list"
	AllowedRAGMCPs       []string `json:"allowed_rag_mcps,omitempty"`       // used when mode = "list"
	ExpiresAt            *string  `json:"expires_at,omitempty"`             // RFC3339 timestamp, nil/empty = no expiry
	TotalTokenLimit      *int64   `json:"total_token_limit,omitempty"`      // max total tokens; nil = unlimited
	LimitResetInterval   *string  `json:"limit_reset_interval,omitempty"`   // duration string (e.g. "24h", "7d", "30d"), or nil = manual
}

// createTokenResponse is returned once on creation (the only time the full token is shown).
type createTokenResponse struct {
	Token string           `json:"token"` // full token — shown only once
	Info  service.APIToken `json:"info"`
}

// apiTokensResponse wraps a list of tokens for JSON output.
type apiTokensResponse struct {
	Tokens []service.APIToken `json:"tokens"`
}

// ListAPITokensAPI handles GET /api/v1/api-tokens.
func (s *Server) ListAPITokensAPI(w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	tokens, err := s.tokenStore.ListAPITokens(r.Context(), q)
	if err != nil {
		slog.Error("list api tokens failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list tokens: %v", err), http.StatusInternalServerError)
		return
	}

	if tokens == nil {
		tokens = &service.ListResult[service.APIToken]{Data: []service.APIToken{}}
	}

	httpResponseJSON(w, tokens, http.StatusOK)
}

// CreateAPITokenAPI handles POST /api/v1/api-tokens.
// Returns the full token exactly once.
func (s *Server) CreateAPITokenAPI(w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req createTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	// Generate token: at_ + 32 random bytes hex-encoded = at_ + 64 hex chars.
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		httpResponse(w, "failed to generate token", http.StatusInternalServerError)
		return
	}
	fullToken := "at_" + hex.EncodeToString(rawBytes)

	// Hash for storage.
	hash := sha256.Sum256([]byte(fullToken))
	tokenHash := hex.EncodeToString(hash[:])

	// Prefix for display (first 8 chars of full token = "at_xxxxx").
	tokenPrefix := fullToken[:8]

	// Compute expiry.
	var expiresAt types.Null[types.Time]
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			httpResponse(w, fmt.Sprintf("invalid expires_at: %v", err), http.StatusBadRequest)
			return
		}
		expiresAt = types.NewTimeNull(t.UTC())
	}

	// Validate reset interval if provided.
	if req.LimitResetInterval != nil && *req.LimitResetInterval != "" {
		if _, err := str2duration.ParseDuration(*req.LimitResetInterval); err != nil {
			httpResponse(w, fmt.Sprintf("invalid limit_reset_interval %q: %v", *req.LimitResetInterval, err), http.StatusBadRequest)
			return
		}
	}

	userEmail := s.getUserEmail(r)
	token := service.APIToken{
		Name:                 req.Name,
		TokenPrefix:          tokenPrefix,
		AllowedProvidersMode: req.AllowedProvidersMode,
		AllowedProviders:     req.AllowedProviders,
		AllowedModelsMode:    req.AllowedModelsMode,
		AllowedModels:        req.AllowedModels,
		AllowedWebhooksMode:  req.AllowedWebhooksMode,
		AllowedWebhooks:      req.AllowedWebhooks,
		AllowedRAGMCPsMode:   req.AllowedRAGMCPsMode,
		AllowedRAGMCPs:       req.AllowedRAGMCPs,
		ExpiresAt:            expiresAt,
		TotalTokenLimit:      toNullInt64(req.TotalTokenLimit),
		LimitResetInterval:   toNullString(req.LimitResetInterval),
		CreatedBy:            userEmail,
		UpdatedBy:            userEmail,
	}

	created, err := s.tokenStore.CreateAPIToken(r.Context(), token, tokenHash)
	if err != nil {
		slog.Error("create api token failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create token: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, createTokenResponse{
		Token: fullToken,
		Info:  *created,
	}, http.StatusCreated)
}

// DeleteAPITokenAPI handles DELETE /api/v1/api-tokens/:id.
func (s *Server) DeleteAPITokenAPI(w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "token id is required", http.StatusBadRequest)
		return
	}

	if err := s.tokenStore.DeleteAPIToken(r.Context(), id); err != nil {
		slog.Error("delete api token failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete token: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// UpdateAPITokenAPI handles PUT /api/v1/api-tokens/:id.
func (s *Server) UpdateAPITokenAPI(w http.ResponseWriter, r *http.Request) {
	if s.tokenStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "token id is required", http.StatusBadRequest)
		return
	}

	var req updateTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	var expiresAt types.Null[types.Time]
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			httpResponse(w, fmt.Sprintf("invalid expires_at: %v", err), http.StatusBadRequest)
			return
		}
		expiresAt = types.NewTimeNull(t.UTC())
	}

	// Validate reset interval if provided.
	if req.LimitResetInterval != nil && *req.LimitResetInterval != "" {
		if _, err := str2duration.ParseDuration(*req.LimitResetInterval); err != nil {
			httpResponse(w, fmt.Sprintf("invalid limit_reset_interval %q: %v", *req.LimitResetInterval, err), http.StatusBadRequest)
			return
		}
	}

	userEmail := s.getUserEmail(r)
	token := service.APIToken{
		Name:                 req.Name,
		AllowedProvidersMode: req.AllowedProvidersMode,
		AllowedProviders:     req.AllowedProviders,
		AllowedModelsMode:    req.AllowedModelsMode,
		AllowedModels:        req.AllowedModels,
		AllowedWebhooksMode:  req.AllowedWebhooksMode,
		AllowedWebhooks:      req.AllowedWebhooks,
		AllowedRAGMCPsMode:   req.AllowedRAGMCPsMode,
		AllowedRAGMCPs:       req.AllowedRAGMCPs,
		ExpiresAt:            expiresAt,
		TotalTokenLimit:      toNullInt64(req.TotalTokenLimit),
		LimitResetInterval:   toNullString(req.LimitResetInterval),
		UpdatedBy:            userEmail,
	}

	updated, err := s.tokenStore.UpdateAPIToken(r.Context(), id, token)
	if err != nil {
		slog.Error("update api token failed", "id", id, "error", err)
		if strings.Contains(err.Error(), "not found") {
			httpResponse(w, "token not found", http.StatusNotFound)
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update token: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, updated, http.StatusOK)
}

// GetTokenUsageAPI handles GET /api/v1/api-tokens/:id/usage.
func (s *Server) GetTokenUsageAPI(w http.ResponseWriter, r *http.Request) {
	if s.tokenUsageStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "token id is required", http.StatusBadRequest)
		return
	}

	usage, err := s.tokenUsageStore.GetTokenUsage(r.Context(), id)
	if err != nil {
		slog.Error("get token usage failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get token usage: %v", err), http.StatusInternalServerError)
		return
	}

	if usage == nil {
		usage = []service.TokenUsage{}
	}

	httpResponseJSON(w, usage, http.StatusOK)
}

// ResetTokenUsageAPI handles POST /api/v1/api-tokens/:id/usage/reset.
func (s *Server) ResetTokenUsageAPI(w http.ResponseWriter, r *http.Request) {
	if s.tokenUsageStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "token id is required", http.StatusBadRequest)
		return
	}

	if err := s.tokenUsageStore.ResetTokenUsage(r.Context(), id); err != nil {
		slog.Error("reset token usage failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to reset token usage: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "usage reset", http.StatusOK)
}

// toNullInt64 converts a *int64 to types.Null[int64].
func toNullInt64(v *int64) types.Null[int64] {
	if v == nil {
		return types.Null[int64]{}
	}
	return types.NewNull(*v)
}

// toNullString converts a *string to types.Null[string].
func toNullString(v *string) types.Null[string] {
	if v == nil {
		return types.Null[string]{}
	}
	return types.NewNull(*v)
}
