package service

import (
	"context"
	"encoding/json"
)

// MCPOAuthPendingStorer stores an opaque authorization payload (PKCE verifier,
// client credentials, original upstream and redirect URI). Stores bind it to
// the current live account, workspace and exact session, encrypt it, expire it
// after ten minutes and consume it atomically. stateHash is SHA-256 of a random
// state, never the browser's plaintext nonce. A foreign or expired state returns
// nil, indistinguishable from an unknown state.
type MCPOAuthPendingStorer interface {
	SaveMCPOAuthPending(ctx context.Context, stateHash string, payload json.RawMessage) error
	TakeMCPOAuthPending(ctx context.Context, stateHash string) (json.RawMessage, error)
}
