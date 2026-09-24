package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestPersonalProviderAPIOwnershipAndRedaction(t *testing.T) {
	f := newMachineFixture(t)
	member, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, f.member, "")
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := service.WithAccessPrincipal(t.Context(), member)

	create := httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers", strings.NewReader(`{
		"key":"private-openai",
		"config":{"type":"openai","model":"gpt-private","api_key":"owner-secret","base_url":"https://private.example/v1/chat/completions","proxy":"http://proxy.example:8080","insecure_skip_verify":true}
	}`)).WithContext(memberCtx)
	w := httptest.NewRecorder()
	f.s.CreatePersonalProviderAPI(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", w.Code, w.Body.String())
	}
	var created service.ProviderRecord
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.OwnerUserID != f.member || created.WorkspaceID != "" || created.Scope != service.ProviderScopePersonal || created.Reference != service.PersonalProviderReference(created.ID) {
		t.Fatalf("created ownership: %+v", created)
	}
	if created.Config.APIKey != "***" || created.Config.BaseURL != "https://private.example/v1/chat/completions" || created.Config.Proxy != "http://proxy.example:8080" || !created.Config.InsecureSkipVerify {
		t.Fatalf("response redaction/transport: %+v", created.Config)
	}

	ownerRequest := httptest.NewRequest(http.MethodGet, "/api/v1/personal-providers/"+created.ID, nil).WithContext(memberCtx)
	ownerRequest.SetPathValue("id", created.ID)
	w = httptest.NewRecorder()
	f.s.GetPersonalProviderAPI(w, ownerRequest)
	if w.Code != http.StatusOK {
		t.Fatalf("owner get status %d: %s", w.Code, w.Body.String())
	}

	foreignRequest := httptest.NewRequest(http.MethodGet, "/api/v1/personal-providers/"+created.ID, nil).WithContext(f.ctx)
	foreignRequest.SetPathValue("id", created.ID)
	w = httptest.NewRecorder()
	f.s.GetPersonalProviderAPI(w, foreignRequest)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign get status %d: %s", w.Code, w.Body.String())
	}

	discoveryRequest := httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers/discover-models", nil).WithContext(memberCtx)
	discovery := discoverRequest{ProviderID: created.ID, Config: created.Config}
	w = httptest.NewRecorder()
	loaded, admitted := f.s.discoveryConfig(w, discoveryRequest, &discovery, true)
	if !admitted || loaded == nil || discovery.Config.APIKey != "owner-secret" || discovery.Key != created.Reference {
		t.Fatalf("owner discovery credential resolution: admitted=%v loaded=%+v request=%+v response=%s", admitted, loaded, discovery, w.Body.String())
	}
	discoveryRequest = httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers/discover-models", nil).WithContext(f.ctx)
	discovery = discoverRequest{ProviderID: created.ID, Config: created.Config}
	w = httptest.NewRecorder()
	if loaded, admitted = f.s.discoveryConfig(w, discoveryRequest, &discovery, true); admitted || loaded != nil || w.Code != http.StatusNotFound {
		t.Fatalf("foreign discovery admitted=%v loaded=%+v status=%d", admitted, loaded, w.Code)
	}

	authRequest := httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers/claude-auth", nil).WithContext(f.ctx)
	if record, ok := f.s.providerAuthRecord(httptest.NewRecorder(), authRequest, created.Reference, "copilot"); ok || record != nil {
		t.Fatalf("foreign OAuth authorization loaded provider: %+v", record)
	}

	disable := httptest.NewRequest(http.MethodPut, "/api/v1/personal-providers/"+created.ID+"/disable", strings.NewReader(`{"disabled":true}`)).WithContext(f.ctx)
	disable.SetPathValue("id", created.ID)
	w = httptest.NewRecorder()
	f.s.SetPersonalProviderDisabledAPI(w, disable)
	if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
		t.Fatalf("foreign disable status %d: %s", w.Code, w.Body.String())
	}
}

func TestPersonalProviderClaudeAuthorizationIsOwnerScoped(t *testing.T) {
	f := newMachineFixture(t)
	member, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, f.member, "")
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := service.WithAccessPrincipal(t.Context(), member)
	provider, err := f.store.CreatePersonalProvider(memberCtx, service.ProviderRecord{
		Key:    "claude-private",
		Config: config.LLMConfig{Type: "anthropic", AuthType: "claude-code", Model: "claude-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.s.providerAuthClientFactory = func(string, bool) (*http.Client, error) {
		return &http.Client{Transport: providerAuthTestTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"owner-access","refresh_token":"owner-refresh","expires_in":3600}`))}, nil
		})}, nil
	}

	start := httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers/claude-auth", strings.NewReader(`{"key":"`+provider.Reference+`"}`)).WithContext(memberCtx)
	w := httptest.NewRecorder()
	f.s.ClaudeAuthStartAPI(w, start)
	if w.Code != http.StatusOK {
		t.Fatalf("start status %d: %s", w.Code, w.Body.String())
	}
	var started claudeAuthStartResponse
	if err := json.Unmarshal(w.Body.Bytes(), &started); err != nil {
		t.Fatal(err)
	}
	authURL, err := url.Parse(started.AuthURL)
	if err != nil {
		t.Fatal(err)
	}
	code := "owner-code#" + authURL.Query().Get("state")
	body, _ := json.Marshal(claudeAuthCallbackRequest{Key: provider.Reference, Code: code})

	foreign := httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers/claude-auth/callback", strings.NewReader(string(body))).WithContext(f.ctx)
	w = httptest.NewRecorder()
	f.s.ClaudeAuthCallbackAPI(w, foreign)
	if w.Code != http.StatusNotFound {
		t.Fatalf("foreign callback status %d: %s", w.Code, w.Body.String())
	}

	callback := httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers/claude-auth/callback", strings.NewReader(string(body))).WithContext(memberCtx)
	w = httptest.NewRecorder()
	f.s.ClaudeAuthCallbackAPI(w, callback)
	if w.Code != http.StatusOK {
		t.Fatalf("owner callback status %d: %s", w.Code, w.Body.String())
	}
	stored, err := f.store.GetPersonalProvider(memberCtx, provider.ID)
	if err != nil || stored.Config.RefreshToken != "owner-refresh" {
		t.Fatalf("owner OAuth persistence: %+v %v", stored, err)
	}
}
