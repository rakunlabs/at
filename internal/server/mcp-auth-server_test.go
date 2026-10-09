package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/mcpauth"
)

// TestMCPAuthServerEndToEnd drives AT as an MCP authorization server the way
// an MCP client does: 401 challenge → protected resource metadata →
// authorization server metadata → dynamic registration → consent →
// code exchange with PKCE → tool call as the signed-in account → refresh
// rotation → revocation. AT's own MCP OAuth client performs discovery and
// registration, so the documents are checked against a real consumer.
func TestMCPAuthServerEndToEnd(t *testing.T) {
	f := newMachineFixture(t)
	srv, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "gitlab", Config: service.MCPServerConfig{
		EnabledBuiltinTools: []string{"task_list"},
		OAuth:               &service.MCPServerOAuth{Enabled: true, DynamicClients: true},
	}})
	if err != nil {
		t.Fatal(err)
	}

	mux := ada.New()
	gateway := mux.Group("/gateway")
	gateway.POST("/v1/mcp/{name}", f.s.GatewayMCPHandler)
	gateway.POST("/v1/mcp/{name}/mcp", f.s.GatewayMCPHandler)
	f.s.registerMCPAuthServerRoutes(mux, "")
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	f.s.config.ExternalURL = httpServer.URL
	resource := httpServer.URL + "/gateway/v1/mcp/gitlab"

	rpc := func(token, body string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, resource, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`

	// 1. Unauthenticated: a challenge naming the protected resource metadata.
	resp := rpc("", initialize)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/gateway/v1/mcp/gitlab") {
		t.Fatalf("challenge: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	// 2. Discovery and registration with AT's own MCP OAuth client.
	client := mcpauth.Client(true)
	meta, err := mcpauth.Discover(t.Context(), client, resource, "")
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if meta.Issuer != httpServer.URL || meta.Resource != resource || meta.RegistrationEndpoint == "" {
		t.Fatalf("metadata: %+v", meta)
	}
	redirect := "http://127.0.0.1:43110/callback"
	clientID, clientSecret, err := mcpauth.Register(t.Context(), client, meta, redirect, "Claude Code")
	if err != nil || clientID == "" || clientSecret != "" {
		t.Fatalf("register: %q %q %v", clientID, clientSecret, err)
	}

	// 3. The authorize endpoint hands a valid request to the consent page and
	// refuses a redirect URI the client did not register, without redirecting.
	pkce, _ := mcpauth.NewPKCE()
	authorizeURL, err := mcpauth.AuthorizeURL(meta, clientID, redirect, nil, "st4te", pkce.Challenge)
	if err != nil {
		t.Fatal(err)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if r, err := noRedirect.Get(authorizeURL); err != nil || r.StatusCode != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "#/oauth/mcp/authorize?") {
		t.Fatalf("authorize: %v %+v", err, r)
	}
	bad := strings.Replace(authorizeURL, url.QueryEscape(redirect), url.QueryEscape("https://evil.example/cb"), 1)
	if r, err := noRedirect.Get(bad); err != nil || r.StatusCode != http.StatusBadRequest {
		t.Fatalf("foreign redirect accepted: %v %+v", err, r)
	}

	// 4. Consent as the signed-in account (the browser session).
	params := map[string]string{}
	for k, v := range mustQuery(t, authorizeURL) {
		params[k] = v[0]
	}
	consent := func(approve bool) map[string]any {
		t.Helper()
		body, _ := json.Marshal(mcpAuthConsentRequest{Params: params, Approve: approve})
		w := httptest.NewRecorder()
		f.s.MCPAuthConsentAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp-auth/authorize", strings.NewReader(string(body))).WithContext(f.ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("consent: %d %s", w.Code, w.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}
	denied := mustQuery(t, consent(false)["redirect"].(string))
	if denied.Get("error") != "access_denied" || denied.Get("state") != "st4te" {
		t.Fatalf("deny: %v", denied)
	}
	approved := mustQuery(t, consent(true)["redirect"].(string))
	code := approved.Get("code")
	if code == "" || approved.Get("state") != "st4te" || approved.Get("iss") != httpServer.URL {
		t.Fatalf("approve: %v", approved)
	}

	// 5. A wrong verifier burns the code; a fresh one exchanges once.
	if _, err := mcpauth.Exchange(t.Context(), client, meta.TokenEndpoint, clientID, "", code, redirect, strings.Repeat("x", 43), resource); err == nil {
		t.Fatal("wrong PKCE verifier accepted")
	}
	if _, err := mcpauth.Exchange(t.Context(), client, meta.TokenEndpoint, clientID, "", code, redirect, pkce.Verifier, resource); err == nil {
		t.Fatal("code reused after a failed exchange")
	}
	code = mustQuery(t, consent(true)["redirect"].(string)).Get("code")
	tok, err := mcpauth.Exchange(t.Context(), client, meta.TokenEndpoint, clientID, "", code, redirect, pkce.Verifier, resource)
	if err != nil || !strings.HasPrefix(tok.AccessToken, mcpAuthAccessPrefix) || tok.RefreshToken == "" {
		t.Fatalf("exchange: %+v %v", tok, err)
	}
	if _, err := mcpauth.Exchange(t.Context(), client, meta.TokenEndpoint, clientID, "", code, redirect, pkce.Verifier, resource); err == nil {
		t.Fatal("code exchanged twice")
	}

	// 6. Calls run as the signed-in account; the server has no Run as binding.
	if resp := rpc(tok.AccessToken, initialize); resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize with token: %d", resp.StatusCode)
	}
	call := rpc(tok.AccessToken, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"task_list","arguments":{}}}`)
	var out struct {
		Error  any `json:"error"`
		Result any `json:"result"`
	}
	if err := json.NewDecoder(call.Body).Decode(&out); err != nil || call.StatusCode != http.StatusOK || out.Error != nil {
		t.Fatalf("tool call: %d %+v %v", call.StatusCode, out, err)
	}

	// The token is audience-bound to this server.
	other, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "other", Config: service.MCPServerConfig{OAuth: &service.MCPServerOAuth{Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, httpServer.URL+"/gateway/v1/mcp/"+other.Name, strings.NewReader(initialize))
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token accepted by another server: %v %+v", err, r)
	}

	// 7. Refresh rotates: the old refresh token stops working.
	refreshed, err := mcpauth.Refresh(t.Context(), client, meta.TokenEndpoint, clientID, "", tok.RefreshToken, resource)
	if err != nil || refreshed.AccessToken == tok.AccessToken {
		t.Fatalf("refresh: %+v %v", refreshed, err)
	}
	if _, err := mcpauth.Refresh(t.Context(), client, meta.TokenEndpoint, clientID, "", tok.RefreshToken, resource); err == nil {
		t.Fatal("rotated refresh token reused")
	}

	// 8. The account sees and revokes its sign-in; tokens stop working.
	w := httptest.NewRecorder()
	f.s.ListMCPAuthGrantsAPI(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp-auth/grants", nil).WithContext(f.ctx))
	var grants []service.MCPAuthGrant
	if err := json.Unmarshal(w.Body.Bytes(), &grants); err != nil || len(grants) != 1 || grants[0].ServerName != "gitlab" || grants[0].ClientName != "Claude Code" {
		t.Fatalf("grants: %s", w.Body.String())
	}
	revoke := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp-auth/grants/"+grants[0].ID, nil).WithContext(f.ctx)
	revoke.SetPathValue("id", grants[0].ID)
	w = httptest.NewRecorder()
	f.s.RevokeMCPAuthGrantAPI(w, revoke)
	if w.Code != http.StatusOK {
		t.Fatalf("revoke: %d %s", w.Code, w.Body.String())
	}
	if resp := rpc(refreshed.AccessToken, initialize); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token still works: %d", resp.StatusCode)
	}
	if _, err := mcpauth.Refresh(t.Context(), client, meta.TokenEndpoint, clientID, "", refreshed.RefreshToken, resource); err == nil {
		t.Fatal("refresh after revocation")
	}

	// 9. A token belongs to its account: the member's calls run as the
	// member, and disabling the member ends access although the token itself
	// has not expired.
	if err := f.store.CreateAuthSession(t.Context(), service.AuthSession{Hash: "member-login", UserID: f.member, Version: 0, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	memberPrincipal, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, f.member, "member-login")
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := service.WithAccessPrincipal(t.Context(), memberPrincipal)
	body, _ := json.Marshal(mcpAuthConsentRequest{Params: params, Approve: true})
	w = httptest.NewRecorder()
	f.s.MCPAuthConsentAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(memberCtx))
	var memberConsent map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &memberConsent)
	code = mustQuery(t, memberConsent["redirect"]).Get("code")
	tok, err = mcpauth.Exchange(t.Context(), client, meta.TokenEndpoint, clientID, "", code, redirect, pkce.Verifier, resource)
	if err != nil {
		t.Fatalf("member exchange: %v (%s)", err, w.Body.String())
	}
	if resp := rpc(tok.AccessToken, initialize); resp.StatusCode != http.StatusOK {
		t.Fatalf("member token: %d", resp.StatusCode)
	}
	access, err := f.store.ResolveMCPAuthAccessToken(t.Context(), mcpAuthHash(tok.AccessToken))
	if err != nil || access == nil || access.Grant.UserID != f.member {
		t.Fatalf("member token resolves to another account: %+v %v", access, err)
	}
	if _, err := f.store.InvalidateAuthUser(t.Context(), f.member, true); err != nil {
		t.Fatal(err)
	}
	if resp := rpc(tok.AccessToken, initialize); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("disabled account still admitted: %d", resp.StatusCode)
	}

	// 10. Dynamic clients can be turned off per server.
	srv.Config.OAuth.DynamicClients = false
	if _, err := f.store.UpdateMCPServer(f.ctx, srv.ID, *srv); err != nil {
		t.Fatal(err)
	}
	if r, err := noRedirect.Get(authorizeURL); err != nil || r.StatusCode != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "error=unauthorized_client") {
		t.Fatalf("dynamic client admitted after disabling: %v %+v", err, r)
	}
}

// TestMCPAuthServerPreregisteredClient covers a client created by an
// administrator for one server, with a secret.
func TestMCPAuthServerPreregisteredClient(t *testing.T) {
	f := newMachineFixture(t)
	srv, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "internal", Config: service.MCPServerConfig{
		EnabledBuiltinTools: []string{"task_list"},
		OAuth:               &service.MCPServerOAuth{Enabled: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	mux := ada.New()
	mux.Group("/gateway").POST("/v1/mcp/{name}", f.s.GatewayMCPHandler)
	f.s.registerMCPAuthServerRoutes(mux, "")
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	f.s.config.ExternalURL = httpServer.URL
	resource := httpServer.URL + "/gateway/v1/mcp/internal"

	create := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"CI","redirect_uris":["https://ci.example/cb"],"confidential":true}`)).WithContext(f.ctx)
	create.SetPathValue("id", srv.ID)
	w := httptest.NewRecorder()
	f.s.CreateMCPAuthClientAPI(w, create)
	var created service.MCPAuthClient
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil || w.Code != http.StatusCreated || created.Secret == "" || !created.Confidential {
		t.Fatalf("create client: %d %s", w.Code, w.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(f.ctx)
	list.SetPathValue("id", srv.ID)
	w = httptest.NewRecorder()
	f.s.ListMCPAuthClientsAPI(w, list)
	if strings.Contains(w.Body.String(), created.Secret) || !strings.Contains(w.Body.String(), created.ClientID) {
		t.Fatalf("list leaked secret or missed client: %s", w.Body.String())
	}

	verifier := strings.Repeat("v", 64)
	sum := sha256.Sum256([]byte(verifier))
	params := map[string]string{
		"response_type": "code", "client_id": created.ClientID, "redirect_uri": "https://ci.example/cb",
		"code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]), "code_challenge_method": "S256", "resource": resource,
	}
	body, _ := json.Marshal(mcpAuthConsentRequest{Params: params, Approve: true})
	w = httptest.NewRecorder()
	f.s.MCPAuthConsentAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(f.ctx))
	var out map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	code := mustQuery(t, out["redirect"]).Get("code")
	if code == "" {
		t.Fatalf("consent: %s", w.Body.String())
	}
	client := mcpauth.Client(true)
	if _, err := mcpauth.Exchange(t.Context(), client, httpServer.URL+"/oauth/mcp/token", created.ClientID, "wrong", code, "https://ci.example/cb", verifier, resource); err == nil {
		t.Fatal("wrong client secret accepted")
	}
	// Client authentication fails before the code is touched, so the real
	// client can still redeem it.
	tok, err := mcpauth.Exchange(t.Context(), client, httpServer.URL+"/oauth/mcp/token", created.ClientID, created.Secret, code, "https://ci.example/cb", verifier, resource)
	if err != nil || tok.AccessToken == "" {
		t.Fatalf("exchange with the client secret: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, resource, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	if r, err := http.DefaultClient.Do(req); err != nil || r.StatusCode != http.StatusOK {
		t.Fatalf("pre-registered client token rejected: %v %+v", err, r)
	}

	// The client is bound to its server.
	other, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "elsewhere", Config: service.MCPServerConfig{OAuth: &service.MCPServerOAuth{Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	params["resource"] = httpServer.URL + "/gateway/v1/mcp/" + other.Name
	body, _ = json.Marshal(mcpAuthConsentRequest{Params: params, Approve: true})
	w = httptest.NewRecorder()
	f.s.MCPAuthConsentAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(f.ctx))
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if mustQuery(t, out["redirect"]).Get("error") != "unauthorized_client" {
		t.Fatalf("client used for another server: %s", w.Body.String())
	}
}

func mustQuery(t *testing.T, raw string) url.Values {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query()
}

// TestMCPAuthServerChainsUpstreamAccounts checks that consent reports the
// OAuth upstreams the account still has to connect when chaining is on.
func TestMCPAuthServerChainsUpstreamAccounts(t *testing.T) {
	fake := newFakeMCPOAuth(t)
	service.SetTrustedLocalMCPLoader(func(context.Context) ([]string, error) { return []string{fake.mcpURL()}, nil })
	t.Cleanup(func() { service.SetTrustedLocalMCPLoader(nil) })
	f := newMachineFixture(t)
	f.s.connectionStore = f.store
	set, err := f.store.CreateMCPSet(f.ctx, service.MCPSet{Name: "gitlab-tools", Config: service.MCPServerConfig{
		MCPUpstreams: []service.MCPUpstream{{URL: fake.mcpURL(), Auth: &service.MCPUpstreamAuth{Type: "oauth2"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "gitlab", Servers: []string{set.Name}, Config: service.MCPServerConfig{
		OAuth: &service.MCPServerOAuth{Enabled: true, DynamicClients: true, ChainUpstreams: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	pending := f.s.mcpAuthPendingUpstreams(f.ctx, srv)
	if len(pending) != 1 || pending[0].SetID != set.ID || pending[0].UpstreamIndex != 0 {
		t.Fatalf("pending upstream accounts: %+v", pending)
	}
}
