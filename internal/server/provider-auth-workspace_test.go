package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

type providerAuthTestTransport func(*http.Request) (*http.Response, error)

func (f providerAuthTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProviderPageClaudeAuthorizationKeepsSelectedWorkspace(t *testing.T) {
	store := postgrestest.New(t, bytes.Repeat([]byte{1}, 32))
	admin, err := store.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: password.Dummy, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), store)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store, nativeAuth: a, config: config.Server{BasePath: "/at"}, providers: map[string]ProviderInfo{"claude": {defaultModel: "unchanged-global"}}, providerFactory: func(config.LLMConfig) (service.LLMProvider, error) { return codexAuthTestProvider{}, nil }}
	exchanges := 0
	verifiers := map[string]string{}
	s.providerAuthClientFactory = func(string, bool) (*http.Client, error) {
		return &http.Client{Transport: providerAuthTestTransport(func(r *http.Request) (*http.Response, error) {
			if err := r.ParseForm(); err != nil {
				return nil, err
			}
			code := r.PostForm.Get("code")
			if verifiers[code] == "" || r.PostForm.Get("code_verifier") != verifiers[code] {
				return nil, fmt.Errorf("wrong PKCE verifier for %s", code)
			}
			exchanges++
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"access_token":%q,"refresh_token":%q,"expires_in":3600}`, "access-"+code, "refresh-"+code)))}, nil
		})}, nil
	}
	mux := ada.New()
	a.register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(s.workspaceBusinessAuthentication())
	api.POST("/v1/providers", s.CreateProviderAPI)
	api.POST("/v1/providers/claude-auth", s.ClaudeAuthStartAPI)
	api.POST("/v1/providers/claude-auth/callback", s.ClaudeAuthCallbackAPI)
	api.POST("/v1/providers/claude-auth/token", s.ClaudeAuthTokenAPI)
	api.POST("/v1/providers/discover-models", s.DiscoverModelsAPI)
	cookie := nativeLoginCookie(t, mux, "admin")
	call := func(path, workspace, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/at/api/v1/providers"+path, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Origin", a.cfg.Origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-AT-Workspace-ID", workspace)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	var workspaces []string
	var contexts []context.Context
	for _, name := range []string{"one", "two"} {
		workspace, err := store.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID}), name, admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		workspaces = append(workspaces, workspace.ID)
		actor, _, err := store.ResolveWorkspaceAccess(t.Context(), workspace.ID, admin.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		contexts = append(contexts, service.WithAccessPrincipal(t.Context(), actor))
		w := call("", workspace.ID, `{"key":"claude","config":{"type":"anthropic","auth_type":"claude-code","model":"claude-test"}}`)
		if w.Code != http.StatusCreated {
			t.Fatalf("create provider: %d %s", w.Code, w.Body.String())
		}
	}
	// Two equally named providers must keep independent PKCE state. Use the
	// real UI middleware (cookies + selected-workspace header) for every step.
	for i, workspace := range workspaces {
		w := call("/claude-auth", workspace, `{"key":"claude"}`)
		if w.Code != http.StatusOK || w.Header().Get("X-AT-Scope") != "workspace" {
			t.Fatalf("start: %d %s", w.Code, w.Body.String())
		}
		var result claudeAuthStartResponse
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		authURL, err := url.Parse(result.AuthURL)
		if err != nil {
			t.Fatal(err)
		}
		claudeAuthFlows.mu.Lock()
		for key, flow := range claudeAuthFlows.flows {
			if key.WorkspaceID == workspace && flow.State == authURL.Query().Get("state") {
				verifiers[fmt.Sprintf("code-%d", i)] = flow.Verifier
			}
		}
		claudeAuthFlows.mu.Unlock()
	}
	for i, workspace := range workspaces {
		w := call("/claude-auth/callback", workspace, fmt.Sprintf(`{"key":"claude","code":"code-%d"}`, i))
		if w.Code != http.StatusOK {
			t.Fatalf("callback: %d %s", w.Code, w.Body.String())
		}
		record, err := store.GetProvider(contexts[i], "claude")
		if err != nil || record == nil || record.Config.RefreshToken != fmt.Sprintf("refresh-code-%d", i) || record.Config.TokenExpiresAt == "" {
			t.Fatalf("tokens not saved in selected workspace: %+v %v", record, err)
		}
	}
	if exchanges != 2 {
		t.Fatalf("exchanges = %d", exchanges)
	}
	modelsAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-code-") {
			t.Errorf("missing workspace credential: %s", r.Header.Get("Authorization"))
		}
		model := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer access-")
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": model}}, "has_more": false})
	}))
	defer modelsAPI.Close()
	for i, workspace := range workspaces {
		body, _ := json.Marshal(discoverRequest{Key: "claude", Config: config.LLMConfig{Type: "anthropic", BaseURL: modelsAPI.URL}})
		w := call("/discover-models", workspace, string(body))
		if w.Code != 200 || !strings.Contains(w.Body.String(), fmt.Sprintf("code-%d", i)) || w.Header().Get("X-AT-Scope") != "workspace" {
			t.Fatalf("scoped discovery: %d %s", w.Code, w.Body.String())
		}
	}
	w := call("/claude-auth/token", workspaces[0], `{"key":"claude","access_token":"pasted-access","refresh_token":"pasted-refresh"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("token paste: %d %s", w.Code, w.Body.String())
	}
	first, err := store.GetProvider(contexts[0], "claude")
	if err != nil || first.Config.RefreshToken != "pasted-refresh" {
		t.Fatal("paste was not persisted")
	}
	second, err := store.GetProvider(contexts[1], "claude")
	if err != nil || second.Config.RefreshToken != "refresh-code-1" {
		t.Fatal("paste changed another workspace")
	}
	if s.providers["claude"].defaultModel != "unchanged-global" {
		t.Fatal("workspace authorization replaced global gateway provider")
	}
	if w := call("/claude-auth/token", "foreign", `{"key":"claude","access_token":"bad","refresh_token":"bad"}`); w.Code != http.StatusForbidden {
		t.Fatalf("foreign workspace: %d %s", w.Code, w.Body.String())
	}
}

func TestDeviceProviderAuthorizationPersistsInWorkspace(t *testing.T) {
	f := newMachineFixture(t)
	for _, authType := range []string{"chatgpt", "copilot"} {
		record, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: authType, Config: config.LLMConfig{Type: "openai", AuthType: authType, Model: "test"}})
		if err != nil {
			t.Fatal(err)
		}
		if authType == "chatgpt" {
			err = f.s.saveCodexAuthTokens(f.ctx, record.Key, codexDeviceProviderSnapshot{ID: record.ID, Type: "openai"}, &openai.CodexTokens{AccessToken: "access", RefreshToken: "refresh", AccountID: "account"})
		} else {
			err = f.s.saveDeviceAuthToken(f.ctx, record.Key, record.ID, "access")
		}
		if err != nil {
			t.Fatalf("%s: %v", authType, err)
		}
		saved, err := f.store.GetProvider(f.ctx, record.Key)
		if err != nil || saved.Config.APIKey != "access" {
			t.Fatalf("%s not persisted: %v", authType, err)
		}
	}
}
