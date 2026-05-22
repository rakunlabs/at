package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"

	"github.com/rakunlabs/at/internal/service"
)

type mockGatewayTokenStore struct {
	tokenHash string
	token     *service.APIToken
}

func (m *mockGatewayTokenStore) ListAPITokens(context.Context, *query.Query) (*service.ListResult[service.APIToken], error) {
	return nil, nil
}

func (m *mockGatewayTokenStore) GetAPITokenByHash(_ context.Context, hash string) (*service.APIToken, error) {
	if hash == m.tokenHash {
		return m.token, nil
	}
	return nil, nil
}

func (m *mockGatewayTokenStore) CreateAPIToken(context.Context, service.APIToken, string) (*service.APIToken, error) {
	return nil, nil
}

func (m *mockGatewayTokenStore) UpdateAPIToken(context.Context, string, service.APIToken) (*service.APIToken, error) {
	return nil, nil
}

func (m *mockGatewayTokenStore) DeleteAPIToken(context.Context, string) error { return nil }

func (m *mockGatewayTokenStore) UpdateLastUsed(context.Context, string) error { return nil }

type proxyCaptureProvider struct {
	called bool
	path   string
	body   string
	header http.Header
}

func (p *proxyCaptureProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, nil
}

func (p *proxyCaptureProvider) Proxy(w http.ResponseWriter, r *http.Request, path string) error {
	p.called = true
	p.path = path
	p.header = r.Header.Clone()
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		p.body = string(body)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func TestProxyRequestNativeGatewayUsesProviderAPIKeyHeader(t *testing.T) {
	provider := &proxyCaptureProvider{}
	s := newProxyTestServer("anthropic", provider, gatewayTestToken("test-token", service.APIToken{
		ID:                   "tok_1",
		AllowedProvidersMode: service.AccessModeNone,
		AllowedModelsMode:    service.AccessModeList,
		AllowedModels:        types.Slice[string]{"anthropic/claude-3-5-sonnet"},
	}))

	body := `{"model":"claude-3-5-sonnet","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/providers/anthropic/v1/messages?beta=true", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-token")
	req.SetPathValue("provider", "anthropic")
	req.SetPathValue("*", "v1/messages")
	rec := httptest.NewRecorder()

	s.ProxyRequest(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNoContent, rec.Body.String())
	}
	if !provider.called {
		t.Fatal("provider proxy was not called")
	}
	if provider.path != "/v1/messages" {
		t.Fatalf("proxy path = %q, want /v1/messages", provider.path)
	}
	if provider.body != body {
		t.Fatalf("proxy body = %q, want %q", provider.body, body)
	}
	if got := provider.header.Get("x-api-key"); got != "" {
		t.Fatalf("x-api-key leaked to provider proxy: %q", got)
	}
	if got := provider.header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization leaked to provider proxy: %q", got)
	}
}

func TestProxyRequestNativeGatewayDeniesDisallowedBodyModel(t *testing.T) {
	provider := &proxyCaptureProvider{}
	s := newProxyTestServer("anthropic", provider, gatewayTestToken("test-token", service.APIToken{
		ID:                   "tok_1",
		AllowedProvidersMode: service.AccessModeNone,
		AllowedModelsMode:    service.AccessModeList,
		AllowedModels:        types.Slice[string]{"anthropic/claude-allowed"},
	}))

	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/providers/anthropic/v1/messages", strings.NewReader(`{"model":"claude-denied"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "test-token")
	req.SetPathValue("provider", "anthropic")
	req.SetPathValue("*", "v1/messages")
	rec := httptest.NewRecorder()

	s.ProxyRequest(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if provider.called {
		t.Fatal("provider proxy was called for disallowed model")
	}
}

func TestProxyRequestNativeGatewayRejectsUnknownConfiguredModel(t *testing.T) {
	provider := &proxyCaptureProvider{}
	s := newProxyTestServer("anthropic", provider, gatewayTestToken("test-token", service.APIToken{
		ID:                   "tok_1",
		AllowedProvidersMode: service.AccessModeList,
		AllowedProviders:     types.Slice[string]{"anthropic"},
	}))

	req := httptest.NewRequest(http.MethodPost, "/gateway/v1/providers/anthropic/v1/messages", strings.NewReader(`{"model":"claude-missing"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-token")
	req.SetPathValue("provider", "anthropic")
	req.SetPathValue("*", "v1/messages")
	rec := httptest.NewRecorder()

	s.ProxyRequest(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if provider.called {
		t.Fatal("provider proxy was called for unknown configured model")
	}
}

func newProxyTestServer(providerKey string, provider service.LLMProvider, tokenStore service.APITokenStorer) *Server {
	return &Server{
		providers: map[string]ProviderInfo{
			providerKey: {
				provider:     provider,
				providerType: "anthropic",
				defaultModel: "claude-3-5-sonnet",
				models:       []string{"claude-3-5-sonnet", "claude-allowed"},
			},
		},
		tokenStore: tokenStore,
	}
}

func gatewayTestToken(raw string, token service.APIToken) service.APITokenStorer {
	hash := sha256.Sum256([]byte(raw))
	return &mockGatewayTokenStore{
		tokenHash: hex.EncodeToString(hash[:]),
		token:     &token,
	}
}
