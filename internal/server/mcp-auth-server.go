package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// AT as an MCP authorization server (MCP authorization spec 2025-06-18).
//
//	GET  /.well-known/oauth-protected-resource/gateway/v1/mcp/{name}[/mcp]
//	GET  /.well-known/oauth-authorization-server[/<base>]
//	POST /oauth/mcp/register   RFC 7591 dynamic client registration
//	GET  /oauth/mcp/authorize  → the SPA consent page (#/oauth/mcp/authorize)
//	POST /oauth/mcp/token      authorization_code (PKCE S256) and refresh_token
//	POST /oauth/mcp/revoke     RFC 7009
//
// The consent decision itself is POST /api/v1/mcp-auth/authorize, a normal
// cookie-authenticated, workspace-admitted API call made by the consent page.
// A gateway MCP server with config.oauth.enabled then accepts the issued
// access tokens; a request without credentials gets a 401 whose
// WWW-Authenticate names the protected-resource metadata.

const (
	mcpAuthPrefix         = "/oauth/mcp"
	mcpAuthAccessPrefix   = "atm_"
	mcpAuthRefreshPrefix  = "atr_"
	mcpAuthCodePrefix     = "atc_"
	mcpAuthClientIDPrefix = "atmc_"
	mcpAuthSecretPrefix   = "atms_"
	mcpAuthRequestLimit   = 32 << 10
)

func mcpAuthHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func mcpAuthSecret(prefix string) (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// mcpAuthIssuer is the authorization server's issuer: the deployment's
// external base URL. It doubles as the base of every endpoint.
func (s *Server) mcpAuthIssuer(r *http.Request) string {
	return s.publicBaseURL(r)
}

// mcpResourceURL is the canonical resource identifier of one gateway MCP
// server, as clients configure it.
func (s *Server) mcpResourceURL(r *http.Request, name string) string {
	return s.mcpAuthIssuer(r) + "/gateway/v1/mcp/" + url.PathEscape(name)
}

func (s *Server) mcpResourceMetadataURL(r *http.Request, name string) string {
	issuer := s.mcpAuthIssuer(r)
	u, err := url.Parse(issuer)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + strings.TrimSuffix(u.EscapedPath(), "/") + "/gateway/v1/mcp/" + url.PathEscape(name)
}

// mcpAuthStore returns the store when it implements the authorization server.
func (s *Server) mcpAuthStore() service.MCPAuthServerStorer {
	store, _ := s.store.(service.MCPAuthServerStorer)
	return store
}

// mcpAuthRoute looks up an OAuth-enabled gateway MCP server by name. Names are
// unique per workspace, so a name used in several workspaces is ambiguous for
// a client that has not authenticated yet and is refused.
func (s *Server) mcpAuthRoute(ctx context.Context, name string) (*service.MCPServer, error) {
	store := s.mcpAuthStore()
	if store == nil {
		return nil, errors.New("MCP OAuth is not supported by this store")
	}
	srv, err := store.GetOAuthMCPRoute(ctx, name)
	if err != nil || srv == nil || srv.Config.OAuth == nil || !srv.Config.OAuth.Enabled {
		return nil, err
	}
	return srv, nil
}

// ─── Discovery ───

func mcpAuthJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func mcpAuthError(w http.ResponseWriter, status int, code, description string) {
	mcpAuthJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// MCPAuthProtectedResourceAPI answers RFC 9728 protected resource metadata
// for one OAuth-enabled gateway MCP server.
func (s *Server) MCPAuthProtectedResourceAPI(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	srv, err := s.mcpAuthRoute(r.Context(), name)
	if err != nil || srv == nil {
		http.NotFound(w, r)
		return
	}
	mcpAuthJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.mcpResourceURL(r, srv.Name),
		"authorization_servers":    []string{s.mcpAuthIssuer(r)},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            srv.Name,
	})
}

// MCPAuthServerMetadataAPI answers RFC 8414 authorization server metadata.
func (s *Server) MCPAuthServerMetadataAPI(w http.ResponseWriter, r *http.Request) {
	issuer := s.mcpAuthIssuer(r)
	mcpAuthJSON(w, http.StatusOK, map[string]any{
		"issuer":                                issuer,
		"authorization_endpoint":                issuer + mcpAuthPrefix + "/authorize",
		"token_endpoint":                        issuer + mcpAuthPrefix + "/token",
		"registration_endpoint":                 issuer + mcpAuthPrefix + "/register",
		"revocation_endpoint":                   issuer + mcpAuthPrefix + "/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none", "client_secret_post", "client_secret_basic"},
		"scopes_supported":                      []string{"mcp"},
	})
}

// ─── Registration (RFC 7591) ───

// mcpAuthRegisterLimiter bounds unauthenticated registrations per client
// address; the store additionally bounds the total.
var mcpAuthRegisterLimiter = newWebhookRateLimiter(20)

func (s *Server) MCPAuthRegisterAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		mcpAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "authorization server unavailable")
		return
	}
	if !mcpAuthRegisterLimiter.Allow(s.clientIPs.LimitKey(r)) {
		mcpAuthError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many registrations")
		return
	}
	var req struct {
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, mcpAuthRequestLimit)).Decode(&req); err != nil {
		mcpAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid registration request")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		mcpAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "register between one and ten redirect URIs")
		return
	}
	for _, uri := range req.RedirectURIs {
		if err := service.ValidMCPAuthRedirectURI(uri); err != nil || len(uri) > 2048 {
			mcpAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URIs must be https or loopback")
			return
		}
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			mcpAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported grant type "+g)
			return
		}
	}
	method := req.TokenEndpointAuthMethod
	if method == "" {
		method = "none"
	}
	if method != "none" && method != "client_secret_post" && method != "client_secret_basic" {
		mcpAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported token_endpoint_auth_method")
		return
	}
	name := cleanMCPAuthClientName(req.ClientName)
	clientID, err := mcpAuthSecret(mcpAuthClientIDPrefix)
	if err != nil {
		mcpAuthError(w, http.StatusInternalServerError, "server_error", "registration failed")
		return
	}
	client := service.MCPAuthClient{ClientID: clientID, Name: name, RedirectURIs: req.RedirectURIs}
	secret := ""
	if method != "none" {
		if secret, err = mcpAuthSecret(mcpAuthSecretPrefix); err != nil {
			mcpAuthError(w, http.StatusInternalServerError, "server_error", "registration failed")
			return
		}
		client.SecretHash = mcpAuthHash(secret)
	}
	saved, err := store.RegisterDynamicMCPAuthClient(r.Context(), client)
	if err != nil {
		slog.Warn("MCP client registration failed", "error", err)
		mcpAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "registration unavailable")
		return
	}
	resp := map[string]any{
		"client_id":                  saved.ClientID,
		"client_id_issued_at":        time.Now().Unix(),
		"client_name":                saved.Name,
		"redirect_uris":              saved.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": method,
	}
	if secret != "" {
		resp["client_secret"] = secret
		resp["client_secret_expires_at"] = 0
	}
	mcpAuthJSON(w, http.StatusCreated, resp)
}

func cleanMCPAuthClientName(name string) string {
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }), " ")
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > 80 {
		name = string(r[:80])
	}
	if name == "" {
		name = "MCP client"
	}
	return name
}

// ─── Authorization request ───

// mcpAuthRequest is a validated authorization request.
type mcpAuthRequest struct {
	Client        *service.MCPAuthClient
	Server        *service.MCPServer
	RedirectURI   string
	State         string
	CodeChallenge string
	Resource      string
}

// mcpAuthorizeError is reported to the user (when the redirect URI cannot be
// trusted) or to the client through the redirect.
type mcpAuthorizeError struct {
	Code, Description string
	Redirect          bool
}

func (e *mcpAuthorizeError) Error() string { return e.Description }

// validateMCPAuthRequest checks an authorization request in the order RFC
// 6749 §4.1.2.1 requires: client and redirect URI first (errors there are
// never redirected), then everything else (redirected to the client).
func (s *Server) validateMCPAuthRequest(r *http.Request, q url.Values) (*mcpAuthRequest, error) {
	store := s.mcpAuthStore()
	if store == nil {
		return nil, &mcpAuthorizeError{Code: "temporarily_unavailable", Description: "authorization server unavailable"}
	}
	client, err := store.GetMCPAuthClient(r.Context(), q.Get("client_id"))
	if err != nil {
		return nil, &mcpAuthorizeError{Code: "server_error", Description: "authorization unavailable"}
	}
	if client == nil {
		return nil, &mcpAuthorizeError{Code: "invalid_client", Description: "unknown client"}
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(client.RedirectURIs) == 1 {
		redirect = client.RedirectURIs[0]
	}
	if !service.MCPAuthRedirectMatches(client.RedirectURIs, redirect) {
		return nil, &mcpAuthorizeError{Code: "invalid_request", Description: "the redirect URI is not registered for this client"}
	}
	req := &mcpAuthRequest{Client: client, RedirectURI: redirect, State: q.Get("state")}
	fail := func(code, desc string) (*mcpAuthRequest, error) {
		return req, &mcpAuthorizeError{Code: code, Description: desc, Redirect: true}
	}
	if q.Get("response_type") != "code" {
		return fail("unsupported_response_type", "only response_type=code is supported")
	}
	if q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) < 43 || len(q.Get("code_challenge")) > 128 {
		return fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
	}
	req.CodeChallenge = q.Get("code_challenge")
	resource := q.Get("resource")
	if resource == "" {
		return fail("invalid_target", "the resource parameter naming the MCP server is required")
	}
	name, ok := s.mcpResourceName(r, resource)
	if !ok {
		return fail("invalid_target", "the resource is not an MCP server of this installation")
	}
	srv, err := s.mcpAuthRoute(r.Context(), name)
	if err != nil {
		return fail("invalid_target", "this MCP server name is ambiguous")
	}
	if srv == nil {
		return fail("invalid_target", "this MCP server does not accept sign-in")
	}
	if client.Dynamic {
		if !srv.Config.OAuth.DynamicClients {
			return fail("unauthorized_client", "this MCP server does not accept dynamically registered clients")
		}
		if !service.MCPAuthRedirectAllowed(srv.Config.OAuth.RedirectPatterns, redirect) {
			return fail("unauthorized_client", "this MCP server does not allow this client's redirect URI")
		}
	} else if client.MCPServerID != srv.ID || client.WorkspaceID != srv.WorkspaceID {
		return fail("unauthorized_client", "this client is registered for another MCP server")
	}
	req.Server = srv
	req.Resource = s.mcpResourceURL(r, srv.Name)
	return req, nil
}

// mcpResourceName maps a resource URL (with or without the trailing /mcp a
// client may append) back to a gateway MCP server name.
func (s *Server) mcpResourceName(r *http.Request, resource string) (string, bool) {
	prefix := s.mcpAuthIssuer(r) + "/gateway/v1/mcp/"
	rest, ok := strings.CutPrefix(strings.TrimSuffix(strings.TrimSpace(resource), "/"), prefix)
	if !ok {
		return "", false
	}
	rest = strings.TrimSuffix(rest, "/mcp")
	name, err := url.PathUnescape(rest)
	if err != nil || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	return name, true
}

func mcpAuthRedirect(redirect string, params url.Values) string {
	u, err := url.Parse(redirect)
	if err != nil {
		return redirect
	}
	q := u.Query()
	for k, v := range params {
		q[k] = v
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// MCPAuthAuthorizeAPI receives the client's authorization request. It
// validates what can be validated without a session and hands the browser to
// the consent page, which signs the person in if needed.
func (s *Server) MCPAuthAuthorizeAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req, err := s.validateMCPAuthRequest(r, q)
	if err != nil {
		var ae *mcpAuthorizeError
		if errors.As(err, &ae) && ae.Redirect && req != nil {
			params := url.Values{"error": {ae.Code}, "error_description": {ae.Description}}
			if req.State != "" {
				params.Set("state", req.State)
			}
			params.Set("iss", s.mcpAuthIssuer(r))
			http.Redirect(w, r, mcpAuthRedirect(req.RedirectURI, params), http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Authorization request refused: %s\n", err.Error())
		return
	}
	// The consent page selects the server's workspace before asking; the ID
	// is nonsecret and the API re-checks membership.
	hash := url.Values{}
	for k, v := range q {
		hash[k] = v
	}
	hash.Set("at_workspace", req.Server.WorkspaceID)
	http.Redirect(w, r, s.mcpAuthIssuer(r)+"/#/oauth/mcp/authorize?"+hash.Encode(), http.StatusFound)
}

// ─── Consent (browser session) ───

type mcpAuthConsentRequest struct {
	Params  map[string]string `json:"params"`
	Approve bool              `json:"approve"`
}

// MCPAuthConsentInfoAPI handles GET /api/v1/mcp-auth/authorize: it describes
// a pending request to the consent page (client, server, workspace,
// redirect host) and whether the signed-in account may approve it.
func (s *Server) MCPAuthConsentInfoAPI(w http.ResponseWriter, r *http.Request) {
	req, err := s.validateMCPAuthRequest(r, r.URL.Query())
	if err != nil {
		httpResponseJSON(w, map[string]any{"valid": false, "message": err.Error()}, http.StatusOK)
		return
	}
	principal, _ := service.AccessPrincipalFromContext(r.Context())
	redirect, _ := url.Parse(req.RedirectURI)
	out := map[string]any{
		"valid":          true,
		"client_name":    req.Client.Name,
		"client_dynamic": req.Client.Dynamic,
		"server_name":    req.Server.Name,
		"server_id":      req.Server.ID,
		"description":    req.Server.Config.Description,
		"workspace_id":   req.Server.WorkspaceID,
		"redirect_host":  redirect.Host,
		"resource":       req.Resource,
	}
	if principal.WorkspaceID != req.Server.WorkspaceID {
		out["workspace_mismatch"] = true
	} else {
		out["allowed"] = principal.Allows("mcp.read", service.AccessResource{WorkspaceID: principal.WorkspaceID, ID: req.Server.ID})
	}
	httpResponseJSON(w, out, http.StatusOK)
}

// MCPAuthConsentAPI handles POST /api/v1/mcp-auth/authorize. It records the
// decision of the signed-in account and answers with the redirect the
// consent page navigates to.
func (s *Server) MCPAuthConsentAPI(w http.ResponseWriter, r *http.Request) {
	var body mcpAuthConsentRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, mcpAuthRequestLimit)).Decode(&body); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	q := url.Values{}
	for k, v := range body.Params {
		q.Set(k, v)
	}
	req, err := s.validateMCPAuthRequest(r, q)
	if err != nil {
		var ae *mcpAuthorizeError
		if errors.As(err, &ae) && ae.Redirect && req != nil {
			params := url.Values{"error": {ae.Code}, "error_description": {ae.Description}, "iss": {s.mcpAuthIssuer(r)}}
			if req.State != "" {
				params.Set("state", req.State)
			}
			httpResponseJSON(w, map[string]string{"redirect": mcpAuthRedirect(req.RedirectURI, params)}, http.StatusOK)
			return
		}
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	params := url.Values{"iss": {s.mcpAuthIssuer(r)}}
	if req.State != "" {
		params.Set("state", req.State)
	}
	// Denying records nothing, so any signed-in account may send the client
	// back, including one with no access to the server's workspace.
	if !body.Approve {
		params.Set("error", "access_denied")
		params.Set("error_description", "the user denied access")
		httpResponseJSON(w, map[string]string{"redirect": mcpAuthRedirect(req.RedirectURI, params)}, http.StatusOK)
		return
	}
	principal, _ := service.AccessPrincipalFromContext(r.Context())
	if principal.WorkspaceID != req.Server.WorkspaceID {
		httpResponse(w, "switch to the workspace that owns this MCP server, then approve again", http.StatusConflict)
		return
	}
	code, err := mcpAuthSecret(mcpAuthCodePrefix)
	if err != nil {
		httpResponse(w, "authorization failed", http.StatusInternalServerError)
		return
	}
	grant, err := s.mcpAuthStore().IssueMCPAuthCode(r.Context(), *req.Client, req.Server.ID, req.Resource, mcpAuthHash(code), service.MCPAuthCode{
		RedirectURI: req.RedirectURI, CodeChallenge: req.CodeChallenge, ExpiresAt: time.Now().Add(service.MCPAuthCodeLifetime),
	})
	if err != nil {
		if mcpAuthManagementError(w, err) {
			return
		}
		httpResponse(w, "authorization failed", http.StatusInternalServerError)
		return
	}
	slog.Info("MCP authorization granted", "workspace_id", grant.WorkspaceID, "user_id", grant.UserID, "mcp_server_id", grant.MCPServerID, "client_id", grant.ClientID, "dynamic", req.Client.Dynamic)
	params.Set("code", code)
	resp := map[string]any{"redirect": mcpAuthRedirect(req.RedirectURI, params)}
	if req.Server.Config.OAuth.ChainUpstreams {
		resp["pending_accounts"] = s.mcpAuthPendingUpstreams(r.Context(), req.Server)
	}
	httpResponseJSON(w, resp, http.StatusOK)
}

// mcpAuthPendingUpstreams lists the OAuth upstreams of the server's MCP sets
// that use the signed-in account and that it has not connected yet, so the
// consent page can chain those authorizations before returning to the client.
func (s *Server) mcpAuthPendingUpstreams(ctx context.Context, srv *service.MCPServer) []mcpOAuthAccountTarget {
	runCtx, err := s.bindRuntimePrincipal(ctx, "mcp-auth-consent")
	if err != nil {
		return nil
	}
	principal, _ := service.AccessPrincipalFromContext(ctx)
	resolver, ok := s.mcpSetStore.(service.WorkspaceCredentialStorer)
	if !ok {
		return nil
	}
	var out []mcpOAuthAccountTarget
	connections := map[string][]service.Connection{}
	for _, name := range srv.Servers {
		if service.CheckExecution(runCtx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: name}) != nil {
			continue
		}
		set, err := resolver.ResolveMCPSetForUse(runCtx, name)
		if err != nil || set == nil {
			continue
		}
		for i, upstream := range set.Config.MCPUpstreams {
			if upstream.Auth == nil {
				continue
			}
			one := []service.MCPUpstream{upstream}
			if service.NormalizeMCPUpstreamAuth(one) != nil || !slices.Contains(one[0].Auth.Accounts, service.MCPAccountUser) {
				continue
			}
			auth := one[0].Auth
			list, cached := connections[auth.Provider]
			if !cached {
				list = s.personalMCPConnections(ctx, auth.Provider, principal.UserID)
				connections[auth.Provider] = list
			}
			connected := false
			for _, c := range list {
				if service.SameMCPResource(c.Credentials.MCPOAuth.MCPURL, upstream.URL) && !c.Credentials.MCPOAuth.NeedsReauth {
					connected = true
					break
				}
			}
			if connected {
				continue
			}
			out = append(out, mcpOAuthAccountTarget{
				SetID: set.ID, SetName: set.Name, SetScope: service.ConnectionScope(set.OwnerUserID),
				UpstreamIndex: i, Provider: auth.Provider, Server: mcpServerOrigin(upstream.URL), Accounts: auth.Accounts,
			})
		}
	}
	return out
}

// ─── Token endpoint ───

// mcpAuthClientCredentials authenticates the client at the token and
// revocation endpoints: a public client presents only client_id, a
// confidential one its secret (post body or HTTP Basic).
func (s *Server) mcpAuthClientCredentials(r *http.Request) (*service.MCPAuthClient, string) {
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id == "" {
		return nil, "client authentication required"
	}
	client, err := s.mcpAuthStore().GetMCPAuthClient(r.Context(), id)
	if err != nil || client == nil {
		return nil, "unknown client"
	}
	if client.SecretHash != "" {
		if secret == "" || subtle.ConstantTimeCompare([]byte(mcpAuthHash(secret)), []byte(client.SecretHash)) != 1 {
			return nil, "client authentication failed"
		}
	}
	return client, ""
}

func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

func (s *Server) MCPAuthTokenAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		mcpAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "authorization server unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcpAuthRequestLimit)
	if err := r.ParseForm(); err != nil {
		mcpAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	client, msg := s.mcpAuthClientCredentials(r)
	if client == nil {
		mcpAuthError(w, http.StatusUnauthorized, "invalid_client", msg)
		return
	}
	access, err := mcpAuthSecret(mcpAuthAccessPrefix)
	if err != nil {
		mcpAuthError(w, http.StatusInternalServerError, "server_error", "token issuance failed")
		return
	}
	refresh, err := mcpAuthSecret(mcpAuthRefreshPrefix)
	if err != nil {
		mcpAuthError(w, http.StatusInternalServerError, "server_error", "token issuance failed")
		return
	}
	var grant *service.MCPAuthGrant
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		verifier := r.PostForm.Get("code_verifier")
		grant, err = store.ExchangeMCPAuthCode(r.Context(), mcpAuthHash(r.PostForm.Get("code")), client.ClientID, r.PostForm.Get("redirect_uri"),
			func(challenge string) bool { return verifyPKCE(verifier, challenge) }, mcpAuthHash(access), mcpAuthHash(refresh))
	case "refresh_token":
		grant, err = store.RefreshMCPAuthTokens(r.Context(), mcpAuthHash(r.PostForm.Get("refresh_token")), client.ClientID, mcpAuthHash(access), mcpAuthHash(refresh))
	default:
		mcpAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "use authorization_code or refresh_token")
		return
	}
	if errors.Is(err, service.ErrMCPAuthInvalidGrant) {
		mcpAuthError(w, http.StatusBadRequest, "invalid_grant", "the code or refresh token is invalid, expired or already used")
		return
	}
	if err != nil {
		slog.Error("MCP token issuance failed", "error", err)
		mcpAuthError(w, http.StatusInternalServerError, "server_error", "token issuance failed")
		return
	}
	// RFC 8707: a client naming a resource gets a token only for the one
	// resource the grant was issued for.
	if resource := r.PostForm.Get("resource"); resource != "" && strings.TrimSuffix(strings.TrimSuffix(resource, "/"), "/mcp") != grant.Resource {
		mcpAuthError(w, http.StatusBadRequest, "invalid_target", "the grant was issued for another resource")
		return
	}
	mcpAuthJSON(w, http.StatusOK, map[string]any{
		"access_token":  access,
		"token_type":    "Bearer",
		"expires_in":    int64(service.MCPAuthAccessLifetime / time.Second),
		"refresh_token": refresh,
		"scope":         "mcp",
	})
}

// MCPAuthRevokeAPI implements RFC 7009. It always answers 200 for an
// authenticated client, so token validity cannot be probed.
func (s *Server) MCPAuthRevokeAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		mcpAuthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "authorization server unavailable")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, mcpAuthRequestLimit)
	if err := r.ParseForm(); err != nil {
		mcpAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	client, msg := s.mcpAuthClientCredentials(r)
	if client == nil {
		mcpAuthError(w, http.StatusUnauthorized, "invalid_client", msg)
		return
	}
	if token := r.PostForm.Get("token"); token != "" {
		if err := store.RevokeMCPAuthToken(r.Context(), mcpAuthHash(token), client.ClientID); err != nil {
			slog.Error("MCP token revocation failed", "error", err)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// ─── Resource server side ───

// mcpAuthChallenge answers an unauthenticated request to an OAuth-enabled
// gateway MCP server so an MCP client starts the sign-in flow.
func (s *Server) mcpAuthChallenge(w http.ResponseWriter, r *http.Request, name, errCode, message string) {
	challenge := fmt.Sprintf(`Bearer resource_metadata="%s"`, s.mcpResourceMetadataURL(r, name))
	if errCode != "" {
		challenge += fmt.Sprintf(`, error="%s"`, errCode)
	}
	w.Header().Set("WWW-Authenticate", challenge)
	w.Header().Add("Access-Control-Expose-Headers", "WWW-Authenticate")
	httpResponse(w, message, http.StatusUnauthorized)
}

// bearerToken returns the presented credential, if any.
func bearerToken(r *http.Request) string {
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

// admitMCPAuthAccess binds a gateway MCP request presenting an AT-issued
// access token. The run executes as the grant's account in the grant's
// workspace, under that account's live access and the workspace policy;
// the server's Run as binding is not used.
func (s *Server) admitMCPAuthAccess(w http.ResponseWriter, r *http.Request, name, token string) (*service.MCPServer, bool) {
	store := s.mcpAuthStore()
	if store == nil {
		httpResponse(w, "MCP sign-in is not available", http.StatusServiceUnavailable)
		return nil, false
	}
	access, err := store.ResolveMCPAuthAccessToken(r.Context(), mcpAuthHash(token))
	if err != nil {
		slog.Error("MCP access token lookup failed", "error", err)
		httpResponse(w, "internal error during authentication", http.StatusInternalServerError)
		return nil, false
	}
	if access == nil {
		s.mcpAuthChallenge(w, r, name, "invalid_token", "the access token is invalid or expired")
		return nil, false
	}
	grant := access.Grant
	srv, err := s.mcpAuthRoute(r.Context(), name)
	if err != nil || srv == nil || srv.ID != grant.MCPServerID || srv.WorkspaceID != grant.WorkspaceID || s.mcpResourceURL(r, srv.Name) != grant.Resource {
		// A token is audience-bound: one server, under the URL it was
		// issued for. Disabling sign-in on the server also ends its tokens.
		s.mcpAuthChallenge(w, r, name, "invalid_token", "the access token was not issued for this MCP server")
		return nil, false
	}
	ctx, err := s.bindMCPAuthGrant(r.Context(), grant)
	if err != nil {
		slog.Warn("MCP access denied", "mcp_server_id", srv.ID, "user_id", grant.UserID, "error", err)
		httpResponse(w, "your account no longer has access to this MCP server", http.StatusForbidden)
		return nil, false
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "mcp_servers.use", ResourceID: srv.ID}); err != nil {
		httpResponse(w, "your account may not use this MCP server", http.StatusForbidden)
		return nil, false
	}
	resolver, ok := s.mcpServerStore.(service.WorkspaceCredentialStorer)
	if !ok {
		httpResponse(w, "MCP execution access denied", http.StatusForbidden)
		return nil, false
	}
	full, err := resolver.ResolveMCPServerForUse(ctx, srv.Name)
	if err != nil || full == nil || full.ID != srv.ID {
		httpResponse(w, "MCP execution access denied", http.StatusForbidden)
		return nil, false
	}
	*r = *r.WithContext(ctx)
	return full, true
}

// bindMCPAuthGrant binds an execution as the grant's account.
func (s *Server) bindMCPAuthGrant(ctx context.Context, grant service.MCPAuthGrant) (context.Context, error) {
	workspaces, ok := s.store.(service.WorkspaceStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	executions, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	live, _, err := workspaces.ResolveWorkspaceAccess(ctx, grant.WorkspaceID, grant.UserID, "")
	if err != nil {
		return nil, err
	}
	policy, err := executions.GetExecutionPolicy(ctx, grant.WorkspaceID)
	if err != nil || policy == nil {
		return nil, fmt.Errorf("load execution policy: %w", err)
	}
	ctx = service.WithAccessPrincipal(ctx, live)
	return s.BindRuntimeExecution(ctx, service.ExecutionProvenance{
		RunID: ulid.Make().String(), UserID: grant.UserID, WorkspaceID: grant.WorkspaceID, Source: "mcp-oauth",
		MembershipVersion: live.MembershipVersion, PolicyVersion: policy.Version, GrantID: grant.ID,
	}, s.revalidateRuntimeExecution)
}

// revalidateMCPAuthGrant is the revalidation step for an MCP OAuth run: the
// grant must still exist and the account must still be a live member.
func (s *Server) revalidateMCPAuthGrant(ctx context.Context, provenance service.ExecutionProvenance) (context.Context, error) {
	store := s.mcpAuthStore()
	workspaces, ok := s.store.(service.WorkspaceStorer)
	if store == nil || !ok {
		return ctx, service.ErrExecutionDenied
	}
	if !mcpAuthGrantLive(ctx, store, provenance.GrantID, provenance.UserID, provenance.WorkspaceID) {
		return ctx, service.ErrExecutionDenied
	}
	live, _, err := workspaces.ResolveWorkspaceAccess(ctx, provenance.WorkspaceID, provenance.UserID, "")
	if err != nil {
		return ctx, err
	}
	return service.WithAccessPrincipal(ctx, live), nil
}

// mcpAuthGrantLive caches grant liveness for a few seconds: revalidation runs
// on every tool action, and the grant only changes when it is revoked.
var mcpAuthGrantCache sync.Map

type mcpAuthGrantCacheEntry struct {
	live    bool
	expires time.Time
}

func mcpAuthGrantLive(ctx context.Context, store service.MCPAuthServerStorer, grantID, userID, workspaceID string) bool {
	if v, ok := mcpAuthGrantCache.Load(grantID); ok {
		if e := v.(mcpAuthGrantCacheEntry); time.Now().Before(e.expires) {
			return e.live
		}
	}
	live, err := store.MCPAuthGrantLive(ctx, grantID, userID, workspaceID)
	if err != nil {
		// A lookup failure denies; it is not cached, so the next action retries.
		return false
	}
	mcpAuthGrantCache.Store(grantID, mcpAuthGrantCacheEntry{live: live, expires: time.Now().Add(5 * time.Second)})
	return live
}

func forgetMCPAuthGrant(id string) { mcpAuthGrantCache.Delete(id) }

// gatewayMCPUnauthorized answers a gateway MCP request that carries no usable
// credential. For an OAuth-enabled server it adds the challenge that starts an
// MCP client's sign-in; any other server keeps the plain 401.
func (s *Server) gatewayMCPUnauthorized(w http.ResponseWriter, r *http.Request, name string, presented bool, message string) {
	if srv, err := s.mcpAuthRoute(r.Context(), name); err == nil && srv != nil {
		code := ""
		if presented {
			code = "invalid_token"
		}
		s.mcpAuthChallenge(w, r, name, code, message)
		return
	}
	httpResponse(w, message, http.StatusUnauthorized)
}

// registerMCPAuthServerRoutes wires the authorization server. Every route is
// gated by the mcp_servers feature, and none reads browser cookies.
func (s *Server) registerMCPAuthServerRoutes(mux interface {
	HandleWithMethod(method, path string, handler http.HandlerFunc, middlewares ...func(next http.Handler) http.Handler)
}, base string) {
	gate := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			enabled, err := s.isFeatureEnabled(r.Context(), service.FeatureMCPServers)
			if err == nil && !enabled {
				http.NotFound(w, r)
				return
			}
			r.Header.Del("Cookie")
			h(w, r)
		}
	}
	for _, resource := range []string{"/gateway/v1/mcp/{name}", "/gateway/v1/mcp/{name}/mcp"} {
		mux.HandleWithMethod(http.MethodGet, "/.well-known/oauth-protected-resource"+base+resource, gate(s.MCPAuthProtectedResourceAPI))
	}
	mux.HandleWithMethod(http.MethodGet, "/.well-known/oauth-authorization-server"+base, gate(s.MCPAuthServerMetadataAPI))
	if base != "" {
		// Some clients look up the issuer's metadata at the bare root.
		mux.HandleWithMethod(http.MethodGet, "/.well-known/oauth-authorization-server", gate(s.MCPAuthServerMetadataAPI))
	}
	mux.HandleWithMethod(http.MethodPost, base+mcpAuthPrefix+"/register", gate(s.MCPAuthRegisterAPI))
	mux.HandleWithMethod(http.MethodGet, base+mcpAuthPrefix+"/authorize", gate(s.MCPAuthAuthorizeAPI))
	mux.HandleWithMethod(http.MethodPost, base+mcpAuthPrefix+"/token", gate(s.MCPAuthTokenAPI))
	mux.HandleWithMethod(http.MethodPost, base+mcpAuthPrefix+"/revoke", gate(s.MCPAuthRevokeAPI))
}

// ─── Management API (browser session) ───

// ListMCPAuthGrantsAPI handles GET /api/v1/mcp-auth/grants: the signed-in
// account's own MCP sign-ins in the selected workspace.
func (s *Server) ListMCPAuthGrantsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	grants, err := store.ListMCPAuthGrants(r.Context())
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, "failed to list MCP sign-ins", http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, grants, http.StatusOK)
}

// RevokeMCPAuthGrantAPI handles DELETE /api/v1/mcp-auth/grants/{id}.
func (s *Server) RevokeMCPAuthGrantAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := store.RevokeMCPAuthGrant(r.Context(), id); err != nil {
		if mcpAuthManagementError(w, err) {
			return
		}
		httpResponse(w, "failed to revoke MCP sign-in", http.StatusInternalServerError)
		return
	}
	forgetMCPAuthGrant(id)
	httpResponseJSON(w, map[string]string{"status": "revoked"}, http.StatusOK)
}

// ListMCPAuthClientsAPI handles GET /api/v1/mcp/servers/{id}/oauth-clients.
func (s *Server) ListMCPAuthClientsAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	clients, err := store.ListMCPAuthClients(r.Context(), r.PathValue("id"))
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, "failed to list OAuth clients", http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, clients, http.StatusOK)
}

// CreateMCPAuthClientAPI handles POST /api/v1/mcp/servers/{id}/oauth-clients.
// A confidential client's secret is returned once, in this response.
func (s *Server) CreateMCPAuthClientAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Name         string   `json:"name"`
		RedirectURIs []string `json:"redirect_uris"`
		Confidential bool     `json:"confidential"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, mcpAuthRequestLimit)).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	var uris []string
	for _, uri := range req.RedirectURIs {
		uri = strings.TrimSpace(uri)
		if uri == "" || slices.Contains(uris, uri) {
			continue
		}
		if err := service.ValidMCPAuthRedirectURI(uri); err != nil {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		uris = append(uris, uri)
	}
	if len(uris) == 0 || len(uris) > 10 {
		httpResponse(w, "add between one and ten redirect URIs", http.StatusBadRequest)
		return
	}
	clientID, err := mcpAuthSecret(mcpAuthClientIDPrefix)
	if err != nil {
		httpResponse(w, "failed to create client", http.StatusInternalServerError)
		return
	}
	client := service.MCPAuthClient{ClientID: clientID, MCPServerID: r.PathValue("id"), Name: cleanMCPAuthClientName(req.Name), RedirectURIs: uris, CreatedBy: s.getUserEmail(r)}
	secret := ""
	if req.Confidential {
		if secret, err = mcpAuthSecret(mcpAuthSecretPrefix); err != nil {
			httpResponse(w, "failed to create client", http.StatusInternalServerError)
			return
		}
		client.SecretHash = mcpAuthHash(secret)
	}
	saved, err := store.CreateMCPAuthClient(r.Context(), client)
	if err != nil {
		if mcpAuthManagementError(w, err) {
			return
		}
		httpResponse(w, "failed to create client", http.StatusInternalServerError)
		return
	}
	saved.Secret = secret
	httpResponseJSON(w, saved, http.StatusCreated)
}

// DeleteMCPAuthClientAPI handles DELETE /api/v1/mcp/servers/{id}/oauth-clients/{client}.
func (s *Server) DeleteMCPAuthClientAPI(w http.ResponseWriter, r *http.Request) {
	store := s.mcpAuthStore()
	if store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	if err := store.DeleteMCPAuthClient(r.Context(), r.PathValue("client")); err != nil {
		if mcpAuthManagementError(w, err) {
			return
		}
		httpResponse(w, "failed to delete client", http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, map[string]string{"status": "deleted"}, http.StatusOK)
}

func mcpAuthManagementError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, service.ErrAccessResourceNotFound) {
		httpResponse(w, "not found", http.StatusNotFound)
		return true
	}
	return workspaceBusinessError(w, err)
}
