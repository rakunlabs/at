package service

import (
	"context"
	"errors"
	"time"

	"github.com/rakunlabs/query"
)

// GitSSHCredentialProvider is reserved for AT-managed deploy-key records. These
// connections are never exposed through the generic Connections API.
const GitSSHCredentialProvider = "__at_git_ssh_credential"

// ─── Connection Management ───
//
// A Connection represents a named, reusable set of credentials for an external
// service (YouTube, Google, Twitter, etc.). Multiple connections can exist for
// the same provider, e.g. several YouTube channels. Agents reference connections
// by ID via AgentConfig.Connections (per-agent default) and SkillRef.Connections
// (per-skill override). One connection can be shared by any number of agents.
//
// The Credentials field is encrypted at rest (AES-256-GCM) using the database
// encryption key configured for the store.

// ConnectionCredentials holds the secret bundle for a connection. Different
// providers populate different subsets of these fields. Marshaled to JSON,
// then encrypted as a single ciphertext blob in the credentials column.
type ConnectionCredentials struct {
	// OAuth2-style fields.
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`

	// Single-token providers (e.g. API key based skills).
	APIKey string `json:"api_key,omitempty"`

	// Free-form bag for additional secrets (multi-key skills, future providers).
	// Keys here are the original variable names (e.g. "openrouter_api_key").
	Extra map[string]string `json:"extra,omitempty"`

	// MCPOAuth is set on connections that authorize an HTTP MCP upstream
	// through the MCP authorization spec. It is encrypted with the rest of the
	// bundle and only ever read by the runtime token source.
	MCPOAuth *MCPOAuthCredential `json:"mcp_oauth,omitempty"`
}

// MCPOAuthCredential is the result of one MCP OAuth authorization plus what
// refreshing it needs. The token endpoint, client and resource are pinned at
// authorization time so a later configuration edit cannot redirect a stored
// refresh token to another server.
type MCPOAuthCredential struct {
	AccessToken        string    `json:"access_token,omitempty"`
	RefreshToken       string    `json:"refresh_token,omitempty"`
	ExpiresAt          time.Time `json:"expires_at,omitzero"`
	ClientID           string    `json:"client_id"`
	ClientSecret       string    `json:"client_secret,omitempty"`
	Issuer             string    `json:"issuer"`
	TokenEndpoint      string    `json:"token_endpoint"`
	Resource           string    `json:"resource,omitempty"`
	Scopes             []string  `json:"scopes,omitempty"`
	MCPURL             string    `json:"mcp_url"`
	Proxy              string    `json:"proxy,omitempty"` // retained for reconnecting the account
	InsecureSkipVerify bool      `json:"insecure_skip_verify,omitempty"`
	// NeedsReauth is set when the authorization server rejected the refresh
	// token. Only a new authorization clears it.
	NeedsReauth bool `json:"needs_reauth,omitempty"`
}

// Fresh reports whether the access token can be used without refreshing.
// A token without a reported expiry is used until the server rejects it.
func (c *MCPOAuthCredential) Fresh(now time.Time) bool {
	if c == nil || c.AccessToken == "" || c.NeedsReauth {
		return false
	}
	return c.ExpiresAt.IsZero() || now.Add(MCPOAuthRefreshSkew).Before(c.ExpiresAt)
}

// MCPOAuthRefreshSkew refreshes slightly before expiry so a request in flight
// does not race the deadline.
const MCPOAuthRefreshSkew = 60 * time.Second

// ErrMCPOAuthReauthRequired means the stored authorization can no longer be
// refreshed. It is never answered by falling back to another account.
var ErrMCPOAuthReauthRequired = errors.New("MCP account authorization expired; reconnect the account")

// Connection scopes. A personal connection belongs to one account inside a
// workspace and is never readable or usable by anyone else, administrators
// included; a workspace connection is shared by the workspace.
const (
	ConnectionScopeWorkspace = "workspace"
	ConnectionScopePersonal  = "personal"
)

// ConnectionScope derives the scope from the owner column.
func ConnectionScope(ownerUserID string) string {
	if ownerUserID != "" {
		return ConnectionScopePersonal
	}
	return ConnectionScopeWorkspace
}

// Connection represents a named, reusable credential set for a single external
// provider instance.
type Connection struct {
	WorkspaceID  string                `json:"workspace_id"`
	OwnerUserID  string                `json:"owner_user_id,omitempty"`
	Scope        string                `json:"scope"`
	ID           string                `json:"id"`
	Provider     string                `json:"provider"`                // "youtube", "google", "twitter", or skill slug for token-only
	Name         string                `json:"name"`                    // unique within provider, e.g. "Main Channel"
	AccountLabel string                `json:"account_label,omitempty"` // human-readable identity (channel title, email)
	Description  string                `json:"description,omitempty"`
	Credentials  ConnectionCredentials `json:"credentials"`        // encrypted JSON blob in storage
	Metadata     map[string]any        `json:"metadata,omitempty"` // scopes, expires_at, etc.
	CreatedAt    string                `json:"created_at"`
	UpdatedAt    string                `json:"updated_at"`
	CreatedBy    string                `json:"created_by,omitempty"`
	UpdatedBy    string                `json:"updated_by,omitempty"`
}

// ConnectionStorer defines CRUD operations for connections.
type ConnectionStorer interface {
	ListConnections(ctx context.Context, q *query.Query) (*ListResult[Connection], error)
	ListConnectionsByProvider(ctx context.Context, provider string) ([]Connection, error)
	GetConnection(ctx context.Context, id string) (*Connection, error)
	GetConnectionByName(ctx context.Context, provider, name string) (*Connection, error)
	CreateConnection(ctx context.Context, c Connection) (*Connection, error)
	UpdateConnection(ctx context.Context, id string, c Connection) (*Connection, error)
	DeleteConnection(ctx context.Context, id string) error
}

// MCPOAuthConnectionStorer is the runtime half of MCP OAuth. Every method is
// admission-checked against the context principal (connections.use, and a
// personal connection only for its owner); none is an HTTP DTO.
type MCPOAuthConnectionStorer interface {
	// ResolvePersonalConnectionForUse returns the caller's own newest
	// personal connection for provider, or nil.
	ResolvePersonalConnectionForUse(ctx context.Context, provider string) (*Connection, error)
	// WithMCPOAuthTokens holds the connection row lock across reload, change
	// and encrypted save, so replicas never spend one refresh token twice.
	// The credential is saved when change succeeds, and also when it fails
	// with ErrMCPOAuthReauthRequired (to record that state).
	WithMCPOAuthTokens(ctx context.Context, connectionID string, change func(*MCPOAuthCredential) error) (*MCPOAuthCredential, error)
}
