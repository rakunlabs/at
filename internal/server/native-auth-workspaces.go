package server

import (
	"context"
	"errors"
	"net/http"
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
