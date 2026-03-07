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

	// Scope for inference via Pro/Max subscription.
	// Matches Claude Code CLI's inferenceOnly mode.
	ClaudeOAuthScopes = "user:inference"

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
// The server uses this to persist the new tokens to the store.
type TokenRefreshCallback func(ctx context.Context, accessToken, refreshToken string)

// OAuthTokenSource manages Claude OAuth tokens with automatic refresh.
// It caches the access token and refreshes it using the refresh token
// when it approaches expiry.
type OAuthTokenSource struct {
	mu           sync.Mutex
	accessToken  string
	refreshToken string
	expiresAt    time.Time
	httpClient   *http.Client
	onRefresh    TokenRefreshCallback // optional, called after successful refresh
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

// Token returns a valid access token, refreshing if necessary.
func (ts *OAuthTokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.accessToken != "" && time.Now().Before(ts.expiresAt.Add(-oauthTokenExpiryBuffer)) {
		return ts.accessToken, nil
	}

	if ts.refreshToken == "" {
		if ts.accessToken != "" {
			// Token expired but no refresh token — return stale token and hope for the best.
			return ts.accessToken, nil
		}

		return "", fmt.Errorf("no access token or refresh token available (authorize via Claude OAuth)")
	}

	return ts.refreshLocked(ctx)
}

// refreshLocked exchanges the refresh token for new tokens.
// Caller must hold ts.mu.
func (ts *OAuthTokenSource) refreshLocked(ctx context.Context) (string, error) {
	// Use JSON body matching the opencode-anthropic-auth plugin's refresh flow.
	body := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": ts.refreshToken,
		"client_id":     ClaudeOAuthClientID,
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal refresh request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ClaudeTokenURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return "", fmt.Errorf("build refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

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

	ts.accessToken = tokenResp.AccessToken
	if tokenResp.RefreshToken != "" {
		ts.refreshToken = tokenResp.RefreshToken
	}
	if tokenResp.ExpiresIn > 0 {
		ts.expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
	}

	slog.Debug("claude oauth token refreshed", "expires_in", tokenResp.ExpiresIn)

	// Notify the server to persist the new tokens.
	if ts.onRefresh != nil {
		ts.onRefresh(ctx, ts.accessToken, ts.refreshToken)
	}

	return ts.accessToken, nil
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

	// Use JSON body matching the Claude Code CLI's exchangeCodeForTokens.
	body := map[string]string{
		"grant_type":    "authorization_code",
		"code":          code,
		"client_id":     ClaudeOAuthClientID,
		"code_verifier": codeVerifier,
		"redirect_uri":  redirectURI,
	}
	if state != "" {
		body["state"] = state
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal token request: %w", err)
	}

	slog.Info("claude oauth token exchange",
		"token_url", ClaudeTokenURL,
		"code_len", len(code),
		"state_len", len(state),
		"verifier_len", len(codeVerifier),
		"redirect_uri", redirectURI,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ClaudeTokenURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

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
