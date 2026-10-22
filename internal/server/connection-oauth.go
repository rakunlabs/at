package server

import (
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Connection OAuth admission ───
//
// The connector OAuth endpoints (/oauth/*) predate workspaces and, without a
// connection, write the installation's global <slug>_refresh_token variable.
// That legacy form stays installation administration. Authorizing a named
// connection is different: the store already decides who may change that row
// (its owner for a personal connection, connections.write + credentials.manage
// for a workspace one), so those requests are admitted per workspace on
// connections.use and the store makes the final decision on save.
//
// Popups and provider redirects are top-level navigations that cannot send the
// workspace header, so a start carries ?workspace_id= and the provider return
// carries the workspace as a nonsecret "<workspace>." prefix of the state. The
// state entry itself stays bound to the initiating session.

func connectionOAuthReturnPath(cookiePath, path string) bool {
	return path == cookiePath+"api/v1/oauth/callback" || path == cookiePath+"api/v1/oauth/code-display"
}

func connectionOAuthStartPath(cookiePath, path string) bool {
	return path == cookiePath+"api/v1/oauth/start" || path == cookiePath+"api/v1/oauth/manual-url"
}

// connectionOAuthPolicy admits connection-scoped OAuth requests in the
// selected workspace. Requests without a connection (or with an unscoped
// state) return nil and keep installation-only admission.
func connectionOAuthPolicy(r *http.Request, path string) *BusinessRoutePolicy {
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && (path == "/oauth/start" || path == "/oauth/manual-url"),
		r.Method == http.MethodPost && path == "/oauth/exchange":
		if strings.TrimSpace(q.Get("connection_id")) == "" {
			return nil
		}
	case r.Method == http.MethodGet && (path == "/oauth/callback" || path == "/oauth/code-display"):
		if mcpOAuthStateWorkspace(q.Get("state")) == "" {
			return nil
		}
	default:
		return nil
	}
	return &BusinessRoutePolicy{Method: r.Method, Pattern: path, Capability: "connections.use"}
}

// connectionOAuthScoped reports whether the request was admitted per
// workspace (as opposed to the legacy installation-wide administration path).
func connectionOAuthScoped(r *http.Request) (service.AccessPrincipal, bool) {
	p, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok || p.WorkspaceID == "" || service.LegacyWorkspaceAccessFromContext(r.Context()) {
		return service.AccessPrincipal{}, false
	}
	return p, true
}
