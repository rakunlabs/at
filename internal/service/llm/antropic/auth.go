package antropic

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ─── Constants ───

const (
	// Claude Code OAuth application (public client, no secret required).
	ClaudeOAuthClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"

	// OAuth endpoints.
	ClaudeAuthURL   = "https://claude.ai/oauth/authorize"
	ClaudeTokenURL  = "https://platform.claude.com/v1/oauth/token"
	ClaudeManualURI = "https://platform.claude.com/oauth/code/callback"

	// Scopes for inference via Pro/Max subscription.
	// Matches Claude Code CLI and opencode-anthropic-oauth plugin.
	ClaudeOAuthScopes = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"

	// Refresh tokens 5 minutes before expiry to avoid edge-case failures.
	oauthTokenExpiryBuffer = 5 * time.Minute
)

// ─── TokenSource interface ───

// TokenSource provides a bearer token for per-request authentication.
// Implementations handle caching and refreshing transparently.
type TokenSource interface {
	// Token returns a valid bearer token.
	// It may cache tokens and refresh them as needed.
	Token(ctx context.Context) (string, error)
}

// ─── StaticTokenSource ───

// StaticTokenSource returns a fixed token on every call.
type StaticTokenSource struct {
	token string
}

// NewStaticTokenSource creates a TokenSource that always returns the given token.
func NewStaticTokenSource(token string) *StaticTokenSource {
	return &StaticTokenSource{token: token}
}

// Token implements TokenSource.
func (s *StaticTokenSource) Token(_ context.Context) (string, error) {
	return s.token, nil
}

// ─── OAuthTokenSource ───

// TokenRefreshCallback is called after a successful token refresh.
// The server uses this to persist the new tokens (and expiry) to the store
// so that subsequent restarts have a fresh access token and don't have to
// burn an extra refresh round trip on boot.
//
// expiresAt is the absolute expiry time of the new access token. A zero
// value means "unknown" (e.g. the upstream did not return expires_in).
type TokenRefreshCallback func(ctx context.Context, previousRefreshToken, accessToken, refreshToken string, expiresAt time.Time) error

// OAuthTokens carries a durable Claude credential set between a token source
// and the coordinator that owns the provider row lock.
type OAuthTokens struct {
	AccessToken, RefreshToken string
	ExpiresAt                 time.Time
	PreviousRefreshToken      string
}

// TokenCoordinator runs the refresh exchange while holding the durable provider
// lock, so it always observes the currently stored credential. Anthropic
// invalidates the previous refresh token on every exchange; without this,
// independent sources for the same provider (gateway registry, execution cache,
// model discovery, other replicas) each rotate from their own in-memory copy and
// the loser gets `400 invalid_grant`. It may return exchanged credentials
// alongside a persistence error; the source retains them and retries its refresh
// callback before any subsequent exchange.
type TokenCoordinator func(context.Context, func(context.Context, OAuthTokens) (*OAuthTokens, error)) (*OAuthTokens, error)

// TokenFresh reports whether an access token can still be used as-is. An
// unknown expiry is treated as expired so the credential is revalidated.
func TokenFresh(access string, expiry time.Time) bool {
	return access != "" && !expiry.IsZero() && time.Now().Before(expiry.Add(-oauthTokenExpiryBuffer))
}

// OAuthTokenSource manages Claude OAuth tokens with automatic refresh.
// It caches the access token and refreshes it using the refresh token
// when it approaches expiry.
type OAuthTokenSource struct {
	mu             sync.Mutex
	accessToken    string
	refreshToken   string
	expiresAt      time.Time
	httpClient     *http.Client
	onRefresh      TokenRefreshCallback // optional, called after successful refresh
	coordinator    TokenCoordinator     // optional, owns durable reload + persistence
	pendingRefresh string               // previous token whose rotation still needs persistence
}

// NewOAuthTokenSource creates a token source that handles automatic refresh.
// If onRefresh is non-nil, it is called after every successful token refresh
// so the caller can persist the new access and refresh tokens.
func NewOAuthTokenSource(accessToken, refreshToken string, expiresAt time.Time, httpClient *http.Client, onRefresh TokenRefreshCallback) *OAuthTokenSource {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &OAuthTokenSource{
		accessToken:  accessToken,
		refreshToken: refreshToken,
		expiresAt:    expiresAt,
		httpClient:   httpClient,
		onRefresh:    onRefresh,
	}
}

// SetRefreshCallback sets (or replaces) the callback invoked after token refresh.
// This allows the server to wire up persistence after the provider is created.
func (ts *OAuthTokenSource) SetRefreshCallback(fn TokenRefreshCallback) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.onRefresh = fn
}

// SetCoordinator sets (or replaces) the durable refresh coordinator.
func (ts *OAuthTokenSource) SetCoordinator(fn TokenCoordinator) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.coordinator = fn
}

// Token returns a valid access token, refreshing if necessary.
func (ts *OAuthTokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if err := ts.persistLocked(ctx); err != nil {
		return "", err
	}

	if TokenFresh(ts.accessToken, ts.expiresAt) {
		token := ts.accessToken
		return token, nil
	}

	// The coordinator reloads under the provider row lock, so it can adopt a
	// credential another source already refreshed instead of spending this
	// source's (possibly consumed) refresh token.
	if ts.coordinator != nil {
		return ts.coordinatedRefreshLocked(ctx)
	}

	if ts.refreshToken == "" {
		if ts.accessToken != "" {
			// Token expired but no refresh token — log a warning so the operator
			// knows auth may fail, then return the stale token as a best-effort
			// fallback. This is better than a hard error because short-lived
			// clock skew or server-side grace periods may still accept the token.
			slog.Warn("anthropic oauth: access token expired and no refresh token available, using stale token")
			token := ts.accessToken
			return token, nil
		}

		return "", fmt.Errorf("no access token or refresh token available (authorize via Claude OAuth)")
	}

	return ts.refreshLocked(ctx)
}

// coordinatedRefreshLocked delegates reload, exchange and persistence to the
// coordinator. The inner source performs only the exchange; it carries no
// callback of its own so the single-use credential is rotated exactly once.
func (ts *OAuthTokenSource) coordinatedRefreshLocked(ctx context.Context) (string, error) {
	tokens, err := ts.coordinator(ctx, func(exchangeCtx context.Context, current OAuthTokens) (*OAuthTokens, error) {
		if current.RefreshToken == "" {
			return nil, fmt.Errorf("no refresh token available (authorize via Claude OAuth)")
		}
		inner := NewOAuthTokenSource("", current.RefreshToken, time.Time{}, ts.httpClient, nil)
		if _, err := inner.Token(exchangeCtx); err != nil {
			return nil, err
		}
		return &OAuthTokens{
			AccessToken:          inner.accessToken,
			RefreshToken:         inner.refreshToken,
			ExpiresAt:            inner.expiresAt,
			PreviousRefreshToken: current.RefreshToken,
		}, nil
	})
	if tokens != nil && tokens.AccessToken != "" {
		ts.accessToken, ts.refreshToken, ts.expiresAt = tokens.AccessToken, tokens.RefreshToken, tokens.ExpiresAt
		// Persistence is the coordinator's job. Only fall back to the legacy
		// callback when the coordinator exchanged but failed to save, so the
		// rotated credential is not lost.
		if err != nil && tokens.PreviousRefreshToken != "" && ts.onRefresh != nil {
			ts.pendingRefresh = tokens.PreviousRefreshToken
		}
	}
	if err != nil {
		return "", err
	}
	return ts.accessToken, nil
}

// refreshLocked exchanges the refresh token for new tokens.
// Caller holds ts.mu through rotation and persistence. Failed persistence is
// retried before another token is issued, without rotating the token again.
func (ts *OAuthTokenSource) refreshLocked(ctx context.Context) (string, error) {
	// Use form-encoded body matching the OpenCode anthropic-oauth plugin.
	// IMPORTANT: Anthropic's token endpoint returns different token capabilities
	// depending on Content-Type. Form-encoded produces tokens that work with
	// tools+thinking; JSON-encoded tokens may not.
	formValues := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {ts.refreshToken},
		"client_id":     {ClaudeOAuthClientID},
	}
	formBody := formValues.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ClaudeTokenURL, strings.NewReader(formBody))
	if err != nil {
		return "", fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "claude-cli/"+claudeCodeCLIVersion+" (external, cli)")

	resp, err := ts.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("refresh token request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token refresh returned %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return "", fmt.Errorf("parse refresh response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("token refresh returned empty access token")
	}

	ts.pendingRefresh = ts.refreshToken
	ts.accessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		ts.refreshToken = tokenResp.RefreshToken
	}
	if tokenResp.ExpiresIn > 0 {
		ts.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	slog.Debug("claude oauth token refreshed", "expires_in", tokenResp.ExpiresIn)
	if err := ts.persistLocked(ctx); err != nil {
		return "", err
	}
	return ts.accessToken, nil
}

func (ts *OAuthTokenSource) persistLocked(ctx context.Context) error {
	if ts.pendingRefresh == "" {
		return nil
	}
	if ts.onRefresh != nil {
		if err := ts.onRefresh(ctx, ts.pendingRefresh, ts.accessToken, ts.refreshToken, ts.expiresAt); err != nil {
			return fmt.Errorf("persist refreshed Claude OAuth tokens: %w", err)
		}
	}
	ts.pendingRefresh = ""
	return nil
}

// ─── Token types ───

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

// ─── Auth Code Exchange ───

// ExchangeAuthCode exchanges an authorization code for OAuth tokens.
// The code may contain a "#state" suffix from the callback page; if so,
// the state is extracted and sent in the token request (required by Anthropic).
func ExchangeAuthCode(ctx context.Context, code, codeVerifier, redirectURI string, httpClient *http.Client) (*oauthTokenResponse, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// The callback page returns "code#state" — split them.
	var state string
	if idx := strings.IndexByte(code, '#'); idx >= 0 {
		state = code[idx+1:]
		code = code[:idx]
	}

	// Use form-encoded body matching the OpenCode anthropic-oauth plugin.
	// IMPORTANT: Anthropic's token endpoint returns different token capabilities
	// depending on Content-Type. Form-encoded produces tokens that work correctly
	// with tools+thinking; JSON-encoded tokens may not.
	formValues := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {ClaudeOAuthClientID},
		"code_verifier": {codeVerifier},
		"redirect_uri":  {redirectURI},
	}
	if state != "" {
		formValues.Set("state", state)
	}
	formBody := formValues.Encode()

	slog.Info("claude oauth token exchange",
		"token_url", ClaudeTokenURL,
		"code_len", len(code),
		"state_len", len(state),
		"verifier_len", len(codeVerifier),
		"redirect_uri", redirectURI,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ClaudeTokenURL, strings.NewReader(formBody))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "claude-cli/"+claudeCodeCLIVersion+" (external, cli)")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, truncate(string(respBody), 300))
	}

	var tokenResp oauthTokenResponse
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token exchange returned empty access token")
	}

	return &tokenResp, nil
}

// ─── PKCE ───

// PKCEChallenge holds a PKCE code verifier and its S256 challenge.
type PKCEChallenge struct {
	Verifier  string
	Challenge string
}

// GeneratePKCE creates a random PKCE code verifier and its S256 challenge.
func GeneratePKCE() (*PKCEChallenge, error) {
	// 32 bytes of randomness → 43-char base64url string (per RFC 7636).
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("generate random bytes: %w", err)
	}

	verifier := base64.RawURLEncoding.EncodeToString(buf)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEChallenge{
		Verifier:  verifier,
		Challenge: challenge,
	}, nil
}

// BuildAuthURL constructs the full OAuth authorization URL.
// Builds the query string manually to preserve exact parameter order
// matching the opencode-anthropic-auth plugin (Anthropic's OAuth server
// may be sensitive to parameter ordering).
func BuildAuthURL(pkceChallenge, state string) string {
	params := url.Values{}

	// Encode each value individually using url.Values for proper percent-encoding,
	// then assemble in the exact order that the working OpenCode plugin uses.
	encode := func(v string) string {
		params.Set("k", v)
		// url.Values.Encode() produces "k=<encoded>", strip the "k=" prefix.
		return params.Encode()[2:]
	}

	raw := "code=true" +
		"&client_id=" + encode(ClaudeOAuthClientID) +
		"&response_type=code" +
		"&redirect_uri=" + encode(ClaudeManualURI) +
		"&scope=" + encode(ClaudeOAuthScopes) +
		"&code_challenge=" + encode(pkceChallenge) +
		"&code_challenge_method=S256" +
		"&state=" + encode(state)

	return ClaudeAuthURL + "?" + raw
}

// ─── Helpers ───

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}

	return s[:max] + "..."
}
