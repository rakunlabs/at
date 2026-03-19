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
	"youtube": {
		AuthURL:         "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:        "https://oauth2.googleapis.com/token",
		ClientIDVar:     "youtube_client_id",
		ClientSecretVar: "youtube_client_secret",
		RefreshTokenVar: "youtube_refresh_token",
		DefaultScopes:   "https://www.googleapis.com/auth/youtube.upload https://www.googleapis.com/auth/youtube",
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

// OAuthManualAuthURLAPI returns an auth URL using a special redirect URI that shows the code.
// GET /api/v1/oauth/manual-url?provider=youtube
// This is for cases where the standard redirect URI doesn't work (localhost issues, etc.)
// The redirect goes to AT's own /api/v1/oauth/code-display page which shows the code to copy.
func (s *Server) OAuthManualAuthURLAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	providerName := r.URL.Query().Get("provider")
	provider, ok := oauthProviders[providerName]
	if !ok {
		httpResponse(w, fmt.Sprintf("unknown oauth provider %q", providerName), http.StatusBadRequest)
		return
	}

	clientID, err := s.oauthGetVar(r, provider.ClientIDVar)
	if err != nil {
		httpResponse(w, fmt.Sprintf("variable %q not set", provider.ClientIDVar), http.StatusBadRequest)
		return
	}

	scopes := provider.DefaultScopes

	// Use AT's own code-display page as the redirect URI.
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	redirectURI := scheme + "://" + r.Host + strings.TrimSuffix(s.config.BasePath, "/") + "/api/v1/oauth/code-display"

	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {scopes},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
		"state":         {providerName},
	}

	authURL := provider.AuthURL + "?" + params.Encode()

	httpResponseJSON(w, map[string]any{
		"url":          authURL,
		"redirect_uri": redirectURI,
		"provider":     providerName,
	}, http.StatusOK)
}

// OAuthCodeDisplayAPI is a redirect target that shows the authorization code to the user.
// GET /api/v1/oauth/code-display?code=...&state=...
// This page displays the code so the user can copy it back to the AT Connections page.
func (s *Server) OAuthCodeDisplayAPI(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	errMsg := r.URL.Query().Get("error")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if errMsg != "" {
		fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>Authorization Failed</title>
<style>body{font-family:system-ui;max-width:480px;margin:60px auto;padding:20px;text-align:center}
.error{color:#dc2626;font-size:14px;margin-top:16px;padding:12px;background:#fef2f2;border-radius:8px}</style>
</head><body>
<h2>Authorization Failed</h2>
<div class="error">%s</div>
<p style="margin-top:24px;font-size:13px;color:#666">Close this tab and try again.</p>
</body></html>`, errMsg)
		return
	}

	if code == "" {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>No Code</title></head><body>
<h2>No authorization code received</h2>
<p>Close this tab and try again.</p>
</body></html>`)
		return
	}

	fmt.Fprintf(w, `<!DOCTYPE html>
<html><head><title>Authorization Successful</title>
<style>
body{font-family:system-ui;max-width:480px;margin:60px auto;padding:20px;text-align:center}
.code-box{margin:20px 0;padding:16px;background:#f0fdf4;border:2px solid #22c55e;border-radius:8px;word-break:break-all;font-family:monospace;font-size:13px;user-select:all;cursor:text}
.btn{display:inline-block;padding:10px 24px;background:#111;color:#fff;border:none;border-radius:6px;font-size:14px;cursor:pointer;margin-top:8px}
.btn:hover{background:#333}
.hint{font-size:13px;color:#666;margin-top:16px}
</style>
</head><body>
<h2>Authorization Successful</h2>
<p style="font-size:14px;color:#444">Copy this code and paste it in the AT Connections page:</p>
<div class="code-box" id="code">%s</div>
<button class="btn" onclick="navigator.clipboard.writeText(document.getElementById('code').textContent).then(()=>{this.textContent='Copied!'})">Copy Code</button>
<p class="hint">After copying, close this tab and paste the code in AT.</p>
</body></html>`, code)
}

// OAuthExchangeAPI exchanges a manually-pasted authorization code for tokens.
// POST /api/v1/oauth/exchange {provider, code, redirect_uri}
func (s *Server) OAuthExchangeAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Provider    string `json:"provider"`
		Code        string `json:"code"`
		RedirectURI string `json:"redirect_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}

	provider, ok := oauthProviders[req.Provider]
	if !ok {
		httpResponse(w, fmt.Sprintf("unknown provider %q", req.Provider), http.StatusBadRequest)
		return
	}
	if req.Code == "" {
		httpResponse(w, "code is required", http.StatusBadRequest)
		return
	}
	if req.RedirectURI == "" {
		httpResponse(w, "redirect_uri is required", http.StatusBadRequest)
		return
	}

	clientID, err := s.oauthGetVar(r, provider.ClientIDVar)
	if err != nil {
		httpResponse(w, fmt.Sprintf("variable %q not set", provider.ClientIDVar), http.StatusBadRequest)
		return
	}
	clientSecret, err := s.oauthGetVar(r, provider.ClientSecretVar)
	if err != nil {
		httpResponse(w, fmt.Sprintf("variable %q not set", provider.ClientSecretVar), http.StatusBadRequest)
		return
	}

	// Exchange code for tokens.
	resp, err := http.PostForm(provider.TokenURL, url.Values{
		"code":          {req.Code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {req.RedirectURI},
		"grant_type":    {"authorization_code"},
	})
	if err != nil {
		httpResponse(w, "token exchange failed: "+err.Error(), http.StatusInternalServerError)
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
		httpResponse(w, "invalid token response", http.StatusInternalServerError)
		return
	}
	if tokenResp.Error != "" {
		httpResponse(w, tokenResp.Error+": "+tokenResp.ErrorDesc, http.StatusBadRequest)
		return
	}
	if tokenResp.RefreshToken == "" {
		httpResponse(w, "no refresh token received — try revoking access at https://myaccount.google.com/permissions and re-authorizing", http.StatusBadRequest)
		return
	}

	// Save refresh token as a variable.
	userEmail := s.getUserEmail(r)
	_, err = s.variableStore.CreateVariable(r.Context(), service.Variable{
		Key:         provider.RefreshTokenVar,
		Value:       tokenResp.RefreshToken,
		Description: fmt.Sprintf("OAuth2 refresh token for %s (auto-saved)", req.Provider),
		Secret:      true,
		CreatedBy:   userEmail,
		UpdatedBy:   userEmail,
	})
	if err != nil {
		httpResponse(w, "failed to save refresh token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("oauth manual exchange: saved refresh token",
		"provider", req.Provider,
		"token_var", provider.RefreshTokenVar,
	)

	httpResponseJSON(w, map[string]string{
		"status":   "connected",
		"provider": req.Provider,
		"message":  fmt.Sprintf("%s connected successfully", req.Provider),
	}, http.StatusOK)
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

	// Save refresh token.
	userEmail := s.getUserEmail(r)

	if oauthUserID != "" && s.userPrefStore != nil {
		// Per-user tokens go to user_preferences (encrypted at rest).
		tokenJSON, _ := json.Marshal(tokenResp.RefreshToken)
		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
			UserID: oauthUserID,
			Key:    provider.RefreshTokenVar,
			Value:  json.RawMessage(tokenJSON),
			Secret: true,
		}); err != nil {
			slog.Error("failed to save refresh token to user preferences", "provider", providerName, "error", err)
			renderOAuthResult(w, false, "failed to save refresh token: "+err.Error())
			return
		}
	} else {
		// Global tokens (no user scope) still go to variables.
		varKey := provider.RefreshTokenVar
		if oauthUserID != "" {
			varKey = provider.RefreshTokenVar + "::" + oauthUserID
		}
		if err := s.oauthUpsertVar(r, varKey, tokenResp.RefreshToken, true, userEmail); err != nil {
			slog.Error("failed to save refresh token", "provider", providerName, "error", err)
			renderOAuthResult(w, false, "failed to save refresh token: "+err.Error())
			return
		}
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

// userScopedVarLookup returns a VarLookup that checks:
// 1. Per-user preferences (user_preferences table) — for per-user data like OAuth tokens
// 2. Per-user variables (key::userID in variables table) — legacy per-user scope
// 3. Global variables (variables table)
// If userID is empty, it checks only global variables.
func (s *Server) userScopedVarLookup(ctx context.Context, userID string) workflow.VarLookup {
	if s.variableStore == nil {
		return nil
	}
	return func(key string) (string, error) {
		if userID != "" {
			// 1. Check user_preferences first (for per-user tokens, etc.).
			if s.userPrefStore != nil {
				pref, err := s.userPrefStore.GetUserPreference(ctx, userID, key)
				if err == nil && pref != nil {
					// Unwrap JSON string value for backward compatibility.
					var strVal string
					if json.Unmarshal(pref.Value, &strVal) == nil {
						return strVal, nil
					}
					return string(pref.Value), nil
				}
			}

			// 2. Check per-user scoped variable (legacy: key::userID).
			scopedKey := key + "::" + userID
			v, err := s.variableStore.GetVariableByKey(ctx, scopedKey)
			if err == nil && v != nil {
				return v.Value, nil
			}
		}
		// 3. Fall back to global variable.
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

// ─── Connections API ───

// connectionVarInfo describes a required variable for a connection.
type connectionVarInfo struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Secret      bool   `json:"secret"`
	Set         bool   `json:"set"` // whether this variable already has a value
}

// connectionInfo describes an external service connection.
type connectionInfo struct {
	Provider      string              `json:"provider"`
	Name          string              `json:"name"`
	Description   string              `json:"description"`
	Connected     bool                `json:"connected"`
	Type          string              `json:"type"` // "oauth" or "token"
	SetupComplete bool                `json:"setup_complete"`
	RequiredVars  []connectionVarInfo `json:"required_variables,omitempty"`
	OAuthProvider string              `json:"oauth_provider,omitempty"`
}

// oauthProviderMeta holds display info for OAuth connection providers.
type oauthProviderMeta struct {
	Name        string
	Description string
}

var oauthProvidersMeta = map[string]oauthProviderMeta{
	"google": {
		Name:        "Google",
		Description: "Access Gmail and Google Calendar",
	},
	"youtube": {
		Name:        "YouTube",
		Description: "Upload and publish videos to YouTube",
	},
}

// OAuthConnectionsAPI returns the status of all known external service connections.
// GET /api/v1/oauth/connections
func (s *Server) OAuthConnectionsAPI(w http.ResponseWriter, r *http.Request) {
	connections := []connectionInfo{}

	// 1. Add OAuth-based connections from oauthProviders registry.
	for providerKey, cfg := range oauthProviders {
		meta, ok := oauthProvidersMeta[providerKey]
		if !ok {
			meta = oauthProviderMeta{Name: providerKey, Description: ""}
		}

		// Build required variables with status.
		clientIDSet := false
		clientSecretSet := false
		if s.variableStore != nil {
			clientIDSet = s.lookupVar(r.Context(), cfg.ClientIDVar, r) != ""
			clientSecretSet = s.lookupVar(r.Context(), cfg.ClientSecretVar, r) != ""
		}

		conn := connectionInfo{
			Provider:      providerKey,
			Name:          meta.Name,
			Description:   meta.Description,
			Type:          "oauth",
			OAuthProvider: providerKey,
			RequiredVars: []connectionVarInfo{
				{Key: cfg.ClientIDVar, Description: "OAuth2 Client ID (from Google Cloud Console)", Secret: false, Set: clientIDSet},
				{Key: cfg.ClientSecretVar, Description: "OAuth2 Client Secret", Secret: true, Set: clientSecretSet},
			},
		}

		conn.SetupComplete = clientIDSet && clientSecretSet

		// Check if refresh token exists (= connected).
		if s.variableStore != nil {
			refreshToken := s.lookupVar(r.Context(), cfg.RefreshTokenVar, r)
			conn.Connected = refreshToken != ""
		}

		connections = append(connections, conn)
	}

	// 2. Add token-based connections from installed skill templates that
	//    have required_variables but no oauth field.
	for _, tmpl := range s.skillTemplates {
		if tmpl.OAuth != "" {
			continue // already covered by OAuth connections
		}
		if len(tmpl.RequiredVariables) == 0 {
			continue
		}

		// Build required variables with status.
		var requiredVars []connectionVarInfo
		allSet := true
		for _, v := range tmpl.RequiredVariables {
			isSet := false
			if s.variableStore != nil {
				isSet = s.lookupVar(r.Context(), v.Key, r) != ""
			}
			if !isSet {
				allSet = false
			}
			requiredVars = append(requiredVars, connectionVarInfo{
				Key:         v.Key,
				Description: v.Description,
				Secret:      v.Secret,
				Set:         isSet,
			})
		}

		conn := connectionInfo{
			Provider:     tmpl.Slug,
			Name:         tmpl.Name,
			Description:  tmpl.Description,
			Type:         "token",
			RequiredVars: requiredVars,
		}

		conn.Connected = allSet
		conn.SetupComplete = allSet

		connections = append(connections, conn)
	}

	httpResponseJSON(w, connections, http.StatusOK)
}

// OAuthDisconnectAPI removes the refresh token for an OAuth provider.
// DELETE /api/v1/oauth/connections/{provider}
func (s *Server) OAuthDisconnectAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	provider := r.PathValue("provider")
	cfg, ok := oauthProviders[provider]
	if !ok {
		httpResponse(w, fmt.Sprintf("unknown OAuth provider: %s", provider), http.StatusBadRequest)
		return
	}

	// Find and delete the refresh token variable.
	result, err := s.variableStore.ListVariables(r.Context(), nil)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to list variables: %v", err), http.StatusInternalServerError)
		return
	}

	for _, v := range result.Data {
		if v.Key == cfg.RefreshTokenVar {
			if err := s.variableStore.DeleteVariable(r.Context(), v.ID); err != nil {
				httpResponse(w, fmt.Sprintf("failed to delete token: %v", err), http.StatusInternalServerError)
				return
			}
			httpResponseJSON(w, map[string]string{
				"status":   "disconnected",
				"provider": provider,
			}, http.StatusOK)
			return
		}
	}

	httpResponseJSON(w, map[string]string{
		"status":   "not_connected",
		"provider": provider,
	}, http.StatusOK)
}

// lookupVar is a helper to look up a variable value, returning "" if not found.
func (s *Server) lookupVar(ctx context.Context, key string, r *http.Request) string {
	if s.variableStore == nil {
		return ""
	}

	result, err := s.variableStore.ListVariables(ctx, nil)
	if err != nil {
		return ""
	}

	for _, v := range result.Data {
		if v.Key == key {
			return v.Value
		}
	}
	return ""
}
