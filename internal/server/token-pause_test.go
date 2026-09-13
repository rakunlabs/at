package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestPausedTokenRejectsGatewayRequests(t *testing.T) {
	for _, header := range []string{"Authorization", "x-api-key", "x-goog-api-key", "api-key"} {
		t.Run(header, func(t *testing.T) {
			provider := &proxyCaptureProvider{}
			store := gatewayTestToken("secret", service.APIToken{ID: "paused-token", Paused: true})
			s := newProxyTestServer("upstream", provider, store)
			r := httptest.NewRequest(http.MethodPost, "/gateway/v1/providers/upstream/v1/messages", strings.NewReader(`{"model":"test"}`))
			r.SetPathValue("provider", "upstream")
			r.SetPathValue("*", "v1/messages")
			value := "secret"
			if header == "Authorization" {
				value = "Bearer " + value
			}
			r.Header.Set(header, value)
			w := httptest.NewRecorder()
			s.ProxyRequest(w, r)
			if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "token is paused") {
				t.Fatalf("paused response: %d %s", w.Code, w.Body.String())
			}
			if provider.called {
				t.Fatal("paused token reached upstream")
			}
			if _, ok := s.tokenLastUsed.Load("paused-token"); ok {
				t.Fatal("paused token counted as used")
			}
			store.(*mockGatewayTokenStore).token.Paused = false
			if auth, reason := s.authenticateRequest(r); auth == nil || reason != "" {
				t.Fatalf("resumed token rejected: %s", reason)
			}
		})
	}
}

func TestPausedTokenCannotFallBackToPublicMCP(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["public"] = &service.MCPServer{ID: "public", Name: "public", Public: true}
	s := &Server{
		mcpServerStore: store,
		tokenStore:     gatewayTestToken("secret", service.APIToken{ID: "paused", Paused: true}),
	}
	r := newMCPInitRequest("/gateway/v1/mcp/public", "public")
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	s.GatewayMCPHandler(w, r)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "token is paused") {
		t.Fatalf("paused MCP response: %d %s", w.Code, w.Body.String())
	}
}
