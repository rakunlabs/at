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
	// MetadataDocument marks a client whose client_id is the https URL of
	// its Client ID Metadata Document. Its row caches the fetched document.
	MetadataDocument  bool   `json:"metadata_document,omitempty"`
	MetadataExpiresAt string `json:"metadata_expires_at,omitempty"`
	ClientURI         string `json:"client_uri,omitempty"`
	Confidential      bool   `json:"confidential"`
	SecretHash        string `json:"-"`
	Secret            string `json:"client_secret,omitempty"`
	CreatedBy         string `json:"created_by,omitempty"`
	CreatedAt         string `json:"created_at"`
	LastUsedAt        string `json:"last_used_at,omitempty"`
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
	// RememberedUntil is set while the consent is remembered: a new
	// authorization for the same redirect URI is then issued without asking.
	RememberedUntil string `json:"remembered_until,omitempty"`
	CreatedAt       string `json:"created_at"`
	LastUsedAt      string `json:"last_used_at,omitempty"`
}

// MCPAuthClientUsage is an installation-wide view of one registered client
// for administrators: who signed in through it and when it was last used.
type MCPAuthClientUsage struct {
	MCPAuthClient
	ServerName  string                    `json:"server_name,omitempty"`
	Grants      int                       `json:"grants"`
	ActiveUsers int                       `json:"active_users"`
	Users       []MCPAuthClientUsageEntry `json:"users"`
}

// MCPAuthClientUsageEntry is one account's sign-in through a client.
type MCPAuthClientUsageEntry struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username,omitempty"`
	WorkspaceID string `json:"workspace_id"`
	ServerName  string `json:"server_name,omitempty"`
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
	// RememberUntil, when non-zero, remembers the consent until then.
	RememberUntil time.Time
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
	// MCPAuthConsentLifetime is how long a remembered consent is honoured.
	MCPAuthConsentLifetime = 90 * 24 * time.Hour
	// MCPAuthUnusedClientAge is how long a dynamic or metadata-document
	// client may sit without ever obtaining a grant before it is swept.
	MCPAuthUnusedClientAge = 24 * time.Hour
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
	// FindDynamicMCPAuthClient returns an existing dynamic client with the
	// same name, redirect URIs and authentication method, so a client that
	// registers on every start reuses one record (public clients only).
	FindDynamicMCPAuthClient(ctx context.Context, name string, redirectURIs []string) (*MCPAuthClient, error)
	// UpsertMetadataMCPAuthClient caches a fetched Client ID Metadata
	// Document under its URL client_id.
	UpsertMetadataMCPAuthClient(ctx context.Context, c MCPAuthClient) (*MCPAuthClient, error)

	// Installation administration: every client with its usage, and
	// deletion of any client (which ends every grant issued to it).
	ListAllMCPAuthClients(ctx context.Context) ([]MCPAuthClientUsage, error)
	AdminDeleteMCPAuthClient(ctx context.Context, id string) error
	// SweepMCPAuthClients removes dynamic and metadata-document clients
	// older than the cutoff that never obtained a grant, plus expired codes
	// and tokens. It is internal maintenance and performs no authorization.
	SweepMCPAuthClients(ctx context.Context, unusedBefore time.Time) (int64, error)

	// RememberedMCPAuthGrant returns the caller's live remembered consent for
	// the client, server, resource and exact redirect URI, if any.
	RememberedMCPAuthGrant(ctx context.Context, clientID, mcpServerID, resource, redirectURI string) (*MCPAuthGrant, error)
	// ForgetMCPAuthConsent clears a remembered consent of the caller; the
	// grant and its tokens stay valid.
	ForgetMCPAuthConsent(ctx context.Context, grantID string) error

	// IssueMCPAuthCode creates (or reuses) the caller's grant for the client
	// on the server and stores a code. The account is the context principal.
	// A non-zero code.RememberUntil records the consent for code.RedirectURI.
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

// ─── Client ID Metadata Documents ───

// MCPAuthMetadataClientID reports whether a client_id is a Client ID
// Metadata Document URL: https, a host, a non-root path, no fragment,
// credentials, dot segments or query (draft-ietf-oauth-client-id-metadata-document §3).
func MCPAuthMetadataClientID(clientID string) bool {
	if !strings.HasPrefix(clientID, "https://") || len(clientID) > 2048 {
		return false
	}
	u, err := url.Parse(clientID)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.Path == "" || u.Path == "/" {
		return false
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return u.String() == clientID
}

// MCPAuthClientMetadata is the subset of a Client ID Metadata Document AT
// reads. Unknown fields are ignored.
type MCPAuthClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	ClientURI               string   `json:"client_uri"`
	RedirectURIs            []string `json:"redirect_uris"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	ClientSecret            string   `json:"client_secret"`
}

// ValidateMCPAuthClientMetadata checks a fetched document against the URL it
// was fetched from. Metadata clients are public: a document claiming a
// symmetric secret or a secret-based auth method is refused, because the
// document is public and anyone could read the secret.
func ValidateMCPAuthClientMetadata(clientID string, m MCPAuthClientMetadata) error {
	if m.ClientID != clientID {
		return fmt.Errorf("the document's client_id does not match its URL")
	}
	if m.ClientSecret != "" {
		return fmt.Errorf("a client metadata document must not contain a client_secret")
	}
	switch m.TokenEndpointAuthMethod {
	case "", "none":
	default:
		return fmt.Errorf("token_endpoint_auth_method %q is not supported for metadata documents; use none", m.TokenEndpointAuthMethod)
	}
	if len(m.RedirectURIs) == 0 || len(m.RedirectURIs) > 10 {
		return fmt.Errorf("the document must list between one and ten redirect_uris")
	}
	for _, uri := range m.RedirectURIs {
		if err := ValidMCPAuthRedirectURI(uri); err != nil || len(uri) > 2048 {
			return fmt.Errorf("redirect URI %q must use https or a loopback address", uri)
		}
	}
	for _, g := range m.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			return fmt.Errorf("unsupported grant type %q", g)
		}
	}
	for _, r := range m.ResponseTypes {
		if r != "code" {
			return fmt.Errorf("unsupported response type %q", r)
		}
	}
	if m.ClientURI != "" {
		u, err := url.Parse(m.ClientURI)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("client_uri must be an https URL")
		}
	}
	return nil
}

// MCPAuthClientHost is the host a metadata-document client is identified by;
// the consent page shows it as the verified origin of the application.
func MCPAuthClientHost(clientID string) string {
	u, err := url.Parse(clientID)
	if err != nil {
		return ""
	}
	return u.Host
}
