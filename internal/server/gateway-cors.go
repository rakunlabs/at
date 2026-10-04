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

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gatewayCORSPath(r.URL.Path, gatewayPrefix) {
				gateway.ServeHTTP(w, r)

				return
			}
			fallback.ServeHTTP(w, r)
		})
	}
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
