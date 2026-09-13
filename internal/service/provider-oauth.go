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
