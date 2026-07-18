package openai

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// TokenSource provides a bearer token for per-request authentication.
// Implementations handle caching and refreshing transparently.
type TokenSource interface {
	// Token returns a valid bearer token.
	// It may cache tokens and refresh them as needed.
	Token(ctx context.Context) (string, error)
}

// Official Codex OAuth application and endpoints used by the Codex CLI.
const (
	CodexOAuthClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	CodexDefaultAuthBaseURL = "https://auth.openai.com"
	CodexDeviceAuthTimeout  = 15 * time.Minute
	codexTokenExpiryBuffer  = 5 * time.Minute
)

// CodexAuthEndpoints makes every OAuth endpoint replaceable by unit tests and
// private OpenAI-compatible deployments. Empty fields use the official URLs.
type CodexAuthEndpoints struct {
	DeviceUserCodeURL string
	DeviceTokenURL    string
	DeviceCallbackURL string
	OAuthTokenURL     string
	VerificationURL   string
}

// DefaultCodexAuthEndpoints returns the endpoints used by the official Codex CLI.
func DefaultCodexAuthEndpoints() CodexAuthEndpoints {
	return CodexAuthEndpoints{
		DeviceUserCodeURL: CodexDefaultAuthBaseURL + "/api/accounts/deviceauth/usercode",
		DeviceTokenURL:    CodexDefaultAuthBaseURL + "/api/accounts/deviceauth/token",
		DeviceCallbackURL: CodexDefaultAuthBaseURL + "/deviceauth/callback",
		OAuthTokenURL:     CodexDefaultAuthBaseURL + "/oauth/token",
		VerificationURL:   CodexDefaultAuthBaseURL + "/codex/device",
	}
}

func resolveCodexAuthEndpoints(overrides []CodexAuthEndpoints) CodexAuthEndpoints {
	defaults := DefaultCodexAuthEndpoints()
	if len(overrides) == 0 {
		return defaults
	}
	o := overrides[0]
	if o.DeviceUserCodeURL != "" {
		defaults.DeviceUserCodeURL = o.DeviceUserCodeURL
	}
	if o.DeviceTokenURL != "" {
		defaults.DeviceTokenURL = o.DeviceTokenURL
	}
	if o.DeviceCallbackURL != "" {
		defaults.DeviceCallbackURL = o.DeviceCallbackURL
	}
	if o.OAuthTokenURL != "" {
		defaults.OAuthTokenURL = o.OAuthTokenURL
	}
	if o.VerificationURL != "" {
		defaults.VerificationURL = o.VerificationURL
	}
	return defaults
}

// CodexDeviceCode is the state returned by the first step of device login.
type CodexDeviceCode struct {
	VerificationURL string
	UserCode        string
	DeviceAuthID    string
	Interval        time.Duration
}

type codexDeviceCodeResponse struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	UserCodeAlt  string          `json:"usercode"`
	Interval     json.RawMessage `json:"interval"`
}

// CodexTokens contains the tokens produced by device login or a refresh.
type CodexTokens struct {
	IDToken      string    `json:"id_token"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresIn    int       `json:"expires_in,omitempty"`
	AccountID    string    `json:"-"`
	ExpiresAt    time.Time `json:"-"`
}

// RequestCodexDeviceCode starts the official Codex device authorization flow.
func RequestCodexDeviceCode(ctx context.Context, httpClient *http.Client, endpoints ...CodexAuthEndpoints) (*CodexDeviceCode, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	e := resolveCodexAuthEndpoints(endpoints)
	body, err := json.Marshal(map[string]string{"client_id": CodexOAuthClientID})
	if err != nil {
		return nil, fmt.Errorf("marshal Codex device-code request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.DeviceUserCodeURL, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("build Codex device-code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	respBody, status, err := doCodexAuthRequest(httpClient, req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Codex device-code request returned %d: %s", status, truncate(string(respBody), 300))
	}

	var result codexDeviceCodeResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse Codex device-code response: %w", err)
	}
	if result.UserCode == "" {
		result.UserCode = result.UserCodeAlt
	}
	if result.DeviceAuthID == "" || result.UserCode == "" {
		return nil, fmt.Errorf("Codex device-code response is missing device_auth_id or user_code")
	}
	interval, err := parseCodexDeviceInterval(result.Interval)
	if err != nil {
		return nil, err
	}
	return &CodexDeviceCode{
		VerificationURL: e.VerificationURL,
		UserCode:        result.UserCode,
		DeviceAuthID:    result.DeviceAuthID,
		Interval:        interval,
	}, nil
}

func parseCodexDeviceInterval(raw json.RawMessage) (time.Duration, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 5 * time.Second, nil
	}
	var seconds int64
	if err := json.Unmarshal(raw, &seconds); err != nil {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, fmt.Errorf("parse Codex device-code interval: %w", err)
		}
		if _, err := fmt.Sscan(strings.TrimSpace(value), &seconds); err != nil {
			return 0, fmt.Errorf("parse Codex device-code interval %q: %w", value, err)
		}
	}
	if seconds < 0 {
		return 0, fmt.Errorf("Codex device-code interval cannot be negative")
	}
	if seconds == 0 {
		seconds = 1
	}
	return time.Duration(seconds) * time.Second, nil
}

// CompleteCodexDeviceAuth polls the device endpoint and exchanges the returned
// authorization code for access, refresh, and ID tokens.
func CompleteCodexDeviceAuth(ctx context.Context, device *CodexDeviceCode, httpClient *http.Client, endpoints ...CodexAuthEndpoints) (*CodexTokens, error) {
	if device == nil {
		return nil, fmt.Errorf("Codex device code is required")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	e := resolveCodexAuthEndpoints(endpoints)
	pollCtx, cancel := context.WithTimeout(ctx, CodexDeviceAuthTimeout)
	defer cancel()

	for {
		body, err := json.Marshal(map[string]string{
			"device_auth_id": device.DeviceAuthID,
			"user_code":      device.UserCode,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal Codex device-token request: %w", err)
		}
		req, err := http.NewRequestWithContext(pollCtx, http.MethodPost, e.DeviceTokenURL, strings.NewReader(string(body)))
		if err != nil {
			return nil, fmt.Errorf("build Codex device-token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		respBody, status, err := doCodexAuthRequest(httpClient, req)
		if err != nil {
			return nil, err
		}

		if status >= 200 && status < 300 {
			var code struct {
				AuthorizationCode string `json:"authorization_code"`
				CodeVerifier      string `json:"code_verifier"`
			}
			if err := json.Unmarshal(respBody, &code); err != nil {
				return nil, fmt.Errorf("parse Codex device-token response: %w", err)
			}
			if code.AuthorizationCode == "" || code.CodeVerifier == "" {
				return nil, fmt.Errorf("Codex device-token response is missing authorization_code or code_verifier")
			}
			return ExchangeCodexAuthorizationCode(pollCtx, code.AuthorizationCode, code.CodeVerifier, httpClient, e)
		}
		if status != http.StatusForbidden && status != http.StatusNotFound {
			return nil, fmt.Errorf("Codex device-token request returned %d: %s", status, truncate(string(respBody), 300))
		}

		timer := time.NewTimer(device.Interval)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			return nil, fmt.Errorf("Codex device authorization did not complete: %w", pollCtx.Err())
		case <-timer.C:
		}
	}
}

// ExchangeCodexAuthorizationCode performs the PKCE token exchange used by the
// device callback flow.
func ExchangeCodexAuthorizationCode(ctx context.Context, code, codeVerifier string, httpClient *http.Client, endpoints ...CodexAuthEndpoints) (*CodexTokens, error) {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	e := resolveCodexAuthEndpoints(endpoints)
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {e.DeviceCallbackURL},
		"client_id":     {CodexOAuthClientID},
		"code_verifier": {codeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.OAuthTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build Codex token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	respBody, status, err := doCodexAuthRequest(httpClient, req)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Codex token exchange returned %d: %s", status, truncate(string(respBody), 300))
	}
	return parseCodexTokens(respBody)
}

func doCodexAuthRequest(httpClient *http.Client, req *http.Request) ([]byte, int, error) {
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("Codex auth request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read Codex auth response: %w", err)
	}
	return body, resp.StatusCode, nil
}

func parseCodexTokens(data []byte) (*CodexTokens, error) {
	var tokens CodexTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parse Codex token response: %w", err)
	}
	if tokens.AccessToken == "" {
		return nil, fmt.Errorf("Codex token response returned an empty access token")
	}
	tokens.AccountID, _ = ExtractCodexAccountID(tokens.IDToken)
	if tokens.AccountID == "" {
		tokens.AccountID, _ = ExtractCodexAccountID(tokens.AccessToken)
	}
	if tokens.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	} else {
		tokens.ExpiresAt, _ = codexJWTExpiration(tokens.AccessToken)
	}
	return &tokens, nil
}

func parseCodexRefreshTokens(data []byte) (*CodexTokens, error) {
	var tokens CodexTokens
	if err := json.Unmarshal(data, &tokens); err != nil {
		return nil, fmt.Errorf("parse Codex token refresh response: %w", err)
	}
	if tokens.IDToken != "" {
		tokens.AccountID, _ = ExtractCodexAccountID(tokens.IDToken)
	}
	if tokens.AccountID == "" && tokens.AccessToken != "" {
		tokens.AccountID, _ = ExtractCodexAccountID(tokens.AccessToken)
	}
	if tokens.ExpiresIn > 0 {
		tokens.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
	} else if tokens.AccessToken != "" {
		tokens.ExpiresAt, _ = codexJWTExpiration(tokens.AccessToken)
	}
	return &tokens, nil
}

func codexJWTClaims(token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil, fmt.Errorf("invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("parse JWT payload: %w", err)
	}
	return claims, nil
}

// ExtractCodexAccountID reads chatgpt_account_id from the official nested
// https://api.openai.com/auth claim.
func ExtractCodexAccountID(token string) (string, error) {
	claims, err := codexJWTClaims(token)
	if err != nil {
		return "", err
	}
	auth, ok := claims["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("JWT is missing https://api.openai.com/auth claim")
	}
	accountID, _ := auth["chatgpt_account_id"].(string)
	if accountID == "" {
		return "", fmt.Errorf("JWT is missing chatgpt_account_id claim")
	}
	return accountID, nil
}

func codexJWTExpiration(token string) (time.Time, error) {
	claims, err := codexJWTClaims(token)
	if err != nil {
		return time.Time{}, err
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		return time.Time{}, fmt.Errorf("JWT is missing exp claim")
	}
	return time.Unix(int64(exp), 0), nil
}

// CodexTokenRefreshCallback is invoked after a successful refresh so callers
// can persist the rotated credentials and derived account identifier.
type CodexTokenRefreshCallback func(ctx context.Context, accessToken, refreshToken, accountID string, expiresAt time.Time) error

// CodexTokenReloadCallback reloads credentials persisted by another process.
type CodexTokenReloadCallback func(ctx context.Context) (accessToken, refreshToken, accountID string, expiresAt time.Time, err error)

// CodexTokenSource provides an access token and refreshes it with the official
// JSON refresh-token exchange when it approaches expiry.
type CodexTokenSource struct {
	mu             sync.Mutex
	accessToken    string
	refreshToken   string
	idToken        string
	accountID      string
	expiresAt      time.Time
	httpClient     *http.Client
	endpoints      CodexAuthEndpoints
	onRefresh      CodexTokenRefreshCallback
	onReload       CodexTokenReloadCallback
	persistPending bool
}

// NewCodexTokenSource creates a refreshable ChatGPT Codex token source.
func NewCodexTokenSource(accessToken, refreshToken, accountID string, expiresAt time.Time, httpClient *http.Client, endpoints ...CodexAuthEndpoints) *CodexTokenSource {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if expiresAt.IsZero() {
		expiresAt, _ = codexJWTExpiration(accessToken)
	}
	if accountID == "" {
		accountID, _ = ExtractCodexAccountID(accessToken)
	}
	return &CodexTokenSource{
		accessToken:  accessToken,
		refreshToken: refreshToken,
		accountID:    accountID,
		expiresAt:    expiresAt,
		httpClient:   httpClient,
		endpoints:    resolveCodexAuthEndpoints(endpoints),
	}
}

// SetRefreshCallback sets or replaces the persistence callback.
func (ts *CodexTokenSource) SetRefreshCallback(fn CodexTokenRefreshCallback) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.onRefresh = fn
}

// SetReloadCallback sets the callback used to recover credentials after a 401.
func (ts *CodexTokenSource) SetReloadCallback(fn CodexTokenReloadCallback) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.onReload = fn
}

// AccountID returns the account identifier associated with the current token.
func (ts *CodexTokenSource) AccountID() string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.accountID
}

// Token returns a current Codex access token, refreshing it when necessary.
func (ts *CodexTokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	if ts.persistPending {
		if err := ts.persistLocked(ctx); err != nil {
			ts.mu.Unlock()
			return "", err
		}
	}
	if ts.accessToken != "" && (ts.expiresAt.IsZero() || time.Now().Before(ts.expiresAt.Add(-codexTokenExpiryBuffer))) {
		token := ts.accessToken
		ts.mu.Unlock()
		return token, nil
	}
	if ts.refreshToken == "" {
		ts.mu.Unlock()
		return "", fmt.Errorf("Codex access token expired and no refresh token is available")
	}

	body, err := json.Marshal(map[string]string{
		"client_id":     CodexOAuthClientID,
		"grant_type":    "refresh_token",
		"refresh_token": ts.refreshToken,
	})
	if err != nil {
		ts.mu.Unlock()
		return "", fmt.Errorf("marshal Codex token refresh request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.endpoints.OAuthTokenURL, strings.NewReader(string(body)))
	if err != nil {
		ts.mu.Unlock()
		return "", fmt.Errorf("build Codex token refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	respBody, status, err := doCodexAuthRequest(ts.httpClient, req)
	if err != nil {
		ts.mu.Unlock()
		return "", err
	}
	if status < 200 || status >= 300 {
		ts.mu.Unlock()
		return "", fmt.Errorf("Codex token refresh returned %d: %s", status, truncate(string(respBody), 300))
	}
	tokens, err := parseCodexRefreshTokens(respBody)
	if err != nil {
		ts.mu.Unlock()
		return "", err
	}
	accessToken := ts.accessToken
	if tokens.AccessToken != "" {
		accessToken = tokens.AccessToken
	}
	refreshToken := ts.refreshToken
	if tokens.RefreshToken != "" {
		refreshToken = tokens.RefreshToken
	}
	accountID := ts.accountID
	if accountID == "" {
		accountID = tokens.AccountID
	}
	expiresAt := ts.expiresAt
	if !tokens.ExpiresAt.IsZero() {
		expiresAt = tokens.ExpiresAt
	}

	ts.accessToken = accessToken
	ts.refreshToken = refreshToken
	ts.idToken = tokens.IDToken
	ts.accountID = accountID
	ts.expiresAt = expiresAt
	ts.persistPending = ts.onRefresh != nil
	if err := ts.persistLocked(ctx); err != nil {
		ts.mu.Unlock()
		return "", err
	}
	ts.mu.Unlock()
	return accessToken, nil
}

func (ts *CodexTokenSource) persistLocked(ctx context.Context) error {
	if !ts.persistPending || ts.onRefresh == nil {
		return nil
	}
	if err := ts.onRefresh(ctx, ts.accessToken, ts.refreshToken, ts.accountID, ts.expiresAt); err != nil {
		return fmt.Errorf("persist refreshed Codex tokens: %w", err)
	}
	ts.persistPending = false
	return nil
}

// Invalidate forces the next Token call to refresh the access token.
func (ts *CodexTokenSource) Invalidate() {
	ts.mu.Lock()
	ts.expiresAt = time.Unix(1, 0)
	ts.mu.Unlock()
}

// Reload replaces local credentials with the latest persisted values.
func (ts *CodexTokenSource) Reload(ctx context.Context) (bool, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.onReload == nil {
		return false, nil
	}
	accessToken, refreshToken, accountID, expiresAt, err := ts.onReload(ctx)
	if err != nil {
		return false, fmt.Errorf("reload Codex credentials: %w", err)
	}
	if accessToken == "" {
		return false, fmt.Errorf("reload Codex credentials: persisted access token is empty")
	}
	if ts.accountID != "" && accountID != "" && ts.accountID != accountID {
		return false, fmt.Errorf("reload Codex credentials: ChatGPT account ID changed")
	}
	ts.accessToken = accessToken
	ts.refreshToken = refreshToken
	if ts.accountID == "" {
		ts.accountID = accountID
	}
	ts.expiresAt = expiresAt
	ts.persistPending = false
	return true, nil
}

// ─── GitHub Copilot Token Source ───
//
// CopilotTokenSource exchanges a GitHub OAuth token (obtained via the device
// flow) or a GitHub PAT for a short-lived Copilot JWT. The OAuth token is
// stored in the provider's APIKey field and is obtained by the device-auth
// endpoint in the server package.

const (
	copilotTokenEndpoint = "https://api.github.com/copilot_internal/v2/token"

	// Refresh the token 5 minutes before it actually expires.
	copilotTokenExpiryBuffer = 5 * time.Minute
)

// copilotTokenResponse is the JSON returned by the Copilot token exchange endpoint.
type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
}

// CopilotTokenSource exchanges a GitHub OAuth token (from the device flow) or
// a GitHub PAT for a short-lived JWT that the Copilot API accepts. Tokens are
// cached and automatically refreshed before they expire.
type CopilotTokenSource struct {
	pat        string
	httpClient *http.Client

	mu           sync.Mutex
	cachedToken  string
	tokenExpires time.Time
}

// NewCopilotTokenSource creates a token source that exchanges the given GitHub
// OAuth token (or PAT) for short-lived Copilot JWTs via the GitHub token endpoint.
// An optional *http.Client can be provided to route token-exchange requests
// through a proxy; when nil, http.DefaultClient is used.
func NewCopilotTokenSource(pat string, httpClient *http.Client) *CopilotTokenSource {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &CopilotTokenSource{pat: pat, httpClient: httpClient}
}

// Token returns a valid Copilot JWT, refreshing if necessary.
func (ts *CopilotTokenSource) Token(ctx context.Context) (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.cachedToken != "" && time.Now().Before(ts.tokenExpires.Add(-copilotTokenExpiryBuffer)) {
		return ts.cachedToken, nil
	}

	return ts.refreshLocked(ctx)
}

// refreshLocked calls the token exchange endpoint. Must be called with ts.mu held.
func (ts *CopilotTokenSource) refreshLocked(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, copilotTokenEndpoint, nil)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Authorization", "token "+ts.pat)
	req.Header.Set("User-Agent", "GithubCopilot/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := ts.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, truncate(string(body), 300))
	}

	var tokenResp copilotTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.Token == "" {
		return "", fmt.Errorf("token exchange returned empty token")
	}

	ts.cachedToken = tokenResp.Token
	ts.tokenExpires = time.Unix(tokenResp.ExpiresAt, 0)

	return ts.cachedToken, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ProxyHTTPClient returns an *http.Client configured with the given proxy URL
// and TLS settings. If proxy is empty, it returns nil (callers should fall back
// to http.DefaultClient).
func ProxyHTTPClient(proxy string, insecureSkipVerify bool) (*http.Client, error) {
	if proxy == "" && !insecureSkipVerify {
		return nil, nil
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()

	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}

	if insecureSkipVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}

	return &http.Client{Transport: transport}, nil
}
