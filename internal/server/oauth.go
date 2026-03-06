package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// oauthProviderConfig holds the well-known OAuth2 endpoints for a provider.
type oauthProviderConfig struct {
	AuthURL  string
	TokenURL string
	// Variable keys for client credentials.
	ClientIDVar     string
	ClientSecretVar string
	RefreshTokenVar string
	// Default scopes.
	DefaultScopes string
}

var oauthProviders = map[string]oauthProviderConfig{
	"google": {
		AuthURL:         "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:        "https://oauth2.googleapis.com/token",
		ClientIDVar:     "google_client_id",
		ClientSecretVar: "google_client_secret",
		RefreshTokenVar: "google_refresh_token",
		DefaultScopes:   "https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/calendar",
	},
}

// OAuthStartAPI returns the OAuth2 authorization URL for a provider.
// GET /api/v1/oauth/start?provider=google&scopes=gmail.readonly,calendar&user_id=discord::12345
//
// When user_id is provided, the resulting refresh token is stored as a per-user
// variable (e.g. "google_refresh_token::discord::12345") so that each chat user
// can connect their own Google account while sharing the same client_id/secret.
func (s *Server) OAuthStartAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	providerName := r.URL.Query().Get("provider")
	provider, ok := oauthProviders[providerName]
	if !ok {
		httpResponse(w, fmt.Sprintf("unknown oauth provider %q (supported: google)", providerName), http.StatusBadRequest)
		return
	}

	clientID, err := s.oauthGetVar(r, provider.ClientIDVar)
	if err != nil {
		httpResponse(w, fmt.Sprintf("variable %q not set — create it first", provider.ClientIDVar), http.StatusBadRequest)
		return
	}

	// Build callback URL from the incoming request.
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	callbackURL := scheme + "://" + r.Host + strings.TrimSuffix(s.config.BasePath, "/") + "/api/v1/oauth/callback"

	scopes := provider.DefaultScopes
	if scopeParam := r.URL.Query().Get("scopes"); scopeParam != "" {
		scopes = strings.ReplaceAll(scopeParam, ",", " ")
	}

	// Encode provider and optional user_id into state so the callback can
	// store the refresh token under the correct per-user key.
	state := providerName
	userID := r.URL.Query().Get("user_id")
	if userID != "" {
		state = providerName + "::" + userID
	}

	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {callbackURL},
		"response_type": {"code"},
		"scope":         {scopes},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {state},
	}

	authURL := provider.AuthURL + "?" + params.Encode()

	// If redirect=true (used by bot login links), redirect the browser directly.
	if r.URL.Query().Get("redirect") == "true" {
		http.Redirect(w, r, authURL, http.StatusFound)
		return
	}

	httpResponseJSON(w, map[string]string{"url": authURL}, http.StatusOK)
}

// OAuthCallbackAPI handles the redirect from the OAuth2 provider.
// GET /api/v1/oauth/callback?code=...&state=google  (global token)
// GET /api/v1/oauth/callback?code=...&state=google::discord::12345  (per-user token)
func (s *Server) OAuthCallbackAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	// Parse state: "provider" or "provider::user_id".
	state := r.URL.Query().Get("state")
	providerName, oauthUserID := parseOAuthState(state)
	provider, ok := oauthProviders[providerName]
	if !ok {
		renderOAuthResult(w, false, "unknown provider in state parameter")
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		errMsg := r.URL.Query().Get("error")
		if errMsg == "" {
			errMsg = "no code received"
		}
		renderOAuthResult(w, false, errMsg)
		return
	}

	clientID, err := s.oauthGetVar(r, provider.ClientIDVar)
	if err != nil {
		renderOAuthResult(w, false, fmt.Sprintf("variable %q not set", provider.ClientIDVar))
		return
	}
	clientSecret, err := s.oauthGetVar(r, provider.ClientSecretVar)
	if err != nil {
		renderOAuthResult(w, false, fmt.Sprintf("variable %q not set", provider.ClientSecretVar))
		return
	}

	// Reconstruct callback URL (must match the one used in authorize).
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	callbackURL := scheme + "://" + r.Host + strings.TrimSuffix(s.config.BasePath, "/") + "/api/v1/oauth/callback"

	// Exchange code for tokens.
	resp, err := http.PostForm(provider.TokenURL, url.Values{
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {callbackURL},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		slog.Error("oauth token exchange failed", "provider", providerName, "error", err)
		renderOAuthResult(w, false, "token exchange failed: "+err.Error())
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		renderOAuthResult(w, false, "invalid token response")
		return
	}
	if tokenResp.Error != "" {
		renderOAuthResult(w, false, tokenResp.Error+": "+tokenResp.ErrorDesc)
		return
	}
	if tokenResp.RefreshToken == "" {
		renderOAuthResult(w, false, "no refresh token received — try revoking access and re-authorizing")
		return
	}

	// Determine variable key: per-user or global.
	varKey := provider.RefreshTokenVar
	if oauthUserID != "" {
		varKey = provider.RefreshTokenVar + "::" + oauthUserID
	}

	// Save refresh token as a variable.
	userEmail := s.getUserEmail(r)
	if err := s.oauthUpsertVar(r, varKey, tokenResp.RefreshToken, true, userEmail); err != nil {
		slog.Error("failed to save refresh token", "provider", providerName, "error", err)
		renderOAuthResult(w, false, "failed to save refresh token: "+err.Error())
		return
	}

	logFields := []any{"provider", providerName, "user", userEmail}
	if oauthUserID != "" {
		logFields = append(logFields, "oauth_user_id", oauthUserID)
	}
	slog.Info("oauth refresh token saved", logFields...)
	renderOAuthResult(w, true, "")
}

// ─── Helpers ───

func (s *Server) oauthGetVar(r *http.Request, key string) (string, error) {
	v, err := s.variableStore.GetVariableByKey(r.Context(), key)
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", fmt.Errorf("variable %q not found", key)
	}
	return v.Value, nil
}

func (s *Server) oauthUpsertVar(r *http.Request, key, value string, secret bool, userEmail string) error {
	existing, _ := s.variableStore.GetVariableByKey(r.Context(), key)
	if existing != nil {
		existing.Value = value
		existing.UpdatedBy = userEmail
		_, err := s.variableStore.UpdateVariable(r.Context(), existing.ID, *existing)
		return err
	}

	_, err := s.variableStore.CreateVariable(r.Context(), service.Variable{
		Key:         key,
		Value:       value,
		Description: "Managed by OAuth flow",
		Secret:      secret,
		CreatedBy:   userEmail,
		UpdatedBy:   userEmail,
	})
	return err
}

// parseOAuthState splits a state string into provider and optional user_id.
// Format: "provider" or "provider::user_id".
func parseOAuthState(state string) (provider, userID string) {
	if idx := strings.Index(state, "::"); idx != -1 {
		return state[:idx], state[idx+2:]
	}
	return state, ""
}

// userScopedVarLookup returns a VarLookup that checks for a per-user variable
// first (key + "::" + userID), then falls back to the global variable.
// If userID is empty, it behaves like a normal global lookup.
func (s *Server) userScopedVarLookup(ctx context.Context, userID string) workflow.VarLookup {
	if s.variableStore == nil {
		return nil
	}
	return func(key string) (string, error) {
		// Try per-user variable first.
		if userID != "" {
			scopedKey := key + "::" + userID
			v, err := s.variableStore.GetVariableByKey(ctx, scopedKey)
			if err == nil && v != nil {
				return v.Value, nil
			}
		}
		// Fall back to global variable.
		v, err := s.variableStore.GetVariableByKey(ctx, key)
		if err != nil {
			return "", err
		}
		if v == nil {
			return "", fmt.Errorf("variable %q not found", key)
		}
		return v.Value, nil
	}
}

// buildOAuthLoginURL builds the full OAuth start URL for a bot user.
// Returns empty string if ExternalURL is not configured or client_id is missing.
func (s *Server) buildOAuthLoginURL(ctx context.Context, provider, platform, platformUserID string) string {
	if s.config.ExternalURL == "" {
		return ""
	}

	// Verify client_id is set for this provider.
	providerCfg, ok := oauthProviders[provider]
	if !ok {
		return ""
	}
	if s.variableStore == nil {
		return ""
	}
	v, err := s.variableStore.GetVariableByKey(ctx, providerCfg.ClientIDVar)
	if err != nil || v == nil {
		return ""
	}

	base := strings.TrimSuffix(s.config.ExternalURL, "/") + strings.TrimSuffix(s.config.BasePath, "/")
	userID := platform + "::" + platformUserID

	params := url.Values{
		"provider": {provider},
		"user_id":  {userID},
		"redirect": {"true"},
	}

	return base + "/api/v1/oauth/start?" + params.Encode()
}

func renderOAuthResult(w http.ResponseWriter, success bool, errMsg string) {
	status := "success"
	message := "Account connected successfully! You can close this window."
	if !success {
		status = "error"
		message = "OAuth failed: " + errMsg
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>OAuth</title></head>
<body>
<p>%s</p>
<script>
if (window.opener) {
  window.opener.postMessage({type: "oauth-result", status: "%s"}, "*");
  setTimeout(function() { window.close(); }, 2000);
}
</script>
</body></html>`, message, status)
}
