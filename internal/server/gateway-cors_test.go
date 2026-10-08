package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayCORS(t *testing.T) {
	var reached bool
	handler := corsMiddleware("/at")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name         string
		method       string
		path         string
		headers      map[string]string
		wantStatus   int
		wantReached  bool
		wantOrigin   string
		wantHeaders  string
		wantPNA      string
		wantExposeOn bool
	}{
		{
			name:   "gateway preflight admits authorization",
			method: http.MethodOptions,
			path:   "/at/gateway/v1/chat/completions",
			headers: map[string]string{
				"Origin":                         "https://other-at.example.com",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "Authorization, Content-Type",
			},
			wantStatus:  http.StatusNoContent,
			wantOrigin:  "*",
			wantHeaders: "authorization, content-type",
		},
		{
			name:   "private network preflight",
			method: http.MethodOptions,
			path:   "/at/gateway/v1/models",
			headers: map[string]string{
				"Origin":                                 "https://at.example.com",
				"Access-Control-Request-Method":          "GET",
				"Access-Control-Request-Headers":         "authorization",
				"Access-Control-Request-Private-Network": "true",
			},
			wantStatus:  http.StatusNoContent,
			wantOrigin:  "*",
			wantHeaders: "authorization",
			wantPNA:     "true",
		},
		{
			name:         "gateway request exposes headers",
			method:       http.MethodPost,
			path:         "/at/gateway/v1/chat/completions",
			headers:      map[string]string{"Origin": "https://other-at.example.com"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
			wantOrigin:   "*",
			wantExposeOn: true,
		},
		{
			name:   "odd requested header is not reflected",
			method: http.MethodOptions,
			path:   "/at/gateway/v1/models",
			headers: map[string]string{
				"Origin":                         "https://x.example.com",
				"Access-Control-Request-Method":  "GET",
				"Access-Control-Request-Headers": "authorization, bad\x7fname",
			},
			wantStatus:  http.StatusNoContent,
			wantOrigin:  "*",
			wantHeaders: "authorization",
		},
		{
			name:   "gateway MCP keeps the default policy",
			method: http.MethodOptions,
			path:   "/at/gateway/v1/mcp/files",
			headers: map[string]string{
				"Origin":                         "https://evil.example.com",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "content-type",
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "gateway MCP preflight announcing a token is admitted",
			method: http.MethodOptions,
			path:   "/at/gateway/v1/mcp/files/mcp",
			headers: map[string]string{
				"Origin":                                 "https://other-at.example.com",
				"Access-Control-Request-Method":          "POST",
				"Access-Control-Request-Headers":         "authorization, content-type, mcp-session-id, mcp-protocol-version",
				"Access-Control-Request-Private-Network": "true",
			},
			wantStatus:  http.StatusNoContent,
			wantOrigin:  "*",
			wantHeaders: "authorization, content-type, mcp-session-id, mcp-protocol-version",
			wantPNA:     "true",
		},
		{
			name:   "gateway MCP preflight for GET keeps the default policy",
			method: http.MethodOptions,
			path:   "/at/gateway/v1/mcp/files",
			headers: map[string]string{
				"Origin":                         "https://other-at.example.com",
				"Access-Control-Request-Method":  "GET",
				"Access-Control-Request-Headers": "authorization",
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:   "gateway MCP websocket route keeps the default policy",
			method: http.MethodOptions,
			path:   "/at/gateway/v1/mcp/files/ws",
			headers: map[string]string{
				"Origin":                         "https://other-at.example.com",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "authorization",
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:         "gateway MCP request with a token is exposed",
			method:       http.MethodPost,
			path:         "/at/gateway/v1/mcp/files",
			headers:      map[string]string{"Origin": "https://other-at.example.com", "Authorization": "Bearer at_x"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
			wantOrigin:   "*",
			wantExposeOn: true,
		},
		{
			// Falls back to the default ada policy (which answers simple
			// requests with "*" but refuses the preflight a JSON-RPC POST
			// needs); the gateway MCP expose list is not added.
			name:        "gateway MCP request without a token keeps the default policy",
			method:      http.MethodPost,
			path:        "/at/gateway/v1/mcp/files",
			headers:     map[string]string{"Origin": "https://evil.example.com"},
			wantStatus:  http.StatusOK,
			wantReached: true,
			wantOrigin:  "*",
		},
		{
			name:   "application API keeps the default policy",
			method: http.MethodOptions,
			path:   "/at/api/v1/providers",
			headers: map[string]string{
				"Origin":                         "https://evil.example.com",
				"Access-Control-Request-Method":  "GET",
				"Access-Control-Request-Headers": "authorization",
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name:        "no origin passes through",
			method:      http.MethodGet,
			path:        "/at/gateway/v1/models",
			wantStatus:  http.StatusOK,
			wantReached: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reached = false
			r := httptest.NewRequest(tt.method, tt.path, nil)
			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if reached != tt.wantReached {
				t.Fatalf("reached = %v, want %v", reached, tt.wantReached)
			}
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != tt.wantOrigin {
				t.Fatalf("allow origin = %q, want %q", got, tt.wantOrigin)
			}
			if got := w.Header().Get("Access-Control-Allow-Headers"); got != tt.wantHeaders {
				t.Fatalf("allow headers = %q, want %q", got, tt.wantHeaders)
			}
			if got := w.Header().Get("Access-Control-Allow-Private-Network"); got != tt.wantPNA {
				t.Fatalf("private network = %q, want %q", got, tt.wantPNA)
			}
			if got := w.Header().Get("Access-Control-Allow-Credentials"); got != "" {
				t.Fatalf("credentials must never be allowed, got %q", got)
			}
			if exposed := w.Header().Get("Access-Control-Expose-Headers") != ""; exposed != tt.wantExposeOn {
				t.Fatalf("expose headers set = %v, want %v", exposed, tt.wantExposeOn)
			}
		})
	}
}
