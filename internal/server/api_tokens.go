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

	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

// ─── API Token Management ───

// createTokenRequest is the JSON body for POST /api/v1/api-tokens.
type createTokenRequest struct {
	Name             string   `json:"name"`
	AllowedProviders []string `json:"allowed_providers,omitempty"` // nil = all
	AllowedModels    []string `json:"allowed_models,omitempty"`    // nil = all
	ExpiresIn        *int     `json:"expires_in,omitempty"`        // seconds from now, nil = no expiry
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

	tokens, err := s.tokenStore.ListAPITokens(r.Context())
	if err != nil {
		slog.Error("list api tokens failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list tokens: %v", err), http.StatusInternalServerError)
		return
	}

	if tokens == nil {
		tokens = []service.APIToken{}
	}

	httpResponseJSON(w, apiTokensResponse{Tokens: tokens}, http.StatusOK)
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
	if req.ExpiresIn != nil && *req.ExpiresIn > 0 {
		t := time.Now().UTC().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = types.NewTimeNull(t)
	}

	token := service.APIToken{
		Name:             req.Name,
		TokenPrefix:      tokenPrefix,
		AllowedProviders: req.AllowedProviders,
		AllowedModels:    req.AllowedModels,
		ExpiresAt:        expiresAt,
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

	id := extractAPITokenID(r)
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

// ─── Helpers ───

// extractAPITokenID extracts the token ID from the URL path.
// Expected path: /api/v1/api-tokens/{id}
func extractAPITokenID(r *http.Request) string {
	path := r.URL.Path
	const prefix = "/api/v1/api-tokens/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}

	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimSuffix(id, "/")

	return id
}
