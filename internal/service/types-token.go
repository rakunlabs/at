package service

import (
	"context"

	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── API Token Management ───

// Restriction mode constants for APIToken allowed_*_mode fields.
// "" (empty) and "all" mean unrestricted, "none" means deny all,
// "list" means only items in the corresponding slice are allowed.
const (
	AccessModeAll  = "all"
	AccessModeNone = "none"
	AccessModeList = "list"
)

// APIToken represents a bearer token stored in the database for gateway auth.
type APIToken struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	TokenPrefix          string                 `json:"token_prefix"`           // first 8 chars for display (e.g. "at_xxxx…")
	AllowedProvidersMode string                 `json:"allowed_providers_mode"` // "all" (default/""), "none", or "list"
	AllowedProviders     types.Slice[string]    `json:"allowed_providers"`      // used when mode = "list"
	AllowedModelsMode    string                 `json:"allowed_models_mode"`    // "all" (default/""), "none", or "list"
	AllowedModels        types.Slice[string]    `json:"allowed_models"`         // used when mode = "list" ("provider/model" format)
	AllowedWebhooksMode  string                 `json:"allowed_webhooks_mode"`  // "all" (default/""), "none", or "list"
	AllowedWebhooks      types.Slice[string]    `json:"allowed_webhooks"`       // used when mode = "list" (trigger IDs or aliases)
	AllowedRAGMCPsMode   string                 `json:"allowed_rag_mcps_mode"`  // "all" (default/""), "none", or "list"
	AllowedRAGMCPs       types.Slice[string]    `json:"allowed_rag_mcps"`       // used when mode = "list" (server names)
	ExpiresAt            types.Null[types.Time] `json:"expires_at"`             // zero value = no expiry
	TotalTokenLimit      types.Null[int64]      `json:"total_token_limit"`      // max total tokens allowed (across all models); nil = unlimited
	LimitResetInterval   types.Null[string]     `json:"limit_reset_interval"`   // "daily", "weekly", "monthly", or nil = manual only
	LastResetAt          types.Null[types.Time] `json:"last_reset_at"`          // last time usage counters were reset
	CreatedAt            types.Time             `json:"created_at"`
	LastUsedAt           types.Null[types.Time] `json:"last_used_at"`
	CreatedBy            string                 `json:"created_by"`
	UpdatedBy            string                 `json:"updated_by"`
}

// ResolveAccessMode returns the effective mode for a restriction field.
// It handles backward compatibility: if mode is empty but the slice has items,
// it returns "list"; otherwise empty is treated as "all".
func ResolveAccessMode(mode string, items []string) string {
	if mode != "" {
		return mode
	}
	// Backward compat: old tokens have no mode but may have a populated list.
	if len(items) > 0 {
		return AccessModeList
	}
	return AccessModeAll
}

// APITokenStorer defines CRUD operations for API tokens.
type APITokenStorer interface {
	ListAPITokens(ctx context.Context, q *query.Query) (*ListResult[APIToken], error)
	GetAPITokenByHash(ctx context.Context, hash string) (*APIToken, error)
	CreateAPIToken(ctx context.Context, token APIToken, tokenHash string) (*APIToken, error)
	UpdateAPIToken(ctx context.Context, id string, token APIToken) (*APIToken, error)
	DeleteAPIToken(ctx context.Context, id string) error
	UpdateLastUsed(ctx context.Context, id string) error
}

// ─── Token Usage Tracking ───

// TokenUsage represents cumulative usage statistics for a single API token + model combination.
type TokenUsage struct {
	TokenID          string     `json:"token_id"`
	Model            string     `json:"model"`
	PromptTokens     int64      `json:"prompt_tokens"`
	CompletionTokens int64      `json:"completion_tokens"`
	TotalTokens      int64      `json:"total_tokens"`
	RequestCount     int64      `json:"request_count"`
	LastRequestAt    types.Time `json:"last_request_at"`
}

// TokenUsageStorer defines operations for recording and querying per-token usage.
type TokenUsageStorer interface {
	// RecordUsage atomically increments usage counters for a token+model pair.
	RecordUsage(ctx context.Context, tokenID, model string, usage Usage) error
	// GetTokenUsage returns per-model usage breakdown for a token.
	GetTokenUsage(ctx context.Context, tokenID string) ([]TokenUsage, error)
	// GetTokenTotalUsage returns the sum of total_tokens across all models for a token.
	GetTokenTotalUsage(ctx context.Context, tokenID string) (int64, error)
	// ResetTokenUsage deletes all usage rows for a token and updates last_reset_at.
	ResetTokenUsage(ctx context.Context, tokenID string) error
}
