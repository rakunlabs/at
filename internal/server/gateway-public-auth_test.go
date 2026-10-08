package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestGatewayMCPHandler_PublicAllowsMissingToken(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["public"] = &service.MCPServer{ID: "public", Name: "public", Public: true}
	s := &Server{mcpServerStore: store}

	rr := httptest.NewRecorder()
	s.GatewayMCPHandler(rr, newMCPInitRequest("/gateway/v1/mcp/public", "public"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestGatewayMCPHandler_PrivateRequiresToken(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["private"] = &service.MCPServer{ID: "private", Name: "private"}
	s := &Server{mcpServerStore: store}

	rr := httptest.NewRecorder()
	s.GatewayMCPHandler(rr, newMCPInitRequest("/gateway/v1/mcp/private", "private"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

// A cross-origin request with a rejected credential must not fall back to
// public admission: gateway MCP CORS is opened for any request presenting a
// credential header, so the fallback would let any website read a public
// server with a junk token. Same-origin/non-browser callers keep the fallback.
func TestGatewayMCPHandler_BrowserRejectedTokenNoPublicFallback(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["public"] = &service.MCPServer{ID: "public", Name: "public", Public: true}
	s := &Server{mcpServerStore: store}

	rr := httptest.NewRecorder()
	req := newMCPInitRequest("/gateway/v1/mcp/public", "public")
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Authorization", "Bearer junk")
	s.GatewayMCPHandler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("browser junk token: status = %d, want %d; body: %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = newMCPInitRequest("/gateway/v1/mcp/public", "public")
	req.Header.Set("Authorization", "Bearer junk")
	s.GatewayMCPHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("non-browser junk token: status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func newMCPInitRequest(path, name string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.SetPathValue("name", name)
	return req
}
