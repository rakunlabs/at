package server

import (
	"net/http"
	"strings"

	mcors "github.com/rakunlabs/ada/middleware/cors"
)

// Gateway CORS lets a page on another origin — typically another AT
// installation using this one as a Chats local provider — call the
// OpenAI-compatible gateway straight from the browser.
//
// It is safe to open to every origin because gateway admission is an API token
// carried in a header, never ambient browser state: credentials are not
// allowed, the gateway group strips Cookie, and a page that does not already
// hold a token can do nothing it could not do with curl. The rest of the
// application authenticates with session cookies and keeps the default policy.
//
// The ada middleware cannot express this: an Access-Control-Allow-Headers of
// "*" does not cover Authorization in browsers, and it never answers Chrome's
// Private Network Access preflight.

var gatewayCORSExposeHeaders = strings.Join([]string{
	"x-at-model-used",
	"x-at-routing-profile",
	"x-at-mock-response",
	"x-at-idempotent-replay",
	"x-at-response-cost-cents",
	"x-request-id",
	"retry-after",
}, ", ")

const (
	gatewayCORSAllowMethods = "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS"
	gatewayCORSMaxAge       = "600"
)

// corsMiddleware applies the gateway policy under <base>/gateway/v1/ and the
// default ada policy everywhere else.
func corsMiddleware(basePath string) func(http.Handler) http.Handler {
	defaultCORS := mcors.Middleware()
	gatewayPrefix := basePath + "/gateway/v1/"

	return func(next http.Handler) http.Handler {
		fallback := defaultCORS(next)
		gateway := gatewayCORS(next)
		gatewayMCP := gatewayMCPCORS(next, fallback)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gatewayCORSPath(r.URL.Path, gatewayPrefix) || mcpAuthCORSPath(r.URL.Path, basePath) {
				gateway.ServeHTTP(w, r)

				return
			}
			if gatewayMCPCORSPath(r.URL.Path, gatewayPrefix) {
				gatewayMCP.ServeHTTP(w, r)

				return
			}
			fallback.ServeHTTP(w, r)
		})
	}
}

// gatewayMCPCORSPath reports whether the path is a gateway MCP JSON-RPC
// endpoint: POST mcp/{name} or mcp/{name}/mcp. The SSE (GET) and WebSocket
// routes are not included; a browser Chats client only POSTs.
func gatewayMCPCORSPath(path, prefix string) bool {
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return false
	}
	name, ok := strings.CutPrefix(rest, "mcp/")
	if !ok {
		return false
	}
	name = strings.TrimSuffix(name, "/mcp")

	return name != "" && !strings.Contains(name, "/")
}

// gatewayMCPCORS opens the gateway MCP endpoints to cross-origin browser calls
// that carry a gateway token, so another AT installation can use one of this
// installation's MCP servers as a Chats local MCP server.
//
// The token is the whole condition. A public MCP server admits requests
// without one, so opening these paths unconditionally would let any website a
// person visits drive an MCP server on that person's network. A preflight that
// does not announce a credential header, and an actual request that carries
// none, therefore get the default policy, exactly as before. The handler
// additionally refuses to fall back to public admission for a cross-origin
// request whose credential was rejected (authorizeGatewayMCPServer), so a junk
// header cannot buy CORS access to a public server.
func gatewayMCPCORS(next, fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)

			return
		}

		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		if r.Method == http.MethodOptions && requestedMethod != "" {
			requested := gatewayCORSRequestedHeaders(r)
			if !strings.EqualFold(requestedMethod, http.MethodPost) || !gatewayCORSAnnouncesCredential(requested) {
				fallback.ServeHTTP(w, r)

				return
			}

			h := w.Header()
			h.Add("Vary", "Origin")
			h.Add("Vary", "Access-Control-Request-Method")
			h.Add("Vary", "Access-Control-Request-Headers")
			h.Set("Access-Control-Allow-Origin", "*")
			h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			h.Set("Access-Control-Allow-Headers", requested)
			if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
				h.Set("Access-Control-Allow-Private-Network", "true")
			}
			h.Set("Access-Control-Max-Age", gatewayCORSMaxAge)
			w.WriteHeader(http.StatusNoContent)

			return
		}

		if !gatewayMCPCredentialPresented(r) {
			fallback.ServeHTTP(w, r)

			return
		}

		h := w.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Expose-Headers", "Mcp-Session-Id, x-request-id")
		next.ServeHTTP(w, r)
	})
}

// gatewayCORSAnnouncesCredential reports whether a preflight's requested
// header list names a gateway credential header.
func gatewayCORSAnnouncesCredential(requested string) bool {
	for _, name := range strings.Split(requested, ",") {
		switch strings.TrimSpace(name) {
		case "authorization", "x-api-key":
			return true
		}
	}

	return false
}

// gatewayMCPCredentialPresented reports whether the request carries a gateway
// credential header, valid or not. Validity is the handler's decision.
func gatewayMCPCredentialPresented(r *http.Request) bool {
	if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.TrimSpace(bearer) != "" {
		return true
	}

	return strings.TrimSpace(r.Header.Get("x-api-key")) != ""
}

// gatewayCORSPath reports whether the path is a token-authenticated gateway
// API. The MCP endpoints are excluded because a server marked public admits
// requests without a token, and opening them would let any website a person
// visits drive an MCP server on that person's network. Plugin downloads have
// no browser use.
func gatewayCORSPath(path, prefix string) bool {
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return false
	}

	return !strings.HasPrefix(rest, "mcp/") && rest != "mcp" && !strings.HasPrefix(rest, "claude-code/")
}

func gatewayCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(w, r)

			return
		}

		h := w.Header()
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Origin", "*")

		requestedMethod := r.Header.Get("Access-Control-Request-Method")
		if r.Method != http.MethodOptions || requestedMethod == "" {
			h.Set("Access-Control-Expose-Headers", gatewayCORSExposeHeaders)
			next.ServeHTTP(w, r)

			return
		}

		h.Add("Vary", "Access-Control-Request-Method")
		h.Add("Vary", "Access-Control-Request-Headers")
		h.Set("Access-Control-Allow-Methods", gatewayCORSAllowMethods)
		// Reflecting is safe without credentials, and it is the only way to
		// admit Authorization: the "*" wildcard never covers it.
		if requested := gatewayCORSRequestedHeaders(r); requested != "" {
			h.Set("Access-Control-Allow-Headers", requested)
		}
		if strings.EqualFold(r.Header.Get("Access-Control-Request-Private-Network"), "true") {
			h.Set("Access-Control-Allow-Private-Network", "true")
		}
		h.Set("Access-Control-Max-Age", gatewayCORSMaxAge)
		w.WriteHeader(http.StatusNoContent)
	})
}

// gatewayCORSRequestedHeaders returns the requested header names, dropping
// anything that is not a plain header token so nothing odd is reflected.
func gatewayCORSRequestedHeaders(r *http.Request) string {
	var out []string
	for _, value := range r.Header.Values("Access-Control-Request-Headers") {
		for _, name := range strings.Split(value, ",") {
			name = strings.ToLower(strings.TrimSpace(name))
			if name != "" && validHeaderToken(name) && len(out) < 64 {
				out = append(out, name)
			}
		}
	}

	return strings.Join(out, ", ")
}

func validHeaderToken(s string) bool {
	if len(s) > 128 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			continue
		}

		return false
	}

	return true
}

// mcpAuthCORSPath reports the authorization-server endpoints MCP clients call
// (discovery, registration, token, revocation). Like the gateway they take no
// ambient credentials, so browser-based MCP clients may read them. The
// authorize endpoint is a navigation and needs none.
func mcpAuthCORSPath(path, basePath string) bool {
	if strings.HasPrefix(path, "/.well-known/oauth-protected-resource") || strings.HasPrefix(path, "/.well-known/oauth-authorization-server") {
		return true
	}
	rest, ok := strings.CutPrefix(path, basePath+"/oauth/mcp/")
	return ok && (rest == "register" || rest == "token" || rest == "revoke")
}
