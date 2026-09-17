package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

type wrongGlobalCodex struct{ codexAuthTestProvider }

func (wrongGlobalCodex) Models(context.Context) ([]string, error) {
	return []string{"WRONG-GLOBAL"}, nil
}

func TestCodexDiscoveryRotatesOnceAcrossInstancesAndWorkspaces(t *testing.T) {
	p := postgrestest.New(t, bytes.Repeat([]byte{9}, 32))
	admin, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "admin", PasswordHash: testPasswordHash, Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var rotations atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["refresh_token"] != "refresh-one" {
				http.Error(w, "refresh token reused", 401)
				return
			}
			if rotations.Add(1) != 1 {
				http.Error(w, "refresh token reused", 401)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-new","refresh_token":"refresh-new","expires_in":3600}`))
			return
		}
		if r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("client_version") != openai.CodexClientVersion {
			t.Errorf("unexpected catalog URL: %s", r.URL)
			http.NotFound(w, r)
			return
		}
		account := r.Header.Get("ChatGPT-Account-ID")
		want := "Bearer access-new"
		if account == "two" {
			want = "Bearer access-two"
		}
		if r.Header.Get("Authorization") != want {
			http.Error(w, "wrong account credential", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"slug": "model-" + account}}})
	}))
	defer upstream.Close()
	a, err := newNativeAuth(nativeTestConfig(), p)
	if err != nil {
		t.Fatal(err)
	}
	factory := func(cfg config.LLMConfig) (service.LLMProvider, error) {
		expiry, _ := time.Parse(time.RFC3339, cfg.TokenExpiresAt)
		source := openai.NewCodexTokenSource(cfg.APIKey, cfg.RefreshToken, cfg.ExtraHeaders["ChatGPT-Account-ID"], expiry, upstream.Client(), openai.CodexAuthEndpoints{OAuthTokenURL: upstream.URL + "/oauth/token"})
		return openai.NewCodexProvider("", cfg.ExtraHeaders["ChatGPT-Account-ID"], source, openai.WithCodexBaseURL(cfg.BaseURL), openai.WithCodexHTTPClient(upstream.Client())), nil
	}
	var servers []*ada.Server
	for range 2 {
		s := &Server{store: p, nativeAuth: a, config: config.Server{BasePath: "/at"}, providerFactory: factory, version: "dev", providers: map[string]ProviderInfo{"openai": {provider: wrongGlobalCodex{}}}}
		mux := ada.New()
		a.register(mux, "/at")
		api := mux.Group("/at/api")
		api.Use(s.workspaceBusinessAuthentication())
		api.POST("/v1/providers/discover-models", s.DiscoverModelsAPI)
		servers = append(servers, mux)
	}
	cookie := nativeLoginCookie(t, servers[0], "admin")
	var scopes []context.Context
	var ids []string
	for _, name := range []string{"one", "two"} {
		ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: admin.ID})
		workspace, err := p.CreateWorkspace(ctx, name, admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		actor, _, err := p.ResolveWorkspaceAccess(ctx, workspace.ID, admin.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		ctx = service.WithAccessPrincipal(ctx, actor)
		scopes = append(scopes, ctx)
		ids = append(ids, workspace.ID)
		expiry := time.Now().Add(-time.Hour)
		if name == "two" {
			expiry = time.Now().Add(time.Hour)
		}
		_, err = p.CreateProvider(ctx, service.ProviderRecord{Key: "openai", Config: config.LLMConfig{Type: "openai", AuthType: "chatgpt", APIKey: "access-" + name, RefreshToken: "refresh-" + name, TokenExpiresAt: expiry.Format(time.RFC3339), BaseURL: upstream.URL + "/backend-api/codex/responses", ExtraHeaders: map[string]string{"ChatGPT-Account-ID": name}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	call := func(mux *ada.Server, index int) {
		body := fmt.Sprintf(`{"key":"openai","config":{"type":"openai","auth_type":"chatgpt","base_url":%q}}`, upstream.URL+"/backend-api/codex/responses")
		r := httptest.NewRequest("POST", "/at/api/v1/providers/discover-models", strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", a.cfg.Origin)
		r.Header.Set("X-AT-Workspace-ID", ids[index])
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		want := "model-one"
		if index == 1 {
			want = "model-two"
		}
		if w.Code != 200 || !strings.Contains(w.Body.String(), want) {
			t.Errorf("discovery = %d %s", w.Code, w.Body)
		}
	}
	var wg sync.WaitGroup
	for _, mux := range servers {
		wg.Go(func() { call(mux, 0) })
	}
	wg.Wait()
	call(servers[0], 1)
	if rotations.Load() != 1 {
		t.Fatalf("refresh used %d times", rotations.Load())
	}
	stored, err := p.GetProvider(scopes[0], "openai")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Config.RefreshToken != "refresh-new" || stored.Config.APIKey != "access-new" {
		t.Fatal("rotated credentials were not saved")
	}
	other, err := p.GetProvider(scopes[1], "openai")
	if err != nil {
		t.Fatal(err)
	}
	if other.Config.RefreshToken != "refresh-two" {
		t.Fatal("another workspace credential was overwritten")
	}
	// A fresh process/source reads the saved credential, not the consumed token.
	call(servers[1], 0)
	if rotations.Load() != 1 {
		t.Fatal("restarted source repeated a rotation")
	}
	if err := p.RotateCodexOAuthTokens(t.Context(), ids[0], "openai", "refresh-one", service.CodexOAuthTokens{AccessToken: "stale", RefreshToken: "stale", AccountID: "one"}); err == nil {
		t.Fatal("stale credential overwrote the new token")
	}
}
