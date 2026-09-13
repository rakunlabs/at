package service

import (
	"context"
	"time"
)

// ClaudeOAuthTokenStorer is a credential-rotation-only seam. The previous
// refresh credential must match the stored credential; this does not grant
// general provider configuration or human-session authority.
type ClaudeOAuthTokenStorer interface {
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
