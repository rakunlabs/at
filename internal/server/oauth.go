package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ─── Connector-driven OAuth2 ───
//
// OAuth providers are no longer a compiled-in map. The set of connectable
// services lives in the data-driven Connector registry (see
// connectors-registry.go + the connectors table). Any connector with
// AuthKind == "oauth2" participates in the flows below; new providers are added
// by shipping a connectors/*.json definition or creating one through the UI —
// no code change required.

// connectorVarKey returns the variable/credential key for a given suffix on a
// connector, preferring an explicit connector field whose key ends with that
// suffix and falling back to "<slug><suffix>". This keeps backward
// compatibility with the legacy "<provider>_client_id" variable convention.
func connectorVarKey(c *service.Connector, suffix string) string {
	for _, f := range c.Fields {
		if strings.HasSuffix(f.Key, suffix) {
			return f.Key
		}
	}
	return c.Slug + suffix
}

// connectorScopes joins the connector's OAuth scopes into a space-delimited
// string suitable for the authorize URL.
func connectorScopes(c *service.Connector) string {
	if c.OAuth == nil {
		return ""
	}
	return strings.Join(c.OAuth.Scopes, " ")
}

// isOAuth2Connector reports whether a connector can drive the OAuth2 flow.
func isOAuth2Connector(c *service.Connector) bool {
	return c != nil && c.AuthKind == service.ConnectorAuthOAuth2 && c.OAuth != nil && c.OAuth.AuthURL != "" && c.OAuth.TokenURL != ""
}

// ─── PKCE verifier cache ───

type pkceEntry struct {
	verifier string
	expires  time.Time
}

func (s *Server) pkcePut(key, verifier string) {
	s.oauthPKCE.Store(key, pkceEntry{verifier: verifier, expires: time.Now().Add(10 * time.Minute)})
}

// pkceTake returns and removes the verifier for a key (single use). Empty if
// missing or expired.
func (s *Server) pkceTake(key string) string {
	v, ok := s.oauthPKCE.LoadAndDelete(key)
	if !ok {
		return ""
	}
	e, ok := v.(pkceEntry)
	if !ok || time.Now().After(e.expires) {
		return ""
	}
	return e.verifier
}

func manualPKCEKey(provider, connectionID string) string {
	return "manual:" + provider + ":" + connectionID
}

// OAuthStartAPI returns the OAuth2 authorization URL for a connector.
// GET /api/v1/oauth/start?provider=google&scopes=gmail.readonly,calendar&user_id=discord::12345
// GET /api/v1/oauth/start?provider=youtube&connection_id=conn_01HV
//
// State encoding:
//   - "provider"                        — global refresh token (legacy)
//   - "provider::user_id"               — per-chat-user refresh token (legacy)
//   - "provider::conn::<connection_id>" — write to a named connection row
//
// When connection_id is provided, the connection's own client_id/client_secret
// are used (and the resulting token is stored on the connection row). Otherwise
// the legacy flow reads the client_id from the global variables table.
func (s *Server) OAuthStartAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	providerName := r.URL.Query().Get("provider")
	connector, err := s.resolveConnector(r.Context(), providerName)
	if err != nil {
		httpResponse(w, "failed to resolve connector: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !isOAuth2Connector(connector) {
		httpResponse(w, fmt.Sprintf("unknown oauth provider %q", providerName), http.StatusBadRequest)
		return
	}

	connectionID := r.URL.Query().Get("connection_id")
	clientID, err := s.resolveOAuthClientID(r.Context(), connector, connectionID)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Build callback URL from the incoming request.
	callbackURL := s.oauthCallbackURL(r)

	scopes := connectorScopes(connector)
	if scopeParam := r.URL.Query().Get("scopes"); scopeParam != "" {
		scopes = strings.ReplaceAll(scopeParam, ",", " ")
	}

	// Encode provider + optional scope (user_id OR connection_id) in state.
	state := buildOAuthState(providerName, r.URL.Query().Get("user_id"), connectionID)

	authURL, err := s.buildAuthorizeURL(connector, clientID, callbackURL, scopes, state, state)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}

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
	connector, err := s.resolveConnector(r.Context(), providerName)
	if err != nil {
		httpResponse(w, "failed to resolve connector: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !isOAuth2Connector(connector) {
		httpResponse(w, fmt.Sprintf("unknown oauth provider %q", providerName), http.StatusBadRequest)
		return
	}

	connectionID := r.URL.Query().Get("connection_id")
	clientID, err := s.resolveOAuthClientID(r.Context(), connector, connectionID)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	scopes := connectorScopes(connector)

	// Use AT's own code-display page as the redirect URI.
	redirectURI := s.oauthBaseURL(r) + "/api/v1/oauth/code-display"

	state := buildOAuthState(providerName, "", connectionID)

	// The manual flow has no state on the exchange call, so key the PKCE
	// verifier by provider+connection instead.
	authURL, err := s.buildAuthorizeURL(connector, clientID, redirectURI, scopes, state, manualPKCEKey(providerName, connectionID))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{
		"url":           authURL,
		"redirect_uri":  redirectURI,
		"provider":      providerName,
		"connection_id": connectionID,
	}, http.StatusOK)
}

// buildAuthorizeURL assembles the provider authorize URL from a connector's
// OAuth config. When the connector uses PKCE, a verifier is generated and
// cached under pkceKey for retrieval during the token exchange.
func (s *Server) buildAuthorizeURL(c *service.Connector, clientID, redirectURI, scopes, state, pkceKey string) (string, error) {
	params := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"response_type": {"code"},
		"scope":         {scopes},
		"state":         {state},
	}
	if c.OAuth.AccessType != "" {
		params.Set("access_type", c.OAuth.AccessType)
	}
	if c.OAuth.Prompt != "" {
		params.Set("prompt", c.OAuth.Prompt)
	}
	for k, v := range c.OAuth.ExtraAuthParams {
		params.Set(k, v)
	}
	if c.OAuth.UsePKCE {
		pkce, err := antropic.GeneratePKCE()
		if err != nil {
			return "", fmt.Errorf("generate PKCE challenge: %w", err)
		}
		s.pkcePut(pkceKey, pkce.Verifier)
		params.Set("code_challenge", pkce.Challenge)
		params.Set("code_challenge_method", "S256")
	}
	return c.OAuth.AuthURL + "?" + params.Encode(), nil
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

// oauthTokenResult is the normalized output of a token exchange.
type oauthTokenResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

// exchangeOAuthCode swaps an authorization code for tokens against a
// connector's token endpoint. It sends a form-encoded body with an
// Accept: application/json header (so providers like GitHub that default to
// form-encoded responses return JSON), and includes code_verifier when PKCE
// is in use. client_secret is omitted when empty (PKCE public clients).
func exchangeOAuthCode(ctx context.Context, c *service.Connector, clientID, clientSecret, code, redirectURI, codeVerifier string) (*oauthTokenResult, error) {
	form := url.Values{
		"code":         {code},
		"client_id":    {clientID},
		"redirect_uri": {redirectURI},
		"grant_type":   {"authorization_code"},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	if codeVerifier != "" {
		form.Set("code_verifier", codeVerifier)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.OAuth.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
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
		return nil, fmt.Errorf("invalid token response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("%s: %s", tokenResp.Error, tokenResp.ErrorDesc)
	}

	return &oauthTokenResult{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresIn:    tokenResp.ExpiresIn,
	}, nil
}

// OAuthExchangeAPI exchanges a manually-pasted authorization code for tokens.
// POST /api/v1/oauth/exchange {provider, code, redirect_uri, connection_id?}
func (s *Server) OAuthExchangeAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req struct {
		Provider     string `json:"provider"`
		Code         string `json:"code"`
		RedirectURI  string `json:"redirect_uri"`
		ConnectionID string `json:"connection_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}

	connector, err := s.resolveConnector(r.Context(), req.Provider)
	if err != nil {
		httpResponse(w, "failed to resolve connector: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !isOAuth2Connector(connector) {
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

	clientID, err := s.resolveOAuthClientID(r.Context(), connector, req.ConnectionID)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	clientSecret, err := s.resolveOAuthClientSecret(r.Context(), connector, req.ConnectionID)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	verifier := ""
	if connector.OAuth.UsePKCE {
		verifier = s.pkceTake(manualPKCEKey(req.Provider, req.ConnectionID))
	}

	tok, err := exchangeOAuthCode(r.Context(), connector, clientID, clientSecret, req.Code, req.RedirectURI, verifier)
	if err != nil {
		httpResponse(w, "token exchange failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	if tok.RefreshToken == "" && tok.AccessToken == "" {
		httpResponse(w, "no token received — try revoking access and re-authorizing", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)

	if req.ConnectionID != "" {
		if err := s.saveTokensToConnection(r.Context(), req.ConnectionID, connector, tok.RefreshToken, tok.AccessToken, userEmail); err != nil {
			httpResponse(w, "failed to save token: "+err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("oauth manual exchange: saved token to connection",
			"provider", req.Provider, "connection_id", req.ConnectionID)
	} else if err := s.saveTokensToVariable(r.Context(), connector, tok, "", userEmail); err != nil {
		httpResponse(w, "failed to save token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]string{
		"status":        "connected",
		"provider":      req.Provider,
		"connection_id": req.ConnectionID,
		"message":       fmt.Sprintf("%s connected successfully", req.Provider),
	}, http.StatusOK)
}

// OAuthCallbackAPI handles the redirect from the OAuth2 provider.
func (s *Server) OAuthCallbackAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	state := r.URL.Query().Get("state")
	providerName, oauthUserID, connectionID := parseOAuthState(state)
	connector, err := s.resolveConnector(r.Context(), providerName)
	if err != nil {
		renderOAuthResult(w, false, "failed to resolve connector: "+err.Error())
		return
	}
	if !isOAuth2Connector(connector) {
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

	clientID, err := s.resolveOAuthClientID(r.Context(), connector, connectionID)
	if err != nil {
		renderOAuthResult(w, false, err.Error())
		return
	}
	clientSecret, err := s.resolveOAuthClientSecret(r.Context(), connector, connectionID)
	if err != nil {
		renderOAuthResult(w, false, err.Error())
		return
	}

	callbackURL := s.oauthCallbackURL(r)

	verifier := ""
	if connector.OAuth.UsePKCE {
		verifier = s.pkceTake(state)
	}

	tok, err := exchangeOAuthCode(r.Context(), connector, clientID, clientSecret, code, callbackURL, verifier)
	if err != nil {
		slog.Error("oauth token exchange failed", "provider", providerName, "error", err)
		renderOAuthResult(w, false, "token exchange failed: "+err.Error())
		return
	}
	if tok.RefreshToken == "" && tok.AccessToken == "" {
		renderOAuthResult(w, false, "no token received — try revoking access and re-authorizing")
		return
	}

	userEmail := s.getUserEmail(r)

	switch {
	case connectionID != "":
		if err := s.saveTokensToConnection(r.Context(), connectionID, connector, tok.RefreshToken, tok.AccessToken, userEmail); err != nil {
			slog.Error("failed to save token to connection", "provider", providerName, "connection_id", connectionID, "error", err)
			renderOAuthResult(w, false, "failed to save token: "+err.Error())
			return
		}
	case oauthUserID != "" && s.userPrefStore != nil:
		// Per-user tokens go to user_preferences (encrypted at rest).
		token := tok.RefreshToken
		if token == "" {
			token = tok.AccessToken
		}
		tokenJSON, _ := json.Marshal(token)
		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
			UserID: oauthUserID,
			Key:    connector.Slug + "_refresh_token",
			Value:  json.RawMessage(tokenJSON),
			Secret: true,
		}); err != nil {
			slog.Error("failed to save token to user preferences", "provider", providerName, "error", err)
			renderOAuthResult(w, false, "failed to save token: "+err.Error())
			return
		}
	default:
		if err := s.saveTokensToVariable(r.Context(), connector, tok, oauthUserID, userEmail); err != nil {
			slog.Error("failed to save token", "provider", providerName, "error", err)
			renderOAuthResult(w, false, "failed to save token: "+err.Error())
			return
		}
	}

	logFields := []any{"provider", providerName, "user", userEmail}
	if oauthUserID != "" {
		logFields = append(logFields, "oauth_user_id", oauthUserID)
	}
	if connectionID != "" {
		logFields = append(logFields, "connection_id", connectionID)
	}
	slog.Info("oauth token saved", logFields...)
	renderOAuthResult(w, true, "")
}

// ─── Helpers ───

// oauthBaseURL returns the external base URL (scheme://host + base path) for
// building OAuth redirect URIs from the incoming request.
func (s *Server) oauthBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host + strings.TrimSuffix(s.config.BasePath, "/")
}

func (s *Server) oauthCallbackURL(r *http.Request) string {
	return s.oauthBaseURL(r) + "/api/v1/oauth/callback"
}

// saveTokensToVariable persists tokens from a non-connection flow into the
// global (or per-user) variables table. Refresh token is preferred; when a
// provider returns only an access token (e.g. GitHub OAuth Apps), the access
// token is stored under "<slug>_access_token" instead.
func (s *Server) saveTokensToVariable(ctx context.Context, c *service.Connector, tok *oauthTokenResult, userID, userEmail string) error {
	varKey := c.Slug + "_refresh_token"
	value := tok.RefreshToken
	if value == "" {
		varKey = c.Slug + "_access_token"
		value = tok.AccessToken
	}
	if userID != "" {
		varKey += "::" + userID
	}
	return s.oauthUpsertVar(ctx, varKey, value, true, userEmail)
}

func (s *Server) oauthUpsertVar(ctx context.Context, key, value string, secret bool, userEmail string) error {
	existing, _ := s.variableStore.GetVariableByKey(ctx, key)
	if existing != nil {
		existing.Value = value
		existing.UpdatedBy = userEmail
		_, err := s.variableStore.UpdateVariable(ctx, existing.ID, *existing)
		return err
	}

	_, err := s.variableStore.CreateVariable(ctx, service.Variable{
		Key:         key,
		Value:       value,
		Description: "Managed by OAuth flow",
		Secret:      secret,
		CreatedBy:   userEmail,
		UpdatedBy:   userEmail,
	})
	return err
}

// parseOAuthState splits a state string into its components.
// Supported formats:
//   - "provider"
//   - "provider::user_id"               — legacy per-chat-user scope
//   - "provider::conn::<connection_id>" — named-connection scope
func parseOAuthState(state string) (provider, userID, connectionID string) {
	idx := strings.Index(state, "::")
	if idx == -1 {
		return state, "", ""
	}
	provider = state[:idx]
	rest := state[idx+2:]
	if strings.HasPrefix(rest, "conn::") {
		return provider, "", strings.TrimPrefix(rest, "conn::")
	}
	return provider, rest, ""
}

// buildOAuthState encodes provider, optional user_id, and optional connection_id
// into the state parameter. connection_id takes precedence if both are set.
func buildOAuthState(provider, userID, connectionID string) string {
	if connectionID != "" {
		return provider + "::conn::" + connectionID
	}
	if userID != "" {
		return provider + "::" + userID
	}
	return provider
}

// resolveOAuthClientID returns the client_id to use for a connector, preferring
// the connection row's credentials when connectionID is set.
func (s *Server) resolveOAuthClientID(ctx context.Context, c *service.Connector, connectionID string) (string, error) {
	if connectionID != "" {
		conn, err := s.loadConnectionForOAuth(ctx, connectionID)
		if err != nil {
			return "", err
		}
		if conn.Credentials.ClientID == "" {
			return "", fmt.Errorf("connection %q has no client_id set — update the connection first", connectionID)
		}
		return conn.Credentials.ClientID, nil
	}
	key := connectorVarKey(c, "_client_id")
	v, err := s.variableStore.GetVariableByKey(ctx, key)
	if err != nil {
		return "", fmt.Errorf("load variable %q: %w", key, err)
	}
	if v == nil {
		return "", fmt.Errorf("variable %q not set — create it first or bind a connection", key)
	}
	return v.Value, nil
}

// resolveOAuthClientSecret returns the client_secret for a connector. PKCE
// connectors may legitimately have no secret, in which case "" is returned
// without error.
func (s *Server) resolveOAuthClientSecret(ctx context.Context, c *service.Connector, connectionID string) (string, error) {
	pkce := c.OAuth != nil && c.OAuth.UsePKCE
	if connectionID != "" {
		conn, err := s.loadConnectionForOAuth(ctx, connectionID)
		if err != nil {
			return "", err
		}
		if conn.Credentials.ClientSecret == "" {
			if pkce {
				return "", nil
			}
			return "", fmt.Errorf("connection %q has no client_secret set", connectionID)
		}
		return conn.Credentials.ClientSecret, nil
	}
	key := connectorVarKey(c, "_client_secret")
	v, err := s.variableStore.GetVariableByKey(ctx, key)
	if err != nil {
		return "", fmt.Errorf("load variable %q: %w", key, err)
	}
	if v == nil {
		if pkce {
			return "", nil
		}
		return "", fmt.Errorf("variable %q not set", key)
	}
	return v.Value, nil
}

// loadConnectionForOAuth fetches a connection row, returning a friendly error
// when the store is unconfigured or the row is missing.
func (s *Server) loadConnectionForOAuth(ctx context.Context, connectionID string) (*service.Connection, error) {
	if s.connectionStore == nil {
		return nil, fmt.Errorf("connection store not configured")
	}
	conn, err := s.connectionStore.GetConnection(ctx, connectionID)
	if err != nil {
		return nil, fmt.Errorf("load connection %q: %w", connectionID, err)
	}
	if conn == nil {
		return nil, fmt.Errorf("connection %q not found", connectionID)
	}
	return conn, nil
}

// saveTokensToConnection writes tokens into the connection row, optionally
// fetching the account label from the connector's userinfo endpoint. A refresh
// token is stored on the dedicated field; when a provider returns only an
// access token, it is stored in Extra under "<slug>_access_token".
func (s *Server) saveTokensToConnection(ctx context.Context, connectionID string, c *service.Connector, refreshToken, accessToken, userEmail string) error {
	conn, err := s.loadConnectionForOAuth(ctx, connectionID)
	if err != nil {
		return err
	}
	if refreshToken != "" {
		conn.Credentials.RefreshToken = refreshToken
	} else if accessToken != "" {
		if conn.Credentials.Extra == nil {
			conn.Credentials.Extra = map[string]string{}
		}
		conn.Credentials.Extra[c.Slug+"_access_token"] = accessToken
	}
	if conn.AccountLabel == "" && accessToken != "" {
		if label := fetchConnectorAccountLabel(ctx, c, accessToken); label != "" {
			conn.AccountLabel = label
		}
	}
	conn.UpdatedBy = userEmail
	if _, err := s.connectionStore.UpdateConnection(ctx, conn.ID, *conn); err != nil {
		return fmt.Errorf("update connection %q: %w", connectionID, err)
	}
	return nil
}

// fetchConnectorAccountLabel calls the connector's userinfo endpoint and
// extracts a human-readable label using the connector's AccountLabelPath
// (a dot-path supporting array indices, e.g. "items.0.snippet.title").
// Best-effort: any error yields "".
func fetchConnectorAccountLabel(ctx context.Context, c *service.Connector, accessToken string) string {
	if c.OAuth == nil || c.OAuth.UserinfoURL == "" || c.OAuth.AccountLabelPath == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.OAuth.UserinfoURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return ""
	}
	var data any
	if err := json.Unmarshal(body, &data); err != nil {
		return ""
	}
	return jsonDotPath(data, c.OAuth.AccountLabelPath)
}

// jsonDotPath walks a decoded JSON value along a dot-delimited path, where
// numeric segments index into arrays. Returns the string at the leaf, or "".
func jsonDotPath(v any, path string) string {
	cur := v
	for _, p := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			cur = node[p]
		case []any:
			idx, err := strconv.Atoi(p)
			if err != nil || idx < 0 || idx >= len(node) {
				return ""
			}
			cur = node[idx]
		default:
			return ""
		}
		if cur == nil {
			return ""
		}
	}
	if str, ok := cur.(string); ok {
		return str
	}
	return ""
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

	connector, err := s.resolveConnector(ctx, provider)
	if err != nil || !isOAuth2Connector(connector) {
		return ""
	}
	if s.variableStore == nil {
		return ""
	}
	// Verify client_id is set for this provider.
	v, err := s.variableStore.GetVariableByKey(ctx, connectorVarKey(connector, "_client_id"))
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

// ─── Legacy flat connections view ───

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

// OAuthConnectionsAPI returns the status of all known external service connections.
// GET /api/v1/oauth/connections
//
// This is the legacy flat (variable-backed) view kept for the settings import
// flow. The OAuth providers now come from the connector registry instead of a
// hardcoded map.
func (s *Server) OAuthConnectionsAPI(w http.ResponseWriter, r *http.Request) {
	connections := []connectionInfo{}

	connectors, err := s.listConnectors(r.Context())
	if err != nil {
		httpResponse(w, "failed to list connectors: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 1. OAuth-based connectors from the registry.
	for i := range connectors {
		c := &connectors[i]
		if !isOAuth2Connector(c) {
			continue
		}

		clientIDVar := connectorVarKey(c, "_client_id")
		clientSecretVar := connectorVarKey(c, "_client_secret")

		clientIDSet := false
		clientSecretSet := false
		if s.variableStore != nil {
			clientIDSet = s.lookupVar(r.Context(), clientIDVar, r) != ""
			clientSecretSet = s.lookupVar(r.Context(), clientSecretVar, r) != ""
		}

		conn := connectionInfo{
			Provider:      c.Slug,
			Name:          c.Name,
			Description:   c.Description,
			Type:          "oauth",
			OAuthProvider: c.Slug,
			RequiredVars: []connectionVarInfo{
				{Key: clientIDVar, Description: "OAuth2 Client ID", Secret: false, Set: clientIDSet},
				{Key: clientSecretVar, Description: "OAuth2 Client Secret", Secret: true, Set: clientSecretSet},
			},
		}
		conn.SetupComplete = clientIDSet && clientSecretSet

		if s.variableStore != nil {
			refreshToken := s.lookupVar(r.Context(), c.Slug+"_refresh_token", r)
			conn.Connected = refreshToken != ""
		}

		connections = append(connections, conn)
	}

	// 2. Token-based connections from installed skill templates that have
	//    required_variables but no oauth field.
	for _, tmpl := range s.skillTemplates {
		if tmpl.OAuth != "" {
			continue // already covered by OAuth connections
		}
		if len(tmpl.RequiredVariables) == 0 {
			continue
		}

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
	connector, err := s.resolveConnector(r.Context(), provider)
	if err != nil {
		httpResponse(w, "failed to resolve connector: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !isOAuth2Connector(connector) {
		httpResponse(w, fmt.Sprintf("unknown OAuth provider: %s", provider), http.StatusBadRequest)
		return
	}

	refreshTokenVar := connector.Slug + "_refresh_token"

	result, err := s.variableStore.ListVariables(r.Context(), nil)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to list variables: %v", err), http.StatusInternalServerError)
		return
	}

	for _, v := range result.Data {
		if v.Key == refreshTokenVar {
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
func (s *Server) lookupVar(ctx context.Context, key string, _ *http.Request) string {
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
