package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	Scope                string   `json:"scope,omitempty"`
	Name                 string   `json:"name"`
	AllowedProvidersMode string   `json:"allowed_providers_mode,omitempty"` // "all" (default/""), "none", or "list"
	AllowedProviders     []string `json:"allowed_providers,omitempty"`      // used when mode = "list"
	AllowedModelsMode    string   `json:"allowed_models_mode,omitempty"`    // "all" (default/""), "none", or "list"
	AllowedModels        []string `json:"allowed_models,omitempty"`         // used when mode = "list"
	AllowedWebhooksMode  string   `json:"allowed_webhooks_mode,omitempty"`  // "all" (default/""), "none", or "list"
	AllowedWebhooks      []string `json:"allowed_webhooks,omitempty"`       // used when mode = "list"
	AllowedMCPsMode      string   `json:"allowed_mcps_mode,omitempty"`      // "all" (default/""), "none", or "list"
	AllowedMCPs          []string `json:"allowed_mcps,omitempty"`           // used when mode = "list" (gateway MCP server names)
	LegacyRAGMCPsMode    string   `json:"allowed_rag_mcps_mode,omitempty"`  // deprecated alias for allowed_mcps_mode
	LegacyRAGMCPs        []string `json:"allowed_rag_mcps,omitempty"`       // deprecated alias for allowed_mcps
	ExpiresAt            *string  `json:"expires_at,omitempty"`             // RFC3339 timestamp, nil/empty = no expiry
	TotalTokenLimit      *int64   `json:"total_token_limit,omitempty"`      // max total tokens; nil = unlimited
	SpendLimitCents      *float64 `json:"spend_limit_cents,omitempty"`      // max spend in cents; nil = unlimited
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
	AllowedMCPsMode      string   `json:"allowed_mcps_mode,omitempty"`      // "all" (default/""), "none", or "list"
	AllowedMCPs          []string `json:"allowed_mcps,omitempty"`           // used when mode = "list" (gateway MCP server names)
	LegacyRAGMCPsMode    string   `json:"allowed_rag_mcps_mode,omitempty"`  // deprecated alias for allowed_mcps_mode
	LegacyRAGMCPs        []string `json:"allowed_rag_mcps,omitempty"`       // deprecated alias for allowed_mcps
	ExpiresAt            *string  `json:"expires_at,omitempty"`             // RFC3339 timestamp, nil/empty = no expiry
	TotalTokenLimit      *int64   `json:"total_token_limit,omitempty"`      // max total tokens; nil = unlimited
	SpendLimitCents      *float64 `json:"spend_limit_cents,omitempty"`      // max spend in cents; nil = unlimited
	LimitResetInterval   *string  `json:"limit_reset_interval,omitempty"`   // duration string (e.g. "24h", "7d", "30d"), or nil = manual
}

// createTokenResponse is returned once on creation or rotation (the only times
// the full token is shown).
type createTokenResponse struct {
	Token string           `json:"token"` // full token — shown only once
	Info  service.APIToken `json:"info"`
}

// generateAPITokenSecret mints a gateway bearer token and the values stored for
// it. Three invariants are security-critical and must hold for every caller:
//
//  1. Format is "at_" + hex(32 crypto/rand bytes) = 67 chars.
//  2. Only sha256(plaintext) hex-encoded reaches the database, so a database
//     compromise cannot replay gateway calls.
//  3. The returned plaintext is the only copy; it can never be recovered.
//
// It returns (plaintext, hash, prefix). The prefix is the display-safe first 8
// chars ("at_" + 5 hex), which is not enough to guess the remaining 59.
func generateAPITokenSecret() (string, string, string, error) {
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", "", "", fmt.Errorf("generate token: %w", err)
	}
	fullToken := "at_" + hex.EncodeToString(rawBytes)
	hash := sha256.Sum256([]byte(fullToken))
	return fullToken, hex.EncodeToString(hash[:]), fullToken[:8], nil
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
	if req.Scope != "" && req.Scope != "workspace" && req.Scope != "personal" {
		httpResponse(w, "scope must be workspace or personal", http.StatusBadRequest)
		return
	}
	ownerUserID := ""
	if req.Scope == "personal" {
		principal, ok := service.AccessPrincipalFromContext(r.Context())
		if !ok || principal.UserID == "" {
			workspaceError(w, service.ErrAccessDenied)
			return
		}
		ownerUserID = principal.UserID
	}

	// Backward compat: fold legacy allowed_rag_mcps* fields into allowed_mcps*.
	if req.AllowedMCPsMode == "" && req.LegacyRAGMCPsMode != "" {
		req.AllowedMCPsMode = req.LegacyRAGMCPsMode
	}
	if len(req.AllowedMCPs) == 0 && len(req.LegacyRAGMCPs) > 0 {
		req.AllowedMCPs = req.LegacyRAGMCPs
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	fullToken, tokenHash, tokenPrefix, err := generateAPITokenSecret()
	if err != nil {
		slog.Error("generate api token failed", "error", err)
		httpResponse(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

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
		OwnerUserID:          ownerUserID,
		Name:                 req.Name,
		TokenPrefix:          tokenPrefix,
		AllowedProvidersMode: req.AllowedProvidersMode,
		AllowedProviders:     req.AllowedProviders,
		AllowedModelsMode:    req.AllowedModelsMode,
		AllowedModels:        req.AllowedModels,
		AllowedWebhooksMode:  req.AllowedWebhooksMode,
		AllowedWebhooks:      req.AllowedWebhooks,
		AllowedMCPsMode:      req.AllowedMCPsMode,
		AllowedMCPs:          req.AllowedMCPs,
		ExpiresAt:            expiresAt,
		TotalTokenLimit:      toNullInt64(req.TotalTokenLimit),
		SpendLimitCents:      toNullFloat64(req.SpendLimitCents),
		LimitResetInterval:   toNullString(req.LimitResetInterval),
		CreatedBy:            userEmail,
		UpdatedBy:            userEmail,
	}

	created, err := s.tokenStore.CreateAPIToken(r.Context(), token, tokenHash)
	if err != nil {
		slog.Error("create api token failed", "error", err)
		if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrAccessResourceNotFound) {
			workspaceError(w, err)
			return
		}
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
		if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrAccessResourceNotFound) {
			workspaceError(w, err)
			return
		}
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

	// Backward compat: fold legacy allowed_rag_mcps* fields into allowed_mcps*.
	if req.AllowedMCPsMode == "" && req.LegacyRAGMCPsMode != "" {
		req.AllowedMCPsMode = req.LegacyRAGMCPsMode
	}
	if len(req.AllowedMCPs) == 0 && len(req.LegacyRAGMCPs) > 0 {
		req.AllowedMCPs = req.LegacyRAGMCPs
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
		AllowedMCPsMode:      req.AllowedMCPsMode,
		AllowedMCPs:          req.AllowedMCPs,
		ExpiresAt:            expiresAt,
		TotalTokenLimit:      toNullInt64(req.TotalTokenLimit),
		SpendLimitCents:      toNullFloat64(req.SpendLimitCents),
		LimitResetInterval:   toNullString(req.LimitResetInterval),
		UpdatedBy:            userEmail,
	}

	updated, err := s.tokenStore.UpdateAPIToken(r.Context(), id, token)
	if err != nil {
		slog.Error("update api token failed", "id", id, "error", err)
		if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrAccessResourceNotFound) {
			workspaceError(w, err)
			return
		}
		if strings.Contains(err.Error(), "not found") {
			httpResponse(w, "token not found", http.StatusNotFound)
			return
		}
		httpResponse(w, fmt.Sprintf("failed to update token: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, updated, http.StatusOK)
}

// SetAPITokenPausedAPI changes token availability without changing its settings.
func (s *Server) SetAPITokenPausedAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tokenStore.(service.APITokenPauseStorer)
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "token id is required", http.StatusBadRequest)
		return
	}
	var req struct {
		Paused *bool `json:"paused"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Paused == nil {
		httpResponse(w, "paused must be a boolean", http.StatusBadRequest)
		return
	}
	if err := store.SetAPITokenPaused(r.Context(), id, *req.Paused, s.getUserEmail(r)); err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]bool{"paused": *req.Paused}, http.StatusOK)
}

// RotateAPITokenAPI handles POST /api/v1/api-tokens/:id/rotate.
//
// Rotation replaces the secret of an existing token instead of forcing the
// delete-and-recreate dance, which loses the token's id, its restrictions and
// its accumulated usage, and breaks every reference to it. The id, settings,
// limits and usage counters survive; only the credential changes.
//
// The previous secret is invalidated the instant this commits — gateway
// authentication resolves the bearer by hash on every request and caches
// nothing — so callers must be updated before or immediately after rotating.
// Requests already admitted keep running; new ones with the old secret get 401.
// Like creation, the plaintext is returned exactly once and cannot be recovered.
func (s *Server) RotateAPITokenAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tokenStore.(service.APITokenRotateStorer)
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "token id is required", http.StatusBadRequest)
		return
	}

	fullToken, tokenHash, tokenPrefix, err := generateAPITokenSecret()
	if err != nil {
		slog.Error("generate api token failed", "id", id, "error", err)
		httpResponse(w, "failed to generate token", http.StatusInternalServerError)
		return
	}

	rotated, err := store.RotateAPIToken(r.Context(), id, tokenHash, tokenPrefix, s.getUserEmail(r))
	if err != nil {
		slog.Error("rotate api token failed", "id", id, "error", err)
		workspaceError(w, err)
		return
	}

	// The store cleared last_used_at for the new secret. Drop the in-memory
	// write throttle too, otherwise the first use of the rotated token would be
	// suppressed for up to tokenLastUsedThreshold and the UI would keep showing
	// "never used" while the credential is demonstrably working.
	s.tokenLastUsed.Delete(id)

	httpResponseJSON(w, createTokenResponse{
		Token: fullToken,
		Info:  *rotated,
	}, http.StatusOK)
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

	if err := s.authorizeTokenManagement(r.Context(), id, "tokens.read"); err != nil {
		workspaceError(w, err)
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

	if err := s.authorizeTokenManagement(r.Context(), id, "tokens.write"); err != nil {
		workspaceError(w, err)
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

func toNullFloat64(v *float64) types.Null[float64] {
	if v == nil {
		return types.Null[float64]{}
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
