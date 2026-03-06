package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/at/internal/service"
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
// GET /api/v1/oauth/start?provider=google&scopes=gmail.readonly,calendar
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

	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {callbackURL},
		"response_type": {"code"},
		"scope":         {scopes},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {providerName},
	}

	authURL := provider.AuthURL + "?" + params.Encode()

	httpResponseJSON(w, map[string]string{"url": authURL}, http.StatusOK)
}

// OAuthCallbackAPI handles the redirect from the OAuth2 provider.
// GET /api/v1/oauth/callback?code=...&state=google
func (s *Server) OAuthCallbackAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	providerName := r.URL.Query().Get("state")
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

	// Save refresh token as a variable.
	userEmail := s.getUserEmail(r)
	if err := s.oauthUpsertVar(r, provider.RefreshTokenVar, tokenResp.RefreshToken, true, userEmail); err != nil {
		slog.Error("failed to save refresh token", "provider", providerName, "error", err)
		renderOAuthResult(w, false, "failed to save refresh token: "+err.Error())
		return
	}

	slog.Info("oauth refresh token saved", "provider", providerName, "user", userEmail)
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
