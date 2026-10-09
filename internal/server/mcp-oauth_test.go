package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// fakeMCPOAuth is an MCP server and its authorization server. The MCP server
// accepts exactly one current access token; the authorization server issues
// tokens and rotates them on refresh.
type fakeMCPOAuth struct {
	t       *testing.T
	mcp, as *httptest.Server

	mu           sync.Mutex
	valid        string
	refresh      string
	refreshes    int
	invalidGrant bool
	registered   int
	bearers      []string
}

func newFakeMCPOAuth(t *testing.T) *fakeMCPOAuth {
	f := &fakeMCPOAuth{t: t}
	f.as = httptest.NewServer(http.HandlerFunc(f.serveAS))
	f.mcp = httptest.NewServer(http.HandlerFunc(f.serveMCP))
	t.Cleanup(func() { f.mcp.Close(); f.as.Close() })
	return f
}

func (f *fakeMCPOAuth) mcpURL() string { return f.mcp.URL + "/mcp" }

func (f *fakeMCPOAuth) serveAS(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/.well-known/oauth-authorization-server":
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.as.URL, "authorization_endpoint": f.as.URL + "/authorize", "token_endpoint": f.as.URL + "/token",
			"registration_endpoint": f.as.URL + "/register", "code_challenge_methods_supported": []string{"S256"},
		})
	case "/register":
		f.registered++
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "dyn-client"})
	case "/token":
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "dyn-client" || r.Form.Get("resource") != f.mcpURL() {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_client"})
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
			f.valid, f.refresh = "access-1", "refresh-1"
		case "refresh_token":
			if f.invalidGrant || r.Form.Get("refresh_token") != f.refresh {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
			f.refreshes++
			f.valid, f.refresh = "access-2", "refresh-2"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": f.valid, "refresh_token": f.refresh, "token_type": "Bearer", "expires_in": 3600})
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeMCPOAuth) serveMCP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/.well-known/oauth-protected-resource/mcp" {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": f.mcpURL(), "authorization_servers": []string{f.as.URL}, "scopes_supported": []string{"repo"}})
		return
	}
	f.mu.Lock()
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.bearers = append(f.bearers, bearer)
	ok := bearer != "" && bearer == f.valid
	f.mu.Unlock()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.mcp.URL+`/.well-known/oauth-protected-resource/mcp"`)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var req service.MCPRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.HasPrefix(req.Method, "notifications/") {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result := `{}`
	switch req.Method {
	case "initialize":
		result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}`
	case "tools/list":
		result = `{"tools":[{"name":"whoami_remote","description":"d","inputSchema":{"type":"object"}}]}`
	case "tools/call":
		result = `{"content":[{"type":"text","text":"hello ` + bearer + `"}]}`
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(service.MCPResponse{Jsonrpc: "2.0", ID: req.ID, Result: json.RawMessage(result)})
}

func (f *fakeMCPOAuth) set(fn func(*fakeMCPOAuth)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func TestMCPOAuthEndToEnd(t *testing.T) {
	fake := newFakeMCPOAuth(t)
	service.SetTrustedLocalMCPLoader(func(context.Context) ([]string, error) { return []string{fake.mcpURL()}, nil })
	t.Cleanup(func() { service.SetTrustedLocalMCPLoader(nil) })

	var err error
	f := newMachineFixture(t)
	f.s.connectionStore = f.store
	req := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(`{"mode":"trusted_host","version":1,"allow_all_tools":true,"allow_all_nodes":true}`)).WithContext(f.ctx)
	req.SetPathValue("workspace", f.workspace)
	pw := httptest.NewRecorder()
	f.s.RuntimeExecutionPolicyAPI(pw, req)
	if pw.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", pw.Code, pw.Body.String())
	}
	if f.ctx, err = f.s.bindRuntimePrincipal(service.WithAccessPrincipal(t.Context(), mustPrincipal(t, f.ctx)), "test"); err != nil {
		t.Fatal(err)
	}
	set, err := f.store.CreateMCPSet(f.ctx, service.MCPSet{Name: "github", Config: service.MCPServerConfig{
		MCPUpstreams: []service.MCPUpstream{{URL: fake.mcpURL(), Auth: &service.MCPUpstreamAuth{Type: "oauth2"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	provider := set.Config.MCPUpstreams[0].Auth.Provider
	if provider != "mcp-127-0-0-1" || len(set.Config.MCPUpstreams[0].Auth.Accounts) != 1 {
		t.Fatalf("auth not normalized on save: %+v", set.Config.MCPUpstreams[0].Auth)
	}

	// Without an account the tool call fails with guidance, never silently.
	accounts := func() []mcpOAuthAccountTarget {
		t.Helper()
		w := httptest.NewRecorder()
		f.s.MCPOAuthAccountsAPI(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp/oauth/accounts", nil).WithContext(f.ctx))
		if w.Code != http.StatusOK {
			t.Fatalf("accounts: %d %s", w.Code, w.Body.String())
		}
		var out []mcpOAuthAccountTarget
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := accounts(); len(got) != 1 || got[0].SetID != set.ID || got[0].Provider != provider || got[0].Account != nil || strings.Contains(got[0].Server, "/mcp") {
		t.Fatalf("accounts before connecting: %+v", got)
	}
	if _, err := f.s.listExecutionMCPSetTools(f.ctx, "github"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.callExecutionMCPSetTool(f.ctx, "github", "whoami_remote", nil); err == nil || !strings.Contains(err.Error(), "connect your account") {
		t.Fatalf("missing account: %v", err)
	}

	// Start.
	body := `{"set_id":"` + set.ID + `","upstream_index":0,"connection_name":"me"}`
	w := httptest.NewRecorder()
	f.s.MCPOAuthStartAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/oauth/start", strings.NewReader(body)).WithContext(f.ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	var started struct {
		AuthorizeURL string `json:"authorize_url"`
		RedirectURI  string `json:"redirect_uri"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	authorize, err := url.Parse(started.AuthorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	q := authorize.Query()
	if q.Get("client_id") != "dyn-client" || q.Get("resource") != fake.mcpURL() || q.Get("code_challenge_method") != "S256" || q.Get("scope") != "repo" {
		t.Fatalf("authorize URL: %s", started.AuthorizeURL)
	}
	state := q.Get("state")
	if mcpOAuthStateWorkspace(state) != f.workspace {
		t.Fatalf("state carries no workspace selector: %q", state)
	}

	// A replayed or foreign state is refused.
	callback := func(state string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		target := "/api/v1/mcp/oauth/callback?code=the-code&state=" + url.QueryEscape(state)
		f.s.MCPOAuthCallbackAPI(w, httptest.NewRequest(http.MethodGet, target, nil).WithContext(f.ctx))
		return w
	}
	if w := callback(f.workspace + ".forged"); w.Code != http.StatusBadRequest {
		t.Fatalf("forged state: %d", w.Code)
	}
	if w := callback(state); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) || strings.Contains(w.Body.String(), "access-1") {
		t.Fatalf("callback: %d %s", w.Code, w.Body.String())
	}
	if w := callback(state); w.Code != http.StatusBadRequest {
		t.Fatalf("state replay: %d", w.Code)
	}

	conns, err := f.store.ListConnectionsByProvider(f.ctx, provider)
	if err != nil || len(conns) != 1 || conns[0].Scope != service.ConnectionScopePersonal || conns[0].Credentials.MCPOAuth.AccessToken != "access-1" {
		t.Fatalf("stored connection: %+v %v", conns, err)
	}
	if got := accounts(); len(got) != 1 || got[0].Account == nil || got[0].Account.ConnectionID != conns[0].ID {
		t.Fatalf("accounts after connecting: %+v", got)
	}

	// Tool call with the stored token.
	if got, err := f.s.callExecutionMCPSetTool(f.ctx, "github", "whoami_remote", nil); err != nil || got != "hello access-1" {
		t.Fatalf("call: %q %v", got, err)
	}

	// The server revokes the token; the client refreshes exactly once (the
	// fake AS makes the refreshed token the one the MCP server accepts).
	fake.set(func(f *fakeMCPOAuth) { f.valid = "access-1-revoked" })
	got, err := f.s.callExecutionMCPSetTool(f.ctx, "github", "whoami_remote", nil)
	if err != nil || got != "hello access-2" {
		t.Fatalf("refresh call: %q %v", got, err)
	}
	if fake.refreshes != 1 {
		t.Fatalf("refreshed %d times", fake.refreshes)
	}

	// The refresh token stops working: the account must be reconnected, and
	// no other account or static header is tried.
	fake.set(func(f *fakeMCPOAuth) { f.valid = "revoked-again"; f.invalidGrant = true })
	_, err = f.s.callExecutionMCPSetTool(f.ctx, "github", "whoami_remote", nil)
	if err == nil || !errors.Is(err, service.ErrMCPOAuthReauthRequired) && !strings.Contains(err.Error(), "reconnect") {
		t.Fatalf("invalid_grant: %v", err)
	}
	conns, _ = f.store.ListConnectionsByProvider(f.ctx, provider)
	if len(conns) != 1 || !conns[0].Credentials.MCPOAuth.NeedsReauth {
		t.Fatalf("reauth not recorded: %+v", conns)
	}
	for _, bearer := range fake.bearers {
		if bearer == "refresh-1" || bearer == "refresh-2" {
			t.Fatal("refresh token sent to the MCP server")
		}
	}

	// Reconnecting the same connection reuses the registered client and clears the flag.
	w = httptest.NewRecorder()
	f.s.MCPOAuthStartAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/oauth/start", strings.NewReader(`{"connection_id":"`+conns[0].ID+`"}`)).WithContext(f.ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("reconnect start: %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &started)
	authorize, _ = url.Parse(started.AuthorizeURL)
	fake.set(func(f *fakeMCPOAuth) { f.invalidGrant = false })
	if w := callback(authorize.Query().Get("state")); w.Code != http.StatusOK {
		t.Fatalf("reconnect callback: %d %s", w.Code, w.Body.String())
	}
	if fake.registered != 1 {
		t.Fatalf("registered %d clients", fake.registered)
	}
	conns, _ = f.store.ListConnectionsByProvider(f.ctx, provider)
	if len(conns) != 1 || conns[0].Credentials.MCPOAuth.NeedsReauth {
		t.Fatalf("reconnect: %+v", conns)
	}
	if got, err := f.s.callExecutionMCPSetTool(f.ctx, "github", "whoami_remote", nil); err != nil || got != "hello access-1" {
		t.Fatalf("after reconnect: %q %v", got, err)
	}
}

func mustPrincipal(t *testing.T, ctx context.Context) service.AccessPrincipal {
	t.Helper()
	p, ok := service.AccessPrincipalFromContext(ctx)
	if !ok {
		t.Fatal("no principal")
	}
	return p
}
