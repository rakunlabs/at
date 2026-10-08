// Package mcpauth implements the client side of the MCP authorization spec:
// protected resource metadata discovery (RFC 9728), authorization server
// metadata discovery (RFC 8414 / OIDC discovery), dynamic client registration
// (RFC 7591), the authorization code grant with PKCE (RFC 7636) and resource
// indicators (RFC 8707), and refresh.
//
// It is pure protocol: it stores nothing and knows nothing about workspaces,
// connections or accounts. Every URL it is given or discovers is fetched
// through Client, which refuses link-local and multicast destinations always
// and private/loopback destinations unless the MCP server itself lives there.
package mcpauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// Metadata is everything needed to run an authorization for one MCP server.
type Metadata struct {
	// Resource is the canonical resource identifier sent as the RFC 8707
	// `resource` parameter.
	Resource string `json:"resource"`
	// Issuer is the authorization server.
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint,omitempty"`
	ScopesSupported       []string `json:"scopes_supported,omitempty"`
	// ChallengeScope is the scope the server named in its 401 challenge.
	ChallengeScope string `json:"challenge_scope,omitempty"`
}

// Token is a token endpoint response.
type Token struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// ExpiresAt converts ExpiresIn into an absolute time. Zero means unknown.
func (t *Token) ExpiresAt(now time.Time) time.Time {
	if t == nil || t.ExpiresIn <= 0 {
		return time.Time{}
	}
	// Bound untrusted seconds before converting to a duration (overflow would
	// otherwise turn a long-lived token into one already expired).
	seconds := min(t.ExpiresIn, int64(365*24*60*60))
	return now.Add(time.Duration(seconds) * time.Second)
}

// TokenError is an OAuth error response from the token endpoint.
type TokenError struct {
	Status      int
	Code        string
	Description string
}

func (e *TokenError) Error() string {
	// The upstream may echo submitted credentials in its description. Keep it
	// out of logs/tool results; callers can branch on the standardized code.
	if e.Code == "invalid_grant" || e.Code == "invalid_client" || e.Code == "unauthorized_client" || e.Code == "invalid_scope" || e.Code == "access_denied" {
		return fmt.Sprintf("token endpoint returned %d %s", e.Status, e.Code)
	}
	return fmt.Sprintf("token endpoint returned %d", e.Status)
}

// IsInvalidGrant reports a refresh token or code the server no longer
// accepts. The only recovery is a new authorization.
func IsInvalidGrant(err error) bool {
	var te *TokenError
	return errors.As(err, &te) && (te.Code == "invalid_grant" || te.Code == "unauthorized_client")
}

// ErrRegistrationUnsupported means the authorization server offers no
// dynamic client registration and no client ID was configured.
var ErrRegistrationUnsupported = errors.New("the authorization server does not support dynamic client registration; configure a client ID (and secret) for this MCP server")

const (
	maxMetadataBytes = 256 << 10
	requestTimeout   = 20 * time.Second
)

var cgnat = netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 64, 0, 0}), 10)

func isCGNAT(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	return ok && cgnat.Contains(a.Unmap())
}

// AllowsPrivate reports whether rawURL addresses a loopback or private host.
// An MCP server configured on a private network may use an authorization
// server on that network; a public one may not send AT there.
func AllowsPrivate(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || isCGNAT(ip))
}

// Client returns the HTTP client every discovery, registration and token
// request goes through. Destinations are checked at connect time, which also
// covers redirects and DNS answers that change between lookup and dial.
func Client(allowPrivate bool) *http.Client {
	transport := &http.Transport{
		Proxy: nil,
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: func(_, address string, _ syscall.RawConn) error {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return err
				}
				ip := net.ParseIP(host)
				if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
					return fmt.Errorf("refusing authorization request to %s", host)
				}
				if !allowPrivate && (ip.IsLoopback() || ip.IsPrivate() || isCGNAT(ip)) {
					return fmt.Errorf("refusing authorization request to non-public address %s", host)
				}
				return nil
			},
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: requestTimeout,
	}
	return &http.Client{
		Timeout: requestTimeout,
		// A proxy resolves the destination itself and bypasses our dial-time
		// address checks. OAuth discovery must not use ambient proxies.
		Transport: &endpointTransport{base: transport, allowPrivate: allowPrivate},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			// Never replay client secrets, refresh tokens or authorization codes
			// to a redirect target (307/308 preserve the request body).
			if len(via) > 0 && via[0].Method != http.MethodGet {
				return errors.New("authorization POST redirects are not allowed")
			}
			return checkEndpoint(req.URL.String(), allowPrivate)
		},
	}
}

type endpointTransport struct {
	base         *http.Transport
	allowPrivate bool
}

func (t *endpointTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkEndpoint(req.URL.String(), t.allowPrivate); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return t.base.RoundTrip(req)
}

func (t *endpointTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

// checkEndpoint refuses an endpoint that is not https, unless the MCP server
// is itself on a private network (local development).
func checkEndpoint(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("invalid endpoint %q", raw)
	}
	if u.Scheme == "https" || (u.Scheme == "http" && allowPrivate && AllowsPrivate(raw)) {
		return nil
	}
	return fmt.Errorf("endpoint %q must use https", raw)
}

// Discover resolves the authorization metadata for an MCP server URL.
// override, when set, is the issuer to use instead of protected resource
// metadata (for servers that publish none).
func Discover(ctx context.Context, client *http.Client, mcpURL, override string) (*Metadata, error) {
	allowPrivate := AllowsPrivate(mcpURL)
	if err := checkEndpoint(mcpURL, allowPrivate); err != nil {
		return nil, err
	}
	meta := &Metadata{Resource: CanonicalResource(mcpURL)}

	issuer := strings.TrimSpace(override)
	if issuer == "" {
		prm, challengeScope, err := discoverProtectedResource(ctx, client, mcpURL)
		if err != nil {
			return nil, err
		}
		meta.ChallengeScope = challengeScope
		if prm != nil {
			if !sameResource(prm.Resource, mcpURL) {
				return nil, fmt.Errorf("protected resource metadata names %q, which does not match %q", prm.Resource, mcpURL)
			}
			meta.Resource = CanonicalResource(prm.Resource)
			meta.ScopesSupported = prm.ScopesSupported
			if len(prm.AuthorizationServers) > 0 {
				issuer = prm.AuthorizationServers[0]
			}
		}
		if issuer == "" {
			return nil, errors.New("MCP server publishes no authorization server; configure an explicit issuer")
		}
	}
	if err := checkEndpoint(issuer, allowPrivate); err != nil {
		return nil, err
	}

	as, err := discoverAuthorizationServer(ctx, client, issuer)
	if err != nil {
		return nil, err
	}
	if as == nil {
		return nil, fmt.Errorf("authorization server %q publishes no metadata", issuer)
	}
	if as.AuthorizationEndpoint == "" || as.TokenEndpoint == "" {
		return nil, fmt.Errorf("authorization server %q publishes no authorization or token endpoint", issuer)
	}
	if !contains(as.CodeChallengeMethodsSupported, "S256") {
		return nil, fmt.Errorf("authorization server %q does not support PKCE S256", issuer)
	}
	for _, endpoint := range []string{as.AuthorizationEndpoint, as.TokenEndpoint, as.RegistrationEndpoint} {
		if endpoint == "" {
			continue
		}
		if err := checkEndpoint(endpoint, allowPrivate); err != nil {
			return nil, err
		}
	}
	meta.Issuer = issuer
	meta.AuthorizationEndpoint = as.AuthorizationEndpoint
	meta.TokenEndpoint = as.TokenEndpoint
	meta.RegistrationEndpoint = as.RegistrationEndpoint
	if len(meta.ScopesSupported) == 0 {
		meta.ScopesSupported = as.ScopesSupported
	}
	return meta, nil
}

// DefaultScopes picks the scopes to request when none are configured: the
// scope from the 401 challenge, else what the protected resource advertises.
func (m *Metadata) DefaultScopes() []string {
	if m.ChallengeScope != "" {
		return strings.Fields(m.ChallengeScope)
	}
	return m.ScopesSupported
}

// CanonicalResource is the RFC 8707 form of an MCP URL: lowercase scheme and
// host, no fragment, no trailing slash on an empty path.
func CanonicalResource(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	u.Fragment = ""
	if u.Path == "/" {
		u.Path = ""
	}
	return u.String()
}

func sameResource(declared, mcpURL string) bool {
	d, m := strings.TrimSuffix(CanonicalResource(declared), "/"), strings.TrimSuffix(CanonicalResource(mcpURL), "/")
	if d == m {
		return true
	}
	return false
}

type protectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
	ScopesSupported      []string `json:"scopes_supported"`
}

type authorizationServerMetadata struct {
	Issuer                        string   `json:"issuer"`
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	RegistrationEndpoint          string   `json:"registration_endpoint"`
	ScopesSupported               []string `json:"scopes_supported"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
}

// discoverProtectedResource probes the MCP endpoint without credentials and
// follows its WWW-Authenticate resource_metadata; without one it tries the
// well-known locations. A server that answers without asking for credentials
// returns nil metadata.
func discoverProtectedResource(ctx context.Context, client *http.Client, mcpURL string) (*protectedResourceMetadata, string, error) {
	var candidates []string
	challengeScope := ""
	header, err := probeChallenge(ctx, client, mcpURL)
	if err != nil {
		return nil, "", fmt.Errorf("probe MCP authorization: %w", err)
	}
	if header != "" {
		params := ParseBearerChallenge(header)
		challengeScope = params["scope"]
		if rm := params["resource_metadata"]; rm != "" {
			candidates = append(candidates, rm)
		}
	}
	u, err := url.Parse(mcpURL)
	if err != nil {
		return nil, "", fmt.Errorf("parse MCP URL: %w", err)
	}
	origin := u.Scheme + "://" + u.Host
	if path := strings.TrimSuffix(u.EscapedPath(), "/"); path != "" {
		candidates = append(candidates, origin+"/.well-known/oauth-protected-resource"+path)
	}
	candidates = append(candidates, origin+"/.well-known/oauth-protected-resource")

	allowPrivate := AllowsPrivate(mcpURL)
	for _, candidate := range candidates {
		if err := checkEndpoint(candidate, allowPrivate); err != nil {
			return nil, "", err
		}
		var prm protectedResourceMetadata
		found, err := getJSON(ctx, client, candidate, &prm)
		if err != nil {
			return nil, "", err
		}
		if found {
			return &prm, challengeScope, nil
		}
	}
	return nil, challengeScope, nil
}

// probeChallenge sends an unauthenticated initialize and returns the
// WWW-Authenticate header of a 401.
func probeChallenge(ctx context.Context, client *http.Client, mcpURL string) (string, error) {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"at-mcp-client","version":"1.0.0"}}}`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, mcpURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil
	}
	return resp.Header.Get("WWW-Authenticate"), nil
}

func discoverAuthorizationServer(ctx context.Context, client *http.Client, issuer string) (*authorizationServerMetadata, error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return nil, fmt.Errorf("parse authorization server %q: %w", issuer, err)
	}
	origin := u.Scheme + "://" + u.Host
	path := strings.TrimSuffix(u.EscapedPath(), "/")
	var candidates []string
	if path != "" {
		candidates = []string{
			origin + "/.well-known/oauth-authorization-server" + path,
			origin + "/.well-known/openid-configuration" + path,
			origin + path + "/.well-known/openid-configuration",
		}
	} else {
		candidates = []string{
			origin + "/.well-known/oauth-authorization-server",
			origin + "/.well-known/openid-configuration",
		}
	}
	for _, candidate := range candidates {
		var as authorizationServerMetadata
		found, err := getJSON(ctx, client, candidate, &as)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		if as.Issuer != issuer {
			return nil, fmt.Errorf("authorization server metadata at %s names issuer %q, expected %q", candidate, as.Issuer, issuer)
		}
		return &as, nil
	}
	return nil, nil
}

// getJSON fetches a metadata document. A 404/405/400/401 is "not here" so
// discovery can try the next location; any other failure is an error.
func getJSON(ctx context.Context, client *http.Client, rawURL string, out any) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusOK:
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return false, nil
	default:
		return false, fmt.Errorf("fetch %s: status %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxMetadataBytes+1))
	if err != nil {
		return false, fmt.Errorf("read %s: %w", rawURL, err)
	}
	if len(data) > maxMetadataBytes {
		return false, fmt.Errorf("metadata at %s is too large", rawURL)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return false, fmt.Errorf("decode metadata at %s: %w", rawURL, err)
	}
	return true, nil
}

// ParseBearerChallenge reads the auth-params of a Bearer WWW-Authenticate
// challenge (RFC 6750 §3), e.g. resource_metadata and scope.
func ParseBearerChallenge(header string) map[string]string {
	out := map[string]string{}
	// Split only outside quoted values: "Bearer" inside Basic's realm must
	// not be mistaken for an authentication scheme.
	quoted, escaped, start := false, false, 0
	parts := []string{}
	for i, r := range header {
		if escaped {
			escaped = false
			continue
		}
		if quoted && r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			quoted = !quoted
		}
		if r == ',' && !quoted {
			parts = append(parts, strings.TrimSpace(header[start:i]))
			start = i + 1
		}
	}
	parts = append(parts, strings.TrimSpace(header[start:]))
	rest := ""
	for i, part := range parts {
		if len(part) >= len("Bearer ") && strings.EqualFold(part[:len("Bearer")], "Bearer") && (part[len("Bearer")] == ' ' || part[len("Bearer")] == '\t') {
			rest = strings.Join(parts[i:], ",")
			rest = strings.TrimSpace(rest[len("Bearer"):])
			break
		}
	}
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " ,\t")
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(rest[:eq]))
		if strings.ContainsAny(key, " \t") {
			// A new auth scheme begins here; its parameters are not Bearer's.
			break
		}
		rest = strings.TrimLeft(rest[eq+1:], " \t")
		var value string
		if strings.HasPrefix(rest, `"`) {
			var b strings.Builder
			i := 1
			for ; i < len(rest); i++ {
				if rest[i] == '\\' && i+1 < len(rest) {
					i++
					b.WriteByte(rest[i])
					continue
				}
				if rest[i] == '"' {
					break
				}
				b.WriteByte(rest[i])
			}
			value = b.String()
			if i < len(rest) {
				rest = rest[i+1:]
			} else {
				rest = ""
			}
		} else {
			end := strings.IndexAny(rest, ", \t")
			if end < 0 {
				end = len(rest)
			}
			value, rest = rest[:end], rest[end:]
		}
		out[key] = value
	}
	return out
}

// Register performs dynamic client registration for a public client.
func Register(ctx context.Context, client *http.Client, meta *Metadata, redirectURI, clientName string) (clientID, clientSecret string, err error) {
	if meta.RegistrationEndpoint == "" {
		return "", "", ErrRegistrationUnsupported
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                clientName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, meta.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("register client: %w", err)
	}
	defer resp.Body.Close()
	data, err := readPayload(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("read registration response: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
		return "", "", ErrRegistrationUnsupported
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("register client: status %d", resp.StatusCode)
	}
	var out struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.ClientID == "" {
		return "", "", fmt.Errorf("register client: response carries no client_id")
	}
	return out.ClientID, out.ClientSecret, nil
}

// PKCE is a verifier and its S256 challenge.
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE returns a fresh 256-bit verifier.
func NewPKCE() (PKCE, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return PKCE{}, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(b[:])
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{Verifier: verifier, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// AuthorizeURL builds the authorization request.
func AuthorizeURL(meta *Metadata, clientID, redirectURI string, scopes []string, state, challenge string) (string, error) {
	u, err := url.Parse(meta.AuthorizationEndpoint)
	if err != nil {
		return "", fmt.Errorf("parse authorization endpoint: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if meta.Resource != "" {
		q.Set("resource", meta.Resource)
	}
	if len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// Exchange redeems an authorization code.
func Exchange(ctx context.Context, client *http.Client, tokenEndpoint, clientID, clientSecret, code, redirectURI, verifier, resource string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	return tokenRequest(ctx, client, tokenEndpoint, clientSecret, form)
}

// Refresh exchanges a refresh token for a new access token.
func Refresh(ctx context.Context, client *http.Client, tokenEndpoint, clientID, clientSecret, refreshToken, resource string) (*Token, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	}
	if resource != "" {
		form.Set("resource", resource)
	}
	return tokenRequest(ctx, client, tokenEndpoint, clientSecret, form)
}

func tokenRequest(ctx context.Context, client *http.Client, endpoint, clientSecret string, form url.Values) (*Token, error) {
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	data, err := readPayload(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	var payload struct {
		Token
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(data, &payload) != nil {
		// GitHub answers form-encoded when the Accept header is ignored.
		values, parseErr := url.ParseQuery(string(data))
		if parseErr != nil {
			return nil, fmt.Errorf("token response: status %d, unreadable body", resp.StatusCode)
		}
		payload.AccessToken = values.Get("access_token")
		payload.RefreshToken = values.Get("refresh_token")
		payload.TokenType = values.Get("token_type")
		payload.Scope = values.Get("scope")
		payload.Error = values.Get("error")
		payload.ErrorDescription = values.Get("error_description")
		if v := values.Get("expires_in"); v != "" {
			fmt.Sscan(v, &payload.ExpiresIn)
		}
	}
	// GitHub reports OAuth errors with HTTP 200.
	if payload.Error != "" || resp.StatusCode != http.StatusOK {
		return nil, &TokenError{Status: resp.StatusCode, Code: payload.Error, Description: payload.ErrorDescription}
	}
	if payload.AccessToken == "" {
		return nil, errors.New("token response carries no access_token")
	}
	if payload.TokenType != "" && !strings.EqualFold(payload.TokenType, "bearer") {
		return nil, fmt.Errorf("unsupported token type %q", payload.TokenType)
	}
	tok := payload.Token
	return &tok, nil
}

// ProviderForURL derives the connection provider key for an MCP server from
// its host, e.g. https://api.githubcopilot.com/mcp/ → mcp-api-githubcopilot-com.
func ProviderForURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	host := ""
	if err == nil {
		host = strings.ToLower(u.Hostname())
	}
	var b strings.Builder
	b.WriteString("mcp-")
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// ValidProvider reports whether a provider key is safe to use as a
// connection provider and variable-name prefix.
func ValidProvider(p string) bool {
	if len(p) < 2 || len(p) > 64 {
		return false
	}
	for _, r := range p {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

func readPayload(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxMetadataBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMetadataBytes {
		return nil, errors.New("OAuth response exceeds size limit")
	}
	return data, nil
}
