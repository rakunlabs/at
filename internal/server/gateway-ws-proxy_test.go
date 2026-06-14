package server

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// startEchoWSUpstream starts an HTTP server that accepts a WebSocket-style
// upgrade, records selected request headers, and then echoes raw bytes.
// It does not implement real WS framing — the proxy tunnels opaque bytes
// after the 101, which is exactly what this verifies.
func startEchoWSUpstream(t *testing.T, gotHeaders chan<- http.Header) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		select {
		case gotHeaders <- r.Header.Clone():
		default:
		}

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()

		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: dummy\r\n\r\n")
		rw.Flush()

		// Echo loop: read a line, write it back prefixed.
		for {
			line, err := rw.ReadString('\n')
			if err != nil {
				return
			}
			rw.WriteString("echo:" + line)
			rw.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type wsUpstreamRequest struct {
	Header http.Header
	Query  url.Values
}

func startInspectWSUpstream(t *testing.T, got chan<- wsUpstreamRequest) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "expected upgrade", http.StatusBadRequest)
			return
		}
		select {
		case got <- wsUpstreamRequest{Header: r.Header.Clone(), Query: r.URL.Query()}:
		default:
		}

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()

		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\n" +
			"Connection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: dummy\r\n\r\n")
		rw.Flush()
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startProxyServer hosts the GatewayMCPWSHandler on a real listener (the
// reverse proxy needs a hijackable ResponseWriter, which httptest.Recorder
// does not provide).
func startProxyServer(t *testing.T, s *Server) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /gateway/v1/mcp/{name}/ws", s.GatewayMCPWSHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// dialWS performs a raw WebSocket-style upgrade against the proxy and
// returns the connection ready for tunneled I/O.
func dialWS(t *testing.T, proxyURL, path string) (net.Conn, *bufio.Reader) {
	return dialWSWithHeaders(t, proxyURL, path, nil)
}

func dialWSWithHeaders(t *testing.T, proxyURL, path string, headers map[string]string) (net.Conn, *bufio.Reader) {
	t.Helper()

	addr := strings.TrimPrefix(proxyURL, "http://")
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	fmt.Fprintf(conn, "GET %s HTTP/1.1\r\n"+
		"Host: %s\r\n"+
		"Upgrade: websocket\r\n"+
		"Connection: Upgrade\r\n"+
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"+
		"Sec-WebSocket-Version: 13\r\n", path, addr)
	for k, v := range headers {
		fmt.Fprintf(conn, "%s: %s\r\n", k, v)
	}
	fmt.Fprint(conn, "\r\n")

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %d, want 101", resp.StatusCode)
	}

	return conn, br
}

func TestGatewayMCPWSHandler_TunnelsFrames(t *testing.T) {
	gotHeaders := make(chan http.Header, 1)
	upstream := startEchoWSUpstream(t, gotHeaders)

	store := newFakeMCPServerStore()
	store.servers["wstool"] = &service.MCPServer{
		ID: "wstool", Name: "wstool", Public: true,
		Config: service.MCPServerConfig{
			WSUpstream: &service.WSUpstream{
				// ws:// scheme must be normalized to http for the dial.
				URL:     "ws://" + strings.TrimPrefix(upstream.URL, "http://"),
				Headers: map[string]string{"X-Upstream-Auth": "secret-1"},
			},
		},
	}
	s := &Server{mcpServerStore: store}
	proxy := startProxyServer(t, s)

	conn, br := dialWS(t, proxy.URL, "/gateway/v1/mcp/wstool/ws?token=ignored-on-public")

	// Bidirectional tunnel: send a line, expect the upstream echo.
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if line != "echo:hello\n" {
		t.Errorf("echo = %q, want %q", line, "echo:hello\n")
	}

	// Upstream must have received the injected header but NOT the AT token.
	select {
	case h := <-gotHeaders:
		if got := h.Get("X-Upstream-Auth"); got != "secret-1" {
			t.Errorf("X-Upstream-Auth = %q, want secret-1", got)
		}
		if got := h.Get("Authorization"); got != "" {
			t.Errorf("Authorization leaked upstream: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the request")
	}
}

func TestGatewayMCPWSHandler_PassesAllowedQueryAndHeaders(t *testing.T) {
	got := make(chan wsUpstreamRequest, 1)
	upstream := startInspectWSUpstream(t, got)

	store := newFakeMCPServerStore()
	store.servers["wstool"] = &service.MCPServer{
		ID: "wstool", Name: "wstool", Public: true,
		Config: service.MCPServerConfig{
			WSUpstream: &service.WSUpstream{
				URL:             "ws://" + strings.TrimPrefix(upstream.URL, "http://") + "?fixed=1&providerId=upstream-fixed",
				PassQueryParams: []string{"tabId", "providerId"},
				PassHeaders:     []string{"X-Client-Trace", "Authorization", "Cookie"},
			},
		},
	}
	s := &Server{mcpServerStore: store}
	proxy := startProxyServer(t, s)

	_, _ = dialWSWithHeaders(t, proxy.URL, "/gateway/v1/mcp/wstool/ws?token=at-token&tabId=42&providerId=browser&ignored=drop", map[string]string{
		"X-Client-Trace":   "trace-1",
		"X-Blocked-Header": "drop-me",
		"Authorization":    "Bearer client-at-token",
		"Cookie":           "session=client",
	})

	select {
	case req := <-got:
		if got := req.Query.Get("fixed"); got != "1" {
			t.Errorf("fixed query = %q, want 1", got)
		}
		if got := req.Query.Get("tabId"); got != "42" {
			t.Errorf("tabId query = %q, want 42", got)
		}
		// Upstream fixed params win over client params.
		if got := req.Query.Get("providerId"); got != "upstream-fixed" {
			t.Errorf("providerId query = %q, want upstream-fixed", got)
		}
		if got := req.Query.Get("ignored"); got != "" {
			t.Errorf("ignored query leaked upstream: %q", got)
		}
		if got := req.Query.Get("token"); got != "" {
			t.Errorf("AT token query leaked upstream: %q", got)
		}

		if got := req.Header.Get("X-Client-Trace"); got != "trace-1" {
			t.Errorf("X-Client-Trace = %q, want trace-1", got)
		}
		if got := req.Header.Get("X-Blocked-Header"); got != "" {
			t.Errorf("X-Blocked-Header leaked upstream: %q", got)
		}
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization leaked upstream: %q", got)
		}
		if got := req.Header.Get("Cookie"); got != "" {
			t.Errorf("Cookie leaked upstream: %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream never received the request")
	}
}

func TestGatewayMCPWSHandler_RequiresUpgrade(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["wstool"] = &service.MCPServer{
		ID: "wstool", Name: "wstool", Public: true,
		Config: service.MCPServerConfig{
			WSUpstream: &service.WSUpstream{URL: "ws://127.0.0.1:1/ws"},
		},
	}
	s := &Server{mcpServerStore: store}

	req := httptest.NewRequest(http.MethodGet, "/gateway/v1/mcp/wstool/ws", nil)
	req.SetPathValue("name", "wstool")
	rr := httptest.NewRecorder()
	s.GatewayMCPWSHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (non-upgrade request)", rr.Code)
	}
}

func TestGatewayMCPWSHandler_NoUpstreamConfigured(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["plain"] = &service.MCPServer{ID: "plain", Name: "plain", Public: true}
	s := &Server{mcpServerStore: store}

	req := httptest.NewRequest(http.MethodGet, "/gateway/v1/mcp/plain/ws", nil)
	req.SetPathValue("name", "plain")
	rr := httptest.NewRecorder()
	s.GatewayMCPWSHandler(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (no ws_upstream)", rr.Code)
	}
}

func TestGatewayMCPWSHandler_PrivateRequiresToken(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["private"] = &service.MCPServer{
		ID: "private", Name: "private",
		Config: service.MCPServerConfig{
			WSUpstream: &service.WSUpstream{URL: "ws://127.0.0.1:1/ws"},
		},
	}
	s := &Server{mcpServerStore: store}

	req := httptest.NewRequest(http.MethodGet, "/gateway/v1/mcp/private/ws", nil)
	req.SetPathValue("name", "private")
	rr := httptest.NewRecorder()
	s.GatewayMCPWSHandler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestParseWSUpstreamURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string // scheme://host
		wantErr bool
	}{
		{"ws scheme", "ws://example.com:9000/socket", "http://example.com:9000", false},
		{"wss scheme", "wss://example.com/socket", "https://example.com", false},
		{"http kept", "http://example.com/ws", "http://example.com", false},
		{"https kept", "https://example.com/ws", "https://example.com", false},
		{"bad scheme", "ftp://example.com", "", true},
		{"no host", "ws:///path", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := parseWSUpstreamURL(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", u)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := u.Scheme + "://" + u.Host; got != tt.want {
				t.Errorf("normalized = %q, want %q", got, tt.want)
			}
		})
	}
}
