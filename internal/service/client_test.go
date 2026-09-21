package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDecodeMCPResponseSSE(t *testing.T) {
	tests := []struct {
		name    string
		stream  string
		wantID  int
		wantErr bool
		want    string // expected result JSON (raw)
	}{
		{
			name:   "single response event",
			stream: "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":3,\"result\":{\"ok\":true}}\n\n",
			wantID: 3,
			want:   `{"ok":true}`,
		},
		{
			name: "skips notifications before response",
			stream: "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\"}\n\n" +
				"data: {\"jsonrpc\":\"2.0\",\"id\":7,\"result\":{\"done\":1}}\n\n",
			wantID: 7,
			want:   `{"done":1}`,
		},
		{
			name:   "multiline data",
			stream: "data: {\"jsonrpc\":\"2.0\",\ndata: \"id\":1,\"result\":{}}\n\n",
			wantID: 1,
			want:   `{}`,
		},
		{
			name:   "trailing event without blank line",
			stream: "data: {\"jsonrpc\":\"2.0\",\"id\":5,\"result\":{\"x\":2}}",
			wantID: 5,
			want:   `{"x":2}`,
		},
		{
			name:    "no matching response",
			stream:  "data: {\"jsonrpc\":\"2.0\",\"id\":99,\"result\":{}}\n\n",
			wantID:  1,
			wantErr: true,
		},
		{
			name:    "empty stream",
			stream:  "",
			wantID:  1,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := decodeMCPResponseSSE(strings.NewReader(tt.stream), tt.wantID)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got response %+v", resp)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.ID != tt.wantID {
				t.Errorf("response ID = %d, want %d", resp.ID, tt.wantID)
			}
			if string(resp.Result) != tt.want {
				t.Errorf("result = %s, want %s", resp.Result, tt.want)
			}
		})
	}
}

// TestHTTPMCPClient_StreamableHTTP verifies the client against a server
// that answers with SSE bodies, uses the spec Mcp-Session-Id header, and
// returns 202 for notifications.
func TestHTTPMCPClient_StreamableHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Errorf("request path = %q, want /mcp", r.URL.Path)
		}
		if r.URL.Query().Get("token") != "abc" {
			t.Errorf("token query = %q, want abc", r.URL.Query().Get("token"))
		}
		if accept := r.Header.Get("Accept"); !strings.Contains(accept, "text/event-stream") {
			t.Errorf("Accept header = %q, want text/event-stream included", accept)
		}

		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "sess-123")
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			result := `{"protocolVersion":"2025-03-26","serverInfo":{"name":"test","version":"1.0"}}`
			w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":" +
				itoa(req.ID) + ",\"result\":" + result + "}\n\n"))
		case "notifications/initialized", "notifications/cancelled":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := r.Header.Get("Mcp-Session-Id"); got != "sess-123" {
				t.Errorf("Mcp-Session-Id = %q, want sess-123", got)
			}
			if got := r.Header.Get("MCP-Protocol-Version"); got != "2025-03-26" {
				t.Errorf("MCP-Protocol-Version = %q, want 2025-03-26", got)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(req.ID) +
				`,"result":{"tools":[{"name":"t1","description":"d","inputSchema":{}}]}}`))
		default:
			t.Errorf("unexpected method %q", req.Method)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	// The client accepts a full Streamable HTTP endpoint URL and must not turn
	// /mcp into /mcp/mcp. The query is preserved for token-based upstreams.
	client, err := NewHTTPMCPClient(context.Background(), strings.TrimSuffix(srv.URL, "/")+"/mcp?token=abc")
	if err != nil {
		t.Fatalf("NewHTTPMCPClient: %v", err)
	}
	defer client.Close()

	if client.sessionID != "sess-123" {
		t.Errorf("sessionID = %q, want sess-123", client.sessionID)
	}
	if client.protocolVersion != "2025-03-26" {
		t.Errorf("protocolVersion = %q, want 2025-03-26", client.protocolVersion)
	}

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "t1" {
		t.Errorf("tools = %+v, want one tool named t1", tools)
	}
}

// The configured URL is dialled verbatim. AT used to append /mcp to anything
// whose path did not already end in it, which made an endpoint like /mcp/api
// or /sse unreachable and said nothing about why.
func TestMCPEndpointURLIsVerbatim(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"host only", "http://127.0.0.1:8787", "http://127.0.0.1:8787"},
		{"host slash", "http://127.0.0.1:8787/", "http://127.0.0.1:8787/"},
		{"full endpoint", "http://127.0.0.1:8787/mcp", "http://127.0.0.1:8787/mcp"},
		{"trailing slash kept", "http://127.0.0.1:8787/mcp/", "http://127.0.0.1:8787/mcp/"},
		{"query preserved", "http://127.0.0.1:8787/mcp?token=abc", "http://127.0.0.1:8787/mcp?token=abc"},
		{"nested path not rewritten", "https://example.com/mcp/api", "https://example.com/mcp/api"},
		{"sse path not rewritten", "https://example.com/sse", "https://example.com/sse"},
		{"base path not rewritten", "https://example.com/bridge", "https://example.com/bridge"},
		{"whitespace trimmed", "  https://example.com/mcp/api\n", "https://example.com/mcp/api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mcpEndpointURL(tt.in); got != tt.want {
				t.Errorf("mcpEndpointURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// A path the normalizer would have rewritten must be the path that is
// requested, end to end through the client.
func TestHTTPMCPClientDialsConfiguredPath(t *testing.T) {
	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":"2025-03-26","serverInfo":{"name":"t","version":"1"}}}`))
		case "tools/list":
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"tools":[]}}`))
		default:
			// notifications/initialized, notifications/cancelled, ...
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer srv.Close()

	client, err := NewHTTPMCPClient(context.Background(), strings.TrimSuffix(srv.URL, "/")+"/mcp/api")
	if err != nil {
		t.Fatalf("NewHTTPMCPClient: %v", err)
	}
	defer client.Close()

	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, p := range gotPaths {
		if p != "/mcp/api" {
			t.Fatalf("request path = %q, want /mcp/api", p)
		}
	}
	if len(gotPaths) == 0 {
		t.Fatal("no requests recorded")
	}
}

func TestHTTPMCPClientCloseIsBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":"2025-03-26","serverInfo":{"name":"test","version":"1"}}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "notifications/cancelled":
			<-r.Context().Done()
		default:
			t.Errorf("unexpected method %q", req.Method)
		}
	}))
	defer srv.Close()

	client, err := NewHTTPMCPClient(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("NewHTTPMCPClient: %v", err)
	}
	started := time.Now()
	err = client.Close()
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 2*httpMCPClientCloseTimeout {
		t.Fatalf("Close took %v, want bounded cleanup", elapsed)
	}
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
