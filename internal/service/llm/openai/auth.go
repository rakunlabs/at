package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	pat string

	mu           sync.Mutex
	cachedToken  string
	tokenExpires time.Time
}

// NewCopilotTokenSource creates a token source that exchanges the given GitHub
// OAuth token (or PAT) for short-lived Copilot JWTs via the GitHub token endpoint.
func NewCopilotTokenSource(pat string) *CopilotTokenSource {
	return &CopilotTokenSource{pat: pat}
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

	resp, err := http.DefaultClient.Do(req)
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
