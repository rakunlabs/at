package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
)

// GatewayMCPWSHandler handles GET /gateway/v1/mcp/{name}/ws.
//
// It is a raw WebSocket passthrough: when the named MCP server config has a
// ws_upstream, the client's upgrade request is reverse-proxied to the
// upstream URL and frames are copied bidirectionally without inspection.
// AT acts as the auth + secret-injection layer in front of the upstream
// socket (the same Bearer-token / public-flag rules as the MCP endpoint).
//
// Browser WebSocket clients cannot set an Authorization header, so a
// `?token=<api-token>` query parameter is accepted as a fallback; it is
// stripped before the request is forwarded upstream.
func (s *Server) GatewayMCPWSHandler(w http.ResponseWriter, r *http.Request) {
	if s.mcpServerStore == nil {
		httpResponse(w, "mcp server store not configured", http.StatusServiceUnavailable)
		return
	}

	// Promote ?token= to a Bearer header for the shared gateway auth path.
	if r.Header.Get("Authorization") == "" {
		if tok := r.URL.Query().Get("token"); tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
	}

	mcpSrv, ok := s.authorizeGatewayMCPServer(w, r)
	if !ok {
		return
	}

	cfg := mcpSrv.Config.WSUpstream
	if cfg == nil || cfg.URL == "" {
		httpResponse(w, fmt.Sprintf("MCP server %q has no ws_upstream configured", mcpSrv.Name), http.StatusNotFound)
		return
	}

	if !isWebSocketUpgrade(r) {
		httpResponse(w, "expected a WebSocket upgrade request", http.StatusBadRequest)
		return
	}

	target, err := parseWSUpstreamURL(s.resolveVarRefs(cfg.URL))
	if err != nil {
		slog.Error("ws proxy: invalid upstream URL", "server", mcpSrv.Name, "error", err)
		httpResponse(w, "invalid ws_upstream URL", http.StatusBadGateway)
		return
	}

	// Resolve configured headers once per connection.
	headers := make(map[string]string, len(cfg.Headers))
	for k, v := range cfg.Headers {
		headers[k] = s.resolveVarRefs(v)
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = target.Scheme
			pr.Out.URL.Host = target.Host
			pr.Out.URL.Path = target.Path
			pr.Out.Host = target.Host

			// Merge query params: upstream URL's fixed params win. When
			// pass_query_params is empty, preserve the previous behaviour and
			// carry all client params except AT's auth token. When configured,
			// only explicitly listed names are forwarded.
			q := target.Query()
			for k, vals := range pr.In.URL.Query() {
				if !shouldPassWSQueryParam(k, cfg.PassQueryParams) {
					continue
				}
				if _, exists := q[k]; exists {
					continue
				}
				for _, v := range vals {
					q.Add(k, v)
				}
			}
			pr.Out.URL.RawQuery = q.Encode()

			if len(cfg.PassHeaders) > 0 {
				for name := range pr.Out.Header {
					if !shouldKeepWSClientHeader(name, cfg.PassHeaders) {
						pr.Out.Header.Del(name)
					}
				}
			}

			// AT credentials must not leak to the upstream.
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Cookie")
			for _, name := range cfg.PassHeaders {
				if !canPassWSHeader(name) {
					continue
				}
				if vals, ok := pr.In.Header[http.CanonicalHeaderKey(name)]; ok {
					pr.Out.Header[http.CanonicalHeaderKey(name)] = slices.Clone(vals)
				}
			}

			for k, v := range headers {
				pr.Out.Header.Set(k, v)
			}
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("ws proxy: upstream connection failed", "server", mcpSrv.Name, "upstream", target.Host, "error", err)
			httpResponse(w, "upstream WebSocket unavailable", http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

// shouldPassWSQueryParam applies ws_upstream.pass_query_params. token is
// reserved for AT auth and is never forwarded from the client query string.
func shouldPassWSQueryParam(name string, allow []string) bool {
	if name == "token" {
		return false
	}
	if len(allow) == 0 {
		return true
	}
	return slices.Contains(allow, name)
}

// canPassWSHeader prevents client credentials intended for AT from leaking to
// the upstream. Upstream auth should be configured with ws_upstream.headers.
func canPassWSHeader(name string) bool {
	switch http.CanonicalHeaderKey(strings.TrimSpace(name)) {
	case "", "Authorization", "Cookie":
		return false
	default:
		return true
	}
}

func shouldKeepWSClientHeader(name string, allow []string) bool {
	canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
	if isWebSocketHandshakeHeader(canonical) {
		return true
	}
	for _, item := range allow {
		if http.CanonicalHeaderKey(strings.TrimSpace(item)) == canonical {
			return true
		}
	}
	return false
}

func isWebSocketHandshakeHeader(name string) bool {
	switch http.CanonicalHeaderKey(strings.TrimSpace(name)) {
	case "Connection", "Upgrade", "Sec-Websocket-Key", "Sec-Websocket-Version", "Sec-Websocket-Protocol", "Sec-Websocket-Extensions":
		return true
	default:
		return false
	}
}

// parseWSUpstreamURL normalizes a configured upstream URL. ws/wss schemes
// are mapped to http/https because httputil.ReverseProxy dials the upgrade
// request over HTTP.
func parseWSUpstreamURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse upstream URL: %w", err)
	}

	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
		// already dialable
	default:
		return nil, fmt.Errorf("unsupported upstream scheme %q (want ws, wss, http or https)", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("upstream URL has no host")
	}

	return u, nil
}

// isWebSocketUpgrade reports whether the request asks for a WebSocket
// upgrade (Connection: Upgrade + Upgrade: websocket).
func isWebSocketUpgrade(r *http.Request) bool {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, part := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
			return true
		}
	}
	return false
}
