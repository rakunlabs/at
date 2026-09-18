package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/issuer"

	"github.com/rakunlabs/at/internal/service"
)

// Scope labels let a workspace-selecting client tell installation-wide data
// apart from selected-workspace data. Refusing the request instead would break
// every page whose store path is not yet partitioned, so the response is
// labelled rather than rejected and the stale selection header is stripped so
// no downstream handler can act on it.
const (
	workspaceScopeHeader       = "X-AT-Scope"
	workspaceScopeInstallation = "installation"
	workspaceScopeWorkspace    = "workspace"
)

// These routes keep the native admin gate and answer with installation-wide
// data until the whole underlying store/execution path is converted.
func (s *Server) requireUnscopedWorkspacePlatform() func(http.Handler) http.Handler {
	return s.requireWorkspacePlatform(true)
}

// requireWorkspacePlatform gates an installation-wide route. Most such routes
// are administration and demand the admin role; a few are shared reference data
// that every signed-in member needs, and those pass admin=false.
func (s *Server) requireWorkspacePlatform(admin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			a := nativeRuntimeFromRequest(r, s.nativeAuth)
			if a == nil {
				nativeError(w, 503, "native workspace authentication unavailable")
				return
			}
			a.require(admin)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(workspaceScopeHeader, workspaceScopeInstallation)
				r.Header.Del("X-AT-Workspace-ID")
				next.ServeHTTP(w, r.WithContext(service.WithLegacyWorkspaceAccess(r.Context())))
			})).ServeHTTP(w, r)
		})
	}
}

// workspaceAuthentication permits both native transports. Mobile access tokens
// still pass mobileBearer's transport validation; cookies still require Origin.
// This middleware is attached only to explicitly registered workspace endpoints.
func (s *Server) workspaceAuthentication(selected bool, capability string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.withRuntimeAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			a := nativeRuntimeFromRequest(r, s.nativeAuth)
			if a == nil {
				nativeError(w, 503, "native workspace authentication unavailable")
				return
			}
			var pair *issuer.Pair
			var err error
			mobile := len(r.Header.Values("Authorization")) > 0
			if mobile {
				pair, err = a.mobileBearer(r)
			} else {
				pair, err = a.currentSession(r)
			}
			if err != nil {
				if errors.Is(err, issuer.ErrNotFound) {
					nativeError(w, 401, "authentication required")
				} else {
					nativeError(w, 503, "authentication unavailable")
				}
				return
			}
			if !mobile && !a.sameOrigin(w, r) {
				return
			}
			if pair == nil || pair.Identity == nil || pair.SessionID == "" {
				nativeError(w, 401, "authentication required")
				return
			}
			ctx := context.WithValue(r.Context(), nativeSessionContextKey{}, pair)
			ctx = context.WithValue(ctx, nativeMobileContextKey{}, mobile)
			ctx = identity.WithContext(ctx, pair.Identity)
			p := service.AccessPrincipal{UserID: pair.Identity.Subject, SessionID: pair.SessionID, PlatformAdmin: pair.Identity.HasRole("admin")}
			if selected {
				values := r.Header.Values("X-AT-Workspace-ID")
				// Native media elements and download/new-tab navigations cannot set
				// custom headers. A nonsecret, explicit workspace selector is allowed
				// only for file reads; all live admission below remains mandatory.
				if (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.URL.Path == a.session.Cookie.Path+"api/v1/files/serve" {
					q, parseErr := url.ParseQuery(r.URL.RawQuery)
					if parseErr != nil {
						nativeError(w, 400, "invalid file query")
						return
					}
					if ids, exists := q["workspace_id"]; exists {
						if len(ids) != 1 || strings.TrimSpace(ids[0]) == "" || len(values) > 1 || (len(values) == 1 && values[0] != ids[0]) {
							nativeError(w, 400, "file workspace selection is ambiguous")
							return
						}
						values = ids
					}
				}
				if len(values) != 1 || strings.TrimSpace(values[0]) == "" || strings.Contains(values[0], ",") {
					nativeError(w, 400, "X-AT-Workspace-ID is required")
					return
				}
				store, ok := s.store.(service.WorkspaceStorer)
				if !ok {
					nativeError(w, 503, "workspace store unavailable")
					return
				}
				p, _, err = store.ResolveWorkspaceAccess(ctx, values[0], p.UserID, p.SessionID)
				if err != nil {
					workspaceError(w, err)
					return
				}
				// Single-workspace mode. Checked only for a non-default
				// selection so the ordinary path pays nothing, and after
				// admission so a caller cannot probe workspaces it has no
				// access to.
				if p.WorkspaceID != service.DefaultWorkspaceID {
					pinned, ferr := s.workspacesPinnedToDefault(ctx)
					if ferr != nil {
						nativeError(w, 500, "failed to check workspace feature")
						return
					}
					if pinned {
						nativeError(w, 403, "additional workspaces are disabled; select the default workspace")
						return
					}
				}
				if id := r.PathValue("workspace"); id != "" && id != p.WorkspaceID {
					nativeError(w, 404, "workspace resource not found")
					return
				}
				if capability != "" && !p.Allows(capability, service.AccessResource{WorkspaceID: p.WorkspaceID}) {
					nativeError(w, 403, "workspace capability required")
					return
				}
				w.Header().Set(workspaceScopeHeader, workspaceScopeWorkspace)
			}
			next.ServeHTTP(w, r.WithContext(service.WithAccessPrincipal(ctx, p)))
		}))
	}
}

// withRuntimeAuth installs the immutable coordinator for the current policy
// version. Routes registered outside the /auth/* tree need it explicitly;
// without it they would fall back to the boot-time singleton, which no longer
// exists once authentication settings live in the database.
func (s *Server) withRuntimeAuth(next http.Handler) http.Handler {
	if s.authSettings == nil {
		return next
	}
	return s.authSettings.withRuntime(next)
}

// revalidateAccessPrincipal is the runtime admission callback. A missing principal
// or session does not acquire installation authority. Workers must additionally
// retain their persisted execution ceiling when applying the returned live grants.
func (s *Server) revalidateAccessPrincipal(ctx context.Context) (context.Context, error) {
	p, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || p.WorkspaceID == "" || p.UserID == "" || p.SessionID == "" {
		return ctx, service.ErrAccessDenied
	}
	store, ok := s.store.(service.WorkspaceStorer)
	if !ok {
		return ctx, service.ErrAccessDenied
	}
	live, _, err := store.ResolveWorkspaceAccess(ctx, p.WorkspaceID, p.UserID, p.SessionID)
	if err != nil {
		return ctx, err
	}
	return service.WithAccessPrincipal(ctx, live), nil
}
