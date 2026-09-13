package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func TestAnthropicModelDiscoveryHeadersPathsAndPagination(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/models" || r.URL.Query().Get("project") != "test" {
			t.Errorf("models URL: %s", r.URL)
		}
		if r.Header.Get("x-api-key") != "key" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("X-Custom") != "configured" {
			t.Errorf("model headers: %v", r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			io.WriteString(w, `{"data":[{"id":"claude-first"}],"has_more":true,"last_id":"cursor /+?"}`)
		} else {
			if r.URL.Query().Get("after_id") != "cursor /+?" {
				t.Errorf("cursor not escaped: %s", r.URL)
			}
			io.WriteString(w, `{"data":[{"id":"claude-second"}],"has_more":false}`)
		}
	}))
	defer upstream.Close()
	models, err := discoverAnthropicModels(t.Context(), config.LLMConfig{BaseURL: upstream.URL + "/v1/messages?project=test", APIKey: "key", ExtraHeaders: map[string]string{"X-Custom": "configured"}})
	if err != nil || len(models) != 2 || models[0] != "claude-first" || calls != 2 {
		t.Fatalf("models %v calls %d: %v", models, calls, err)
	}
}

func TestAnthropicDiscoveryReturnsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid API key", http.StatusUnauthorized)
	}))
	defer upstream.Close()
	body, _ := json.Marshal(discoverRequest{Config: config.LLMConfig{Type: "anthropic", BaseURL: upstream.URL, APIKey: "invalid"}})
	w := httptest.NewRecorder()
	(&Server{}).DiscoverModelsAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))))
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "401") {
		t.Fatalf("upstream failure was hidden: %d %s", w.Code, w.Body.String())
	}
}

func TestAnthropicDiscoveryRefreshesAndPersistsWorkspaceOAuth(t *testing.T) {
	f := newMachineFixture(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fresh-access" || r.Header.Get("x-api-key") != "" || r.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
			t.Errorf("OAuth discovery headers: %v", r.Header)
		}
		io.WriteString(w, `{"data":[{"id":"claude-current"}],"has_more":false}`)
	}))
	defer upstream.Close()
	_, err := f.store.CreateProvider(f.ctx, service.ProviderRecord{Key: "claude", Config: config.LLMConfig{Type: "anthropic", AuthType: "claude-code", APIKey: "expired-access", RefreshToken: "old-refresh", TokenExpiresAt: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), BaseURL: upstream.URL}})
	if err != nil {
		t.Fatal(err)
	}
	f.s.providerAuthClientFactory = func(string, bool) (*http.Client, error) {
		return &http.Client{Transport: providerAuthTestTransport(func(r *http.Request) (*http.Response, error) {
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("refresh_token") != "old-refresh" {
				t.Fatal("wrong refresh credential")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"access_token":"fresh-access","refresh_token":"fresh-refresh","expires_in":3600}`))}, nil
		})}, nil
	}
	body, _ := json.Marshal(discoverRequest{Key: "claude", Config: config.LLMConfig{Type: "anthropic", BaseURL: upstream.URL}})
	w := httptest.NewRecorder()
	f.s.DiscoverModelsAPI(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body))).WithContext(f.ctx))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "claude-current") {
		t.Fatalf("discovery: %d %s", w.Code, w.Body.String())
	}
	record, err := f.store.GetProvider(f.ctx, "claude")
	if err != nil || record.Config.RefreshToken != "fresh-refresh" {
		t.Fatalf("discovery did not persist rotation: %v", err)
	}
}
