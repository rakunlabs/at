package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/mcpauth"
)

func mcpAuthTestServer(t *testing.T, f machineFixture) (*httptest.Server, string) {
	t.Helper()
	srv, err := f.store.CreateMCPServer(f.ctx, service.MCPServer{Name: "docs", Config: service.MCPServerConfig{
		EnabledBuiltinTools: []string{"task_list"},
		OAuth:               &service.MCPServerOAuth{Enabled: true, DynamicClients: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	_ = srv
	mux := ada.New()
	mux.Group("/gateway").POST("/v1/mcp/{name}", f.s.GatewayMCPHandler)
	f.s.registerMCPAuthServerRoutes(mux, "")
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	f.s.config.ExternalURL = httpServer.URL
	return httpServer, httpServer.URL + "/gateway/v1/mcp/docs"
}

func consentParams(clientID, redirect, resource string) (map[string]string, string) {
	pkce, _ := mcpauth.NewPKCE()
	return map[string]string{
		"response_type": "code", "client_id": clientID, "redirect_uri": redirect, "state": "s",
		"code_challenge": pkce.Challenge, "code_challenge_method": "S256", "resource": resource,
	}, pkce.Verifier
}

func postConsent(t *testing.T, f machineFixture, ctx context.Context, body mcpAuthConsentRequest) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	f.s.MCPAuthConsentAPI(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp-auth/authorize", strings.NewReader(string(raw))).WithContext(ctx))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusOK {
		t.Fatalf("consent: %d %s", w.Code, w.Body.String())
	}
	return out
}

// TestMCPAuthClientMetadataDocument covers clients identified by a Client ID
// Metadata Document URL: discovery advertises support, the document is
// fetched once and cached, mismatched or secret-bearing documents are
// refused without redirecting, and a failed refresh keeps a recent copy.
func TestMCPAuthClientMetadataDocument(t *testing.T) {
	f := newMachineFixture(t)
	httpServer, resource := mcpAuthTestServer(t, f)
	clientID := "https://app.example/oauth/client.json"
	redirect := "http://127.0.0.1:55001/cb"
	var fetches atomic.Int32
	doc := service.MCPAuthClientMetadata{ClientID: clientID, ClientName: "Example App", RedirectURIs: []string{redirect}, TokenEndpointAuthMethod: "none"}
	var fail atomic.Bool
	prev := mcpAuthMetadataFetch
	mcpAuthMetadataFetch = func(_ context.Context, id string) (*mcpAuthMetadataResult, error) {
		fetches.Add(1)
		if fail.Load() {
			return nil, errors.New("unreachable")
		}
		d := doc
		if id != clientID {
			d.ClientID = clientID // the document names another URL
		}
		return &mcpAuthMetadataResult{metadata: d, ttl: time.Hour}, nil
	}
	t.Cleanup(func() { mcpAuthMetadataFetch = prev })

	meta, err := mcpauth.Discover(t.Context(), mcpauth.Client(true), resource, "")
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.s.MCPAuthServerMetadataAPI(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(w.Body.String(), `"client_id_metadata_document_supported":true`) {
		t.Fatalf("metadata does not advertise CIMD: %s", w.Body.String())
	}

	params, verifier := consentParams(clientID, redirect, resource)
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := make([]string, 0, len(params))
	for k, v := range params {
		q = append(q, k+"="+v)
	}
	authorize := httpServer.URL + "/oauth/mcp/authorize?" + strings.ReplaceAll(strings.Join(q, "&"), ":", "%3A")
	if r, err := noRedirect.Get(authorize); err != nil || r.StatusCode != http.StatusFound || !strings.Contains(r.Header.Get("Location"), "#/oauth/mcp/authorize?") {
		t.Fatalf("authorize with metadata client: %v %+v", err, r)
	}

	// The consent page shows the verified host; an approval issues a code
	// the token endpoint accepts for the URL client_id.
	info := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp-auth/authorize?"+strings.Join(q, "&"), nil).WithContext(f.ctx)
	f.s.MCPAuthConsentInfoAPI(info, req)
	if !strings.Contains(info.Body.String(), `"client_verified_host":"app.example"`) || !strings.Contains(info.Body.String(), "Example App") {
		t.Fatalf("consent info: %s", info.Body.String())
	}
	code := mustQuery(t, postConsent(t, f, f.ctx, mcpAuthConsentRequest{Params: params, Approve: true})["redirect"].(string)).Get("code")
	tok, err := mcpauth.Exchange(t.Context(), mcpauth.Client(true), meta.TokenEndpoint, clientID, "", code, redirect, verifier, resource)
	if err != nil || tok.AccessToken == "" {
		t.Fatalf("exchange: %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("document fetched %d times, want 1 (cached)", n)
	}

	// Stale-if-error: an expired copy keeps working while the host is down.
	if _, err := f.store.UpsertMetadataMCPAuthClient(t.Context(), service.MCPAuthClient{ClientID: clientID, Name: "Example App", RedirectURIs: []string{redirect}, MetadataExpiresAt: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	mcpAuthMetadataFailures.Delete(clientID)
	if c, err := f.s.resolveMCPAuthClient(httptest.NewRequest(http.MethodGet, "/", nil).WithContext(t.Context()), clientID); err != nil || c == nil {
		t.Fatalf("stale copy not served: %v %v", c, err)
	}

	// A document whose client_id does not match its URL is refused, and the
	// error is shown, not redirected.
	other := "https://app.example/other.json"
	fail.Store(false)
	params["client_id"] = other
	q = q[:0]
	for k, v := range params {
		q = append(q, k+"="+v)
	}
	r, err := noRedirect.Get(httpServer.URL + "/oauth/mcp/authorize?" + strings.ReplaceAll(strings.Join(q, "&"), ":", "%3A"))
	if err != nil || r.StatusCode != http.StatusBadRequest {
		t.Fatalf("mismatched document accepted: %v %+v", err, r)
	}
}

func TestMCPAuthClientMetadataValidation(t *testing.T) {
	id := "https://app.example/c.json"
	ok := service.MCPAuthClientMetadata{ClientID: id, RedirectURIs: []string{"https://app.example/cb"}}
	tests := []struct {
		name string
		id   string
		m    func(service.MCPAuthClientMetadata) service.MCPAuthClientMetadata
		want bool
	}{
		{"valid", id, func(m service.MCPAuthClientMetadata) service.MCPAuthClientMetadata { return m }, true},
		{"secret", id, func(m service.MCPAuthClientMetadata) service.MCPAuthClientMetadata { m.ClientSecret = "x"; return m }, false},
		{"secret method", id, func(m service.MCPAuthClientMetadata) service.MCPAuthClientMetadata {
			m.TokenEndpointAuthMethod = "client_secret_basic"
			return m
		}, false},
		{"mismatch", id, func(m service.MCPAuthClientMetadata) service.MCPAuthClientMetadata {
			m.ClientID = "https://x/y"
			return m
		}, false},
		{"http redirect", id, func(m service.MCPAuthClientMetadata) service.MCPAuthClientMetadata {
			m.RedirectURIs = []string{"http://evil.example/cb"}
			return m
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := service.ValidateMCPAuthClientMetadata(tt.id, tt.m(ok)); (err == nil) != tt.want {
				t.Fatalf("err = %v", err)
			}
		})
	}
	for _, raw := range []string{"http://app.example/c.json", "https://app.example/", "https://app.example/c.json?x=1", "https://u:p@app.example/c", "https://app.example/a/../c", "atmc_abc"} {
		if service.MCPAuthMetadataClientID(raw) {
			t.Errorf("%q accepted as metadata client_id", raw)
		}
	}
	if !service.MCPAuthMetadataClientID(id) {
		t.Error("valid metadata client_id refused")
	}
	if metadataCacheTTL("max-age=10") != mcpAuthMetadataMinTTL || metadataCacheTTL("max-age=999999") != mcpAuthMetadataMaxTTL || metadataCacheTTL("no-store") != mcpAuthMetadataMinTTL {
		t.Error("cache TTL bounds")
	}
}

// The real fetcher refuses non-public addresses at connect time.
func TestMCPAuthClientMetadataRefusesPrivateAddresses(t *testing.T) {
	internal := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer internal.Close()
	if _, err := fetchMCPAuthClientMetadata(t.Context(), internal.URL+"/client.json"); err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("loopback metadata fetched: %v", err)
	}
}

// TestMCPAuthRememberedConsent: a remembered consent issues a code without
// the page for the same redirect only, and can be forgotten.
func TestMCPAuthRememberedConsent(t *testing.T) {
	f := newMachineFixture(t)
	_, resource := mcpAuthTestServer(t, f)
	redirect := "http://127.0.0.1:41000/cb"
	client, err := f.store.RegisterDynamicMCPAuthClient(t.Context(), service.MCPAuthClient{ClientID: "atmc_remember", Name: "Cursor", RedirectURIs: []string{redirect}})
	if err != nil {
		t.Fatal(err)
	}
	params, _ := consentParams(client.ClientID, redirect, resource)

	if out := postConsent(t, f, f.ctx, mcpAuthConsentRequest{Params: params, Approve: true, Check: true}); out["remembered"] != false || out["redirect"] != nil {
		t.Fatalf("check before remembering issued a code: %v", out)
	}
	postConsent(t, f, f.ctx, mcpAuthConsentRequest{Params: params, Approve: true, Remember: true})
	out := postConsent(t, f, f.ctx, mcpAuthConsentRequest{Params: params, Approve: true, Check: true})
	if out["remembered"] != true || mustQuery(t, out["redirect"].(string)).Get("code") == "" {
		t.Fatalf("remembered consent not applied: %v", out)
	}
	// Another loopback port is a different redirect: ask again.
	other := map[string]string{}
	for k, v := range params {
		other[k] = v
	}
	other["redirect_uri"] = "http://127.0.0.1:41999/cb"
	if out := postConsent(t, f, f.ctx, mcpAuthConsentRequest{Params: other, Approve: true, Check: true}); out["remembered"] != false {
		t.Fatalf("remembered consent applied to another redirect: %v", out)
	}

	w := httptest.NewRecorder()
	f.s.ListMCPAuthGrantsAPI(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(f.ctx))
	var grants []service.MCPAuthGrant
	_ = json.Unmarshal(w.Body.Bytes(), &grants)
	if len(grants) != 1 || grants[0].RememberedUntil == "" {
		t.Fatalf("grant does not report remembered consent: %s", w.Body.String())
	}
	forget := httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(f.ctx)
	forget.SetPathValue("id", grants[0].ID)
	w = httptest.NewRecorder()
	f.s.ForgetMCPAuthConsentAPI(w, forget)
	if w.Code != http.StatusOK {
		t.Fatalf("forget: %d %s", w.Code, w.Body.String())
	}
	if out := postConsent(t, f, f.ctx, mcpAuthConsentRequest{Params: params, Approve: true, Check: true}); out["remembered"] != false {
		t.Fatalf("forgotten consent still applied: %v", out)
	}
}

// TestMCPAuthClientRegistrationReuseAndAdmin covers registration reuse, the
// installation client list, admin deletion and the unused-client sweep.
func TestMCPAuthClientRegistrationReuseAndAdmin(t *testing.T) {
	f := newMachineFixture(t)
	httpServer, resource := mcpAuthTestServer(t, f)
	meta, err := mcpauth.Discover(t.Context(), mcpauth.Client(true), resource, "")
	if err != nil {
		t.Fatal(err)
	}
	hc := mcpauth.Client(true)
	a, _, err := mcpauth.Register(t.Context(), hc, meta, "http://127.0.0.1:1/cb", "Claude Code")
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := mcpauth.Register(t.Context(), hc, meta, "http://127.0.0.1:1/cb", "Claude Code")
	if err != nil || a != b {
		t.Fatalf("re-registration created another client: %q %q %v", a, b, err)
	}
	c, _, err := mcpauth.Register(t.Context(), hc, meta, "http://127.0.0.1:2/cb", "Claude Code")
	if err != nil || c == a {
		t.Fatalf("different redirect reused the client: %v", err)
	}
	// Root fallbacks and the OpenID document exist for clients without discovery.
	for _, path := range []string{"/.well-known/openid-configuration", "/oauth/mcp/jwks"} {
		r, err := http.Get(httpServer.URL + path)
		if err != nil || r.StatusCode != http.StatusOK {
			t.Fatalf("%s: %v %+v", path, err, r)
		}
	}
	r, err := http.Post(httpServer.URL+"/register", "application/json", strings.NewReader(`{"client_name":"Root","redirect_uris":["http://127.0.0.1:3/cb"]}`))
	if err != nil || r.StatusCode != http.StatusCreated {
		t.Fatalf("root /register: %v %+v", err, r)
	}

	// The account signs in through client a.
	params, _ := consentParams(a, "http://127.0.0.1:1/cb", resource)
	postConsent(t, f, f.ctx, mcpAuthConsentRequest{Params: params, Approve: true})

	list := httptest.NewRecorder()
	f.s.ListAllMCPAuthClientsAPI(list, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(f.ctx))
	var clients []service.MCPAuthClientUsage
	if err := json.Unmarshal(list.Body.Bytes(), &clients); err != nil || list.Code != http.StatusOK || len(clients) != 3 {
		t.Fatalf("admin list: %d %s", list.Code, list.Body.String())
	}
	var used *service.MCPAuthClientUsage
	for i := range clients {
		if clients[i].ClientID == a {
			used = &clients[i]
		}
	}
	if used == nil || used.Grants != 1 || used.ActiveUsers != 1 || len(used.Users) != 1 || used.Users[0].ServerName != "docs" {
		t.Fatalf("usage: %+v", used)
	}

	// Members cannot see the installation list.
	if err := f.store.CreateAuthSession(t.Context(), service.AuthSession{Hash: "member-clients", UserID: f.member, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	memberPrincipal, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, f.member, "member-clients")
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := service.WithAccessPrincipal(t.Context(), memberPrincipal)
	w := httptest.NewRecorder()
	f.s.ListAllMCPAuthClientsAPI(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(memberCtx))
	if w.Code != http.StatusForbidden {
		t.Fatalf("member listed clients: %d", w.Code)
	}

	// The sweep keeps used clients and young unused ones, and removes old
	// unused ones.
	if n, err := f.store.SweepMCPAuthClients(t.Context(), time.Now().Add(-time.Hour)); err != nil || n != 0 {
		t.Fatalf("sweep removed young clients: %d %v", n, err)
	}
	if n, err := f.store.SweepMCPAuthClients(t.Context(), time.Now().Add(time.Hour)); err != nil || n != 2 {
		t.Fatalf("sweep: %d %v", n, err)
	}

	del := httptest.NewRequest(http.MethodDelete, "/", nil).WithContext(f.ctx)
	del.SetPathValue("id", used.ID)
	w = httptest.NewRecorder()
	f.s.AdminDeleteMCPAuthClientAPI(w, del)
	if w.Code != http.StatusOK {
		t.Fatalf("admin delete: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	f.s.ListMCPAuthGrantsAPI(w, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(f.ctx))
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("grant survived client deletion: %s", w.Body.String())
	}
}

// TestMCPAuthDiscoveryUnderBasePath pins where discovery documents answer
// under a deployment sub-path: RFC 8414 §3.1 inserts the well-known segment
// before the issuer path (/.well-known/<doc>/at), OIDC Discovery appends it
// (/at/.well-known/<doc>), and some clients try the bare root.
func TestMCPAuthDiscoveryUnderBasePath(t *testing.T) {
	s := &Server{}
	mux := ada.New()
	s.registerMCPAuthServerRoutes(mux, "/at")
	for _, doc := range []string{"oauth-authorization-server", "openid-configuration"} {
		for _, p := range []string{"/.well-known/" + doc + "/at", "/at/.well-known/" + doc, "/.well-known/" + doc} {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://host"+p, nil))
			if w.Code != http.StatusOK {
				t.Errorf("%s: %d", p, w.Code)
			}
		}
	}
}
