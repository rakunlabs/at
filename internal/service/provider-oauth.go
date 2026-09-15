package service

import (
	"context"
	"time"
)

// ClaudeOAuthTokens is used only by trusted provider wiring, never a public DTO.
type ClaudeOAuthTokens struct {
	AccessToken, RefreshToken string
	ExpiresAt                 time.Time
	PreviousRefreshToken      string
}

// ClaudeOAuthTokenStorer is a credential-rotation-only seam. The previous
// refresh credential must match the stored credential; this does not grant
// general provider configuration or human-session authority.
//
// WithClaudeOAuthTokens serializes reload, exchange and persistence across all
// token sources and replicas using the provider's owning workspace. Anthropic
// rotates the refresh credential on every exchange, so a source that refreshes
// from a purely in-memory copy can spend a credential another source already
// consumed and get `400 invalid_grant` back. It returns freshly exchanged
// tokens even on persistence failure so the source can retry saving rather
// than rotate the single-use refresh credential again.
type ClaudeOAuthTokenStorer interface {
	WithClaudeOAuthTokens(context.Context, string, string, func(*ClaudeOAuthTokens) error) (*ClaudeOAuthTokens, error)
	RotateClaudeOAuthTokens(ctx context.Context, workspace, key, previousRefresh, access, refresh string, expiry time.Time) error
}

// CodexOAuthTokens is used only by trusted provider wiring, never a public DTO.
type CodexOAuthTokens struct {
	AccessToken, RefreshToken, AccountID string
	ExpiresAt                            time.Time
	PreviousRefreshToken                 string
}

// WithCodexOAuthTokens serializes reload, exchange and persistence across all
// instances using the provider's owning workspace. It returns freshly exchanged
// tokens even on persistence failure so the source can retry saving, not rotate
// the single-use refresh credential again.
type CodexOAuthTokenStorer interface {
	WithCodexOAuthTokens(context.Context, string, string, func(*CodexOAuthTokens) error) (*CodexOAuthTokens, error)
	RotateCodexOAuthTokens(context.Context, string, string, string, CodexOAuthTokens) error
}
