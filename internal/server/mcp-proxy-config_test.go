package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestMCPUpstreamUsesExplicitProxy(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.IsAbs() || r.URL.Host != "mcp.invalid" || r.URL.Path != "/custom/mcp" {
			t.Errorf("unexpected proxy target: %s", r.URL)
		}
		if r.Header.Get("X-MCP") != "kept" {
			t.Error("static header lost")
		}
		var req service.MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		methods = append(methods, req.Method)
		mu.Unlock()
		if req.Method == "notifications/initialized" || req.Method == "notifications/cancelled" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := json.RawMessage(`{}`)
		switch req.Method {
		case "initialize":
			result = json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"proxied","version":"1"}}`)
		case "tools/list":
			result = json.RawMessage(`{"tools":[{"name":"ping","inputSchema":{"type":"object"}}]}`)
		case "tools/call":
			result = json.RawMessage(`{"content":[{"type":"text","text":"through proxy"}]}`)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(service.MCPResponse{Jsonrpc: "2.0", ID: req.ID, Result: result})
	}))
	defer proxy.Close()
	s := &Server{}
	client, err := s.newMCPClient(t.Context(), service.MCPUpstream{URL: "http://mcp.invalid/custom/mcp", Proxy: proxy.URL, Headers: map[string]string{"X-MCP": "kept"}})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := client.ListTools(t.Context())
	if err != nil || len(tools) != 1 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	text, err := client.CallTool(t.Context(), "ping", nil)
	if err != nil || text != "through proxy" {
		t.Fatalf("call: %q %v", text, err)
	}
	_ = client.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 5 {
		t.Fatalf("requests not all proxied: %v", methods)
	}
}

func TestNormalizeMCPProxy(t *testing.T) {
	for _, tt := range []struct {
		name      string
		upstream  service.MCPUpstream
		wantError bool
	}{
		{"static", service.MCPUpstream{URL: "https://mcp.example", Proxy: " http://proxy:8080 "}, false},
		{"oauth", service.MCPUpstream{URL: "https://mcp.example", Proxy: "socks5://proxy:1080", Auth: &service.MCPUpstreamAuth{Type: "oauth2"}}, false},
		{"invalid", service.MCPUpstream{URL: "https://mcp.example", Proxy: "ftp://proxy"}, true},
		{"stdio", service.MCPUpstream{Command: "npx", Proxy: "http://proxy"}, true},
		{"missing URL", service.MCPUpstream{Proxy: "http://proxy"}, true},
		{"TLS stdio", service.MCPUpstream{Command: "npx", InsecureSkipVerify: true}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstreams := []service.MCPUpstream{tt.upstream}
			if err := service.NormalizeMCPUpstreamAuth(upstreams); (err != nil) != tt.wantError {
				t.Fatalf("normalize: %v", err)
			}
		})
	}
}

func TestMCPUpstreamTLSVerificationOptIn(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req service.MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method != "initialize" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(service.MCPResponse{Jsonrpc: "2.0", ID: req.ID, Result: json.RawMessage(`{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"tls","version":"1"}}`)})
	}))
	defer srv.Close()
	s := &Server{}
	for _, insecure := range []bool{false, true} {
		client, err := s.newMCPClient(t.Context(), service.MCPUpstream{URL: srv.URL, InsecureSkipVerify: insecure})
		if (err == nil) != insecure {
			t.Fatalf("insecure=%v: %v", insecure, err)
		}
		if err == nil {
			_ = client.Close()
		}
	}
}
