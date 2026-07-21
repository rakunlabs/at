package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/worldline-go/types"
	str2duration "github.com/xhit/go-str2duration/v2"

	"github.com/rakunlabs/at/internal/service"
)

// ─── API Token Tool Executors (Phase 2) ───
//
// Gateway API tokens authenticate inbound /gateway/v1 calls. The
// security-critical invariants from CreateAPITokenAPI are preserved
// verbatim:
//
//   1. Token format: "at_" + hex(32 random bytes) = 67 chars total.
//   2. The plaintext token is returned EXACTLY ONCE in the create
//      response. After that only token_prefix (first 8 chars) is ever
//      exposed.
//   3. Storage uses sha256(plaintext) hex-encoded — the DB never sees
//      the raw token, so a DB compromise can't replay calls.
//
// We deliberately use crypto/rand (not math/rand) for the same reason
// the HTTP handler does: anything else is a security regression.

// optionalString extracts a *string from args[k]. Returns nil when
// the key is absent or maps to nil. This matches the pointer-string
// semantics in createTokenRequest where omitted = "preserve unset".
func optionalString(args map[string]any, k string) *string {
	v, ok := args[k]
	if !ok || v == nil {
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return nil
	}
	return &s
}

// optionalInt64 extracts a *int64 from args[k]. JSON numbers come in
// as float64; we coerce safely.
func optionalInt64(args map[string]any, k string) *int64 {
	v, ok := args[k]
	if !ok || v == nil {
		return nil
	}
	switch x := v.(type) {
	case float64:
		i := int64(x)
		return &i
	case int64:
		return &x
	case int:
		i := int64(x)
		return &i
	}
	return nil
}

func (s *Server) execAPITokenList(ctx context.Context, _ map[string]any) (string, error) {
	if s.tokenStore == nil {
		return "", fmt.Errorf("api token store not configured")
	}
	tokens, err := s.tokenStore.ListAPITokens(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list api tokens: %w", err)
	}
	if tokens == nil {
		tokens = &service.ListResult[service.APIToken]{Data: []service.APIToken{}}
	}
	out, err := json.MarshalIndent(tokens, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal tokens: %w", err)
	}
	return string(out), nil
}

func (s *Server) execAPITokenCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.tokenStore == nil {
		return "", fmt.Errorf("api token store not configured")
	}
	name := stringArg(args, "name")
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	// Generate plaintext token + storage hash.
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	fullToken := "at_" + hex.EncodeToString(rawBytes)
	hash := sha256.Sum256([]byte(fullToken))
	tokenHash := hex.EncodeToString(hash[:])
	tokenPrefix := fullToken[:8]

	expiresPtr := optionalString(args, "expires_at")
	var expiresAt types.Null[types.Time]
	if expiresPtr != nil && *expiresPtr != "" {
		t, err := time.Parse(time.RFC3339, *expiresPtr)
		if err != nil {
			return "", fmt.Errorf("invalid expires_at: %w", err)
		}
		expiresAt = types.NewTimeNull(t.UTC())
	}

	limitInterval := optionalString(args, "limit_reset_interval")
	if limitInterval != nil && *limitInterval != "" {
		if _, err := str2duration.ParseDuration(*limitInterval); err != nil {
			return "", fmt.Errorf("invalid limit_reset_interval %q: %w", *limitInterval, err)
		}
	}

	allowedProviders, err := decodeStringSlice(args["allowed_providers"])
	if err != nil {
		return "", fmt.Errorf("allowed_providers: %w", err)
	}
	allowedModels, err := decodeStringSlice(args["allowed_models"])
	if err != nil {
		return "", fmt.Errorf("allowed_models: %w", err)
	}
	allowedWebhooks, err := decodeStringSlice(args["allowed_webhooks"])
	if err != nil {
		return "", fmt.Errorf("allowed_webhooks: %w", err)
	}
	allowedMCPs, err := decodeStringSlice(args["allowed_mcps"])
	if err != nil {
		return "", fmt.Errorf("allowed_mcps: %w", err)
	}

	token := service.APIToken{
		Name:                 name,
		TokenPrefix:          tokenPrefix,
		AllowedProvidersMode: stringArg(args, "allowed_providers_mode"),
		AllowedProviders:     allowedProviders,
		AllowedModelsMode:    stringArg(args, "allowed_models_mode"),
		AllowedModels:        allowedModels,
		AllowedWebhooksMode:  stringArg(args, "allowed_webhooks_mode"),
		AllowedWebhooks:      allowedWebhooks,
		AllowedMCPsMode:      stringArg(args, "allowed_mcps_mode"),
		AllowedMCPs:          allowedMCPs,
		ExpiresAt:            expiresAt,
		TotalTokenLimit:      toNullInt64(optionalInt64(args, "total_token_limit")),
		LimitResetInterval:   toNullString(limitInterval),
		CreatedBy:            "mcp",
		UpdatedBy:            "mcp",
	}

	created, err := s.tokenStore.CreateAPIToken(ctx, token, tokenHash)
	if err != nil {
		return "", fmt.Errorf("create api token: %w", err)
	}

	// IMPORTANT: this is the ONLY response path that contains the raw
	// token. The agent should persist it immediately.
	out, err := json.MarshalIndent(map[string]any{
		"token":   fullToken,
		"info":    created,
		"warning": "The `token` field is shown only once and cannot be recovered. Store it immediately.",
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal response: %w", err)
	}
	return string(out), nil
}

func (s *Server) execAPITokenUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.tokenStore == nil {
		return "", fmt.Errorf("api token store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	name := stringArg(args, "name")
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	expiresPtr := optionalString(args, "expires_at")
	var expiresAt types.Null[types.Time]
	if expiresPtr != nil && *expiresPtr != "" {
		t, err := time.Parse(time.RFC3339, *expiresPtr)
		if err != nil {
			return "", fmt.Errorf("invalid expires_at: %w", err)
		}
		expiresAt = types.NewTimeNull(t.UTC())
	}
	limitInterval := optionalString(args, "limit_reset_interval")
	if limitInterval != nil && *limitInterval != "" {
		if _, err := str2duration.ParseDuration(*limitInterval); err != nil {
			return "", fmt.Errorf("invalid limit_reset_interval %q: %w", *limitInterval, err)
		}
	}

	allowedProviders, err := decodeStringSlice(args["allowed_providers"])
	if err != nil {
		return "", fmt.Errorf("allowed_providers: %w", err)
	}
	allowedModels, err := decodeStringSlice(args["allowed_models"])
	if err != nil {
		return "", fmt.Errorf("allowed_models: %w", err)
	}
	allowedWebhooks, err := decodeStringSlice(args["allowed_webhooks"])
	if err != nil {
		return "", fmt.Errorf("allowed_webhooks: %w", err)
	}
	allowedMCPs, err := decodeStringSlice(args["allowed_mcps"])
	if err != nil {
		return "", fmt.Errorf("allowed_mcps: %w", err)
	}

	token := service.APIToken{
		Name:                 name,
		AllowedProvidersMode: stringArg(args, "allowed_providers_mode"),
		AllowedProviders:     allowedProviders,
		AllowedModelsMode:    stringArg(args, "allowed_models_mode"),
		AllowedModels:        allowedModels,
		AllowedWebhooksMode:  stringArg(args, "allowed_webhooks_mode"),
		AllowedWebhooks:      allowedWebhooks,
		AllowedMCPsMode:      stringArg(args, "allowed_mcps_mode"),
		AllowedMCPs:          allowedMCPs,
		ExpiresAt:            expiresAt,
		TotalTokenLimit:      toNullInt64(optionalInt64(args, "total_token_limit")),
		LimitResetInterval:   toNullString(limitInterval),
		UpdatedBy:            "mcp",
	}

	updated, err := s.tokenStore.UpdateAPIToken(ctx, id, token)
	if err != nil {
		// The store returns "not found" via the error string; preserve
		// that semantic for callers without leaking the underlying
		// implementation.
		if strings.Contains(err.Error(), "not found") {
			return "", fmt.Errorf("token %q not found", id)
		}
		return "", fmt.Errorf("update api token %q: %w", id, err)
	}
	out, err := json.MarshalIndent(updated, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal token: %w", err)
	}
	return string(out), nil
}

func (s *Server) execAPITokenDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.tokenStore == nil {
		return "", fmt.Errorf("api token store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.tokenStore.DeleteAPIToken(ctx, id); err != nil {
		return "", fmt.Errorf("delete api token %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}

func (s *Server) execAPITokenGetUsage(ctx context.Context, args map[string]any) (string, error) {
	if s.tokenUsageStore == nil {
		return "", fmt.Errorf("token usage store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	usage, err := s.tokenUsageStore.GetTokenUsage(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get token usage: %w", err)
	}
	if usage == nil {
		usage = []service.TokenUsage{}
	}
	out, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal usage: %w", err)
	}
	return string(out), nil
}

func (s *Server) execAPITokenResetUsage(ctx context.Context, args map[string]any) (string, error) {
	if s.tokenUsageStore == nil {
		return "", fmt.Errorf("token usage store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.tokenUsageStore.ResetTokenUsage(ctx, id); err != nil {
		return "", fmt.Errorf("reset token usage %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"reset","id":%q}`, id), nil
}
