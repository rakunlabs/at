package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// AT as an MCP authorization server. A gateway MCP server with OAuth enabled
// accepts access tokens issued to one account for that one server; calls then
// run as that account under its live workspace access and execution policy.

// MCPServerOAuth is the authorization-server switch of one gateway MCP server
// (MCPServerConfig.OAuth). It lives in the config JSON, so no column is needed
// and an absent block keeps the server token-only.
type MCPServerOAuth struct {
	Enabled bool `json:"enabled"`
	// DynamicClients admits clients registered through RFC 7591 (Claude Code,
	// Cursor, ChatGPT, …). Pre-registered clients are always admitted.
	DynamicClients bool `json:"dynamic_clients"`
	// RedirectPatterns restricts dynamic clients' redirect URIs. Each entry is
	// an exact URI or a prefix ending in "*". Empty admits loopback redirects
	// (http://127.0.0.1, http://localhost, http://[::1], any port) and https.
	RedirectPatterns []string `json:"redirect_patterns,omitempty"`
	// ChainUpstreams sends a signing-in account through each OAuth upstream it
	// has not connected yet, right after consent, so the first tool call does
	// not fail with "connect your account".
	ChainUpstreams bool `json:"chain_upstreams"`
}

// MCPAuthClient is a registered OAuth client. SecretHash is never returned
// by the API; Secret is set only in the response that created the client.
type MCPAuthClient struct {
	ID           string   `json:"id"`
	ClientID     string   `json:"client_id"`
	WorkspaceID  string   `json:"workspace_id,omitempty"`
	MCPServerID  string   `json:"mcp_server_id,omitempty"`
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
	Dynamic      bool     `json:"dynamic"`
	Confidential bool     `json:"confidential"`
	SecretHash   string   `json:"-"`
	Secret       string   `json:"client_secret,omitempty"`
	CreatedBy    string   `json:"created_by,omitempty"`
	CreatedAt    string   `json:"created_at"`
	LastUsedAt   string   `json:"last_used_at,omitempty"`
}

// MCPAuthGrant is one account's consent for one client on one MCP server.
type MCPAuthGrant struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspace_id"`
	UserID      string `json:"user_id"`
	MCPServerID string `json:"mcp_server_id"`
	ServerName  string `json:"server_name,omitempty"`
	ClientID    string `json:"client_id"`
	ClientName  string `json:"client_name"`
	Resource    string `json:"resource"`
	Revoked     bool   `json:"revoked,omitempty"`
	CreatedAt   string `json:"created_at"`
	LastUsedAt  string `json:"last_used_at,omitempty"`
}

// MCPAuthCode is a pending authorization code.
type MCPAuthCode struct {
	GrantID       string
	WorkspaceID   string
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

// MCPAuthAccess is what a presented access token resolves to.
type MCPAuthAccess struct {
	Grant MCPAuthGrant
}

// Lifetimes of the authorization server's artifacts.
const (
	MCPAuthCodeLifetime    = 10 * time.Minute
	MCPAuthAccessLifetime  = time.Hour
	MCPAuthRefreshLifetime = 30 * 24 * time.Hour
	// MCPAuthDynamicClientLimit bounds unauthenticated registrations so the
	// table cannot be filled; the oldest unused ones are pruned first.
	MCPAuthDynamicClientLimit = 5000
)

// ErrMCPAuthInvalidGrant covers every refusal of a code or refresh token:
// unknown, expired, used, revoked, or bound to another client/redirect.
// OAuth reports them all as invalid_grant, so they are not distinguished.
var ErrMCPAuthInvalidGrant = errors.New("invalid_grant")

// MCPAuthServerStorer persists the authorization server. Lookups by secret
// take the SHA-256 hash; callers never pass plaintext.
type MCPAuthServerStorer interface {
	// Clients. Dynamic registration is unauthenticated by design and
	// installation-wide; pre-registered clients are scoped to the caller's
	// workspace and one MCP server.
	RegisterDynamicMCPAuthClient(ctx context.Context, c MCPAuthClient) (*MCPAuthClient, error)
	CreateMCPAuthClient(ctx context.Context, c MCPAuthClient) (*MCPAuthClient, error)
	ListMCPAuthClients(ctx context.Context, mcpServerID string) ([]MCPAuthClient, error)
	DeleteMCPAuthClient(ctx context.Context, id string) error
	// GetMCPAuthClient is unauthenticated metadata for the protocol handlers.
	GetMCPAuthClient(ctx context.Context, clientID string) (*MCPAuthClient, error)

	// IssueMCPAuthCode creates (or reuses) the caller's grant for the client
	// on the server and stores a code. The account is the context principal.
	IssueMCPAuthCode(ctx context.Context, client MCPAuthClient, mcpServerID, resource, codeHash string, code MCPAuthCode) (*MCPAuthGrant, error)
	// ExchangeMCPAuthCode consumes a code and issues the token pair.
	ExchangeMCPAuthCode(ctx context.Context, codeHash, clientID, redirectURI string, verify func(challenge string) bool, accessHash, refreshHash string) (*MCPAuthGrant, error)
	// RefreshMCPAuthTokens rotates a refresh token.
	RefreshMCPAuthTokens(ctx context.Context, refreshHash, clientID, accessHash, newRefreshHash string) (*MCPAuthGrant, error)
	// ResolveMCPAuthAccessToken returns the live grant of an access token, or
	// nil for an unknown, expired or revoked token.
	ResolveMCPAuthAccessToken(ctx context.Context, accessHash string) (*MCPAuthAccess, error)
	// RevokeMCPAuthToken revokes the grant behind a token (RFC 7009).
	RevokeMCPAuthToken(ctx context.Context, tokenHash, clientID string) error

	// MCPAuthGrantLive reports whether a grant still exists for the account
	// and workspace (execution revalidation).
	MCPAuthGrantLive(ctx context.Context, grantID, userID, workspaceID string) (bool, error)

	// GetOAuthMCPRoute is routing metadata before admission: the gateway MCP
	// server with this name, with its full configuration so the OAuth switch
	// can be read. A name used in several workspaces is ErrWorkspaceConflict.
	GetOAuthMCPRoute(ctx context.Context, name string) (*MCPServer, error)

	// Grants of the signed-in account in the selected workspace.
	ListMCPAuthGrants(ctx context.Context) ([]MCPAuthGrant, error)
	RevokeMCPAuthGrant(ctx context.Context, id string) error
}

// ValidMCPAuthRedirectURI checks a redirect URI a client registers or
// requests: absolute, no fragment, no credentials, and https unless it is a
// loopback address (RFC 8252 native apps).
func ValidMCPAuthRedirectURI(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" || u.Fragment != "" || u.User != nil {
		return fmt.Errorf("invalid redirect URI %q", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if MCPAuthLoopbackHost(u.Hostname()) {
			return nil
		}
	}
	return fmt.Errorf("redirect URI %q must use https or a loopback address", raw)
}

// MCPAuthLoopbackHost reports a loopback redirect host.
func MCPAuthLoopbackHost(host string) bool {
	return host == "127.0.0.1" || host == "::1" || strings.EqualFold(host, "localhost")
}

// MCPAuthRedirectMatches reports whether requested equals a registered
// redirect URI. Loopback URIs may differ in port only (RFC 8252 §7.3).
func MCPAuthRedirectMatches(registered []string, requested string) bool {
	if slices.Contains(registered, requested) {
		return true
	}
	req, err := url.Parse(requested)
	if err != nil || req.Scheme != "http" || !MCPAuthLoopbackHost(req.Hostname()) {
		return false
	}
	for _, r := range registered {
		reg, err := url.Parse(r)
		if err != nil || reg.Scheme != "http" || !MCPAuthLoopbackHost(reg.Hostname()) {
			continue
		}
		if reg.Hostname() == req.Hostname() && reg.EscapedPath() == req.EscapedPath() && reg.RawQuery == req.RawQuery {
			return true
		}
	}
	return false
}

// MCPAuthRedirectAllowed applies a server's dynamic-client redirect policy.
func MCPAuthRedirectAllowed(patterns []string, redirect string) bool {
	if ValidMCPAuthRedirectURI(redirect) != nil {
		return false
	}
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			if prefix != "" && strings.HasPrefix(redirect, prefix) {
				return true
			}
			continue
		}
		if MCPAuthRedirectMatches([]string{p}, redirect) {
			return true
		}
	}
	return false
}

// NormalizeMCPServerOAuth validates the switch in place.
func NormalizeMCPServerOAuth(o *MCPServerOAuth) error {
	if o == nil {
		return nil
	}
	var patterns []string
	for _, p := range o.RedirectPatterns {
		p = strings.TrimSpace(p)
		if p == "" || slices.Contains(patterns, p) {
			continue
		}
		check := strings.TrimSuffix(p, "*")
		if u, err := url.Parse(check); err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("%w: redirect pattern %q must be an absolute URI or a prefix ending in *", ErrInvalidMCPConfig, p)
		}
		patterns = append(patterns, p)
	}
	if len(patterns) > 32 {
		return fmt.Errorf("%w: at most 32 redirect patterns", ErrInvalidMCPConfig)
	}
	o.RedirectPatterns = patterns
	return nil
}
