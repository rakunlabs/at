package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

func workspaceBusinessError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, service.ErrAccessDenied) || errors.Is(err, service.ErrWorkspaceRequired) || errors.Is(err, service.ErrWorkspaceConflict) {
		workspaceError(w, err)
		return true
	}
	return false
}

// BusinessRoutePolicy declares admission only after the complete handler/store
// path is scoped. Unlisted routes retain installation-only admission.
type BusinessRoutePolicy struct{ Method, Pattern, Capability, Kind, IDParam string }

func workspaceBusinessPolicies() []BusinessRoutePolicy {
	routes := []BusinessRoutePolicy{
		{"GET", "/providers", "providers.read", "", ""},
		{"POST", "/providers", "providers.write", "", ""},
		{"GET", "/providers/{key}", "providers.read", "providers", "key"},
		{"PUT", "/providers/{key}", "providers.write", "providers", "key"},
		{"DELETE", "/providers/{key}", "providers.write", "providers", "key"},
		{"GET", "/approvals/pending", "approvals.read", "", ""},
		{"GET", "/goals/{id}/children", "goals.read", "goals", "id"},
		{"GET", "/goals/{id}/ancestry", "goals.read", "goals", "id"},
		{"GET", "/goals/{id}/projects", "goals.read", "goals", "id"},
		{"GET", "/organizations/{id}/projects", "organizations.read", "organizations", "id"},
		{"GET", "/organizations/{id}/agents", "organizations.read", "organizations", "id"},
		{"POST", "/organizations/{id}/agents", "organizations.write", "organizations", "id"},
		{"PUT", "/organizations/{id}/agents/{agent_id}", "organizations.write", "organizations", "id"},
		{"DELETE", "/organizations/{id}/agents/{agent_id}", "organizations.write", "organizations", "id"},
		{"GET", "/agents/{id}/tasks", "agents.read", "agents", "id"},
		{"GET", "/tasks/{id}/comments", "tasks.read", "tasks", "id"},
		{"POST", "/tasks/{id}/comments", "comments.write", "tasks", "id"},
		{"GET", "/tasks/{id}/labels", "tasks.read", "tasks", "id"},
		{"GET", "/labels/{id}/tasks", "labels.read", "labels", "id"},
		{"POST", "/tasks/{id}/labels/{label_id}", "tasks.write", "tasks", "id"},
		{"DELETE", "/tasks/{id}/labels/{label_id}", "tasks.write", "tasks", "id"},
		{"POST", "/tasks/{id}/checkout", "tasks.write", "tasks", "id"},
		{"POST", "/tasks/{id}/release", "tasks.write", "tasks", "id"},
		{"GET", "/task-board", "tasks.read", "", ""},
		{"PUT", "/task-board", "tasks.write", "", ""},
		{"DELETE", "/task-board", "tasks.write", "", ""},
	}
	for _, kind := range []string{"organizations", "agents", "goals", "projects", "tasks", "labels", "approvals"} {
		routes = append(routes, BusinessRoutePolicy{"GET", "/" + kind, kind + ".read", "", ""}, BusinessRoutePolicy{"POST", "/" + kind, kind + ".write", "", ""}, BusinessRoutePolicy{"GET", "/" + kind + "/{id}", kind + ".read", kind, "id"}, BusinessRoutePolicy{"PUT", "/" + kind + "/{id}", kind + ".write", kind, "id"})
		if kind != "approvals" {
			routes = append(routes, BusinessRoutePolicy{"DELETE", "/" + kind + "/{id}", kind + ".write", kind, "id"})
		}
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		cap := "comments.write"
		if method == "GET" {
			cap = "comments.read"
		}
		routes = append(routes, BusinessRoutePolicy{method, "/comments/{id}", cap, "comments", "id"})
	}
	return routes
}

// sharedPlatformRoutes are installation-wide routes that every signed-in
// member may reach, not just administrators. Guides are the in-product
// documentation surface: `/docs` is open to all authenticated users, so gating
// its content behind the admin role only produced a broken page for everyone
// else.
func sharedPlatformRoutes() []BusinessRoutePolicy {
	return []BusinessRoutePolicy{
		{"GET", "/guides", "", "", ""},
		{"POST", "/guides", "", "", ""},
		{"GET", "/guides/{id}", "", "", ""},
		{"PUT", "/guides/{id}", "", "", ""},
		{"DELETE", "/guides/{id}", "", "", ""},
	}
}

func isSharedPlatformRoute(method, path string) bool {
	for _, route := range sharedPlatformRoutes() {
		if route.Method == method && matchesBusinessPattern(route.Pattern, path) {
			return true
		}
	}
	return false
}

func matchesBusinessPattern(pattern, path string) bool {
	want, got := strings.Split(pattern, "/"), strings.Split(path, "/")
	if len(want) != len(got) {
		return false
	}
	for i, part := range want {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			if got[i] == "" {
				return false
			}
			continue
		}
		if part != got[i] {
			return false
		}
	}
	return true
}

// Parent wiring: replace apiGroup's blanket native gate with this middleware.
// Runtime authentication remains a separate inner boundary for execution routes.
func (s *Server) workspaceBusinessAuthentication() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, scoped := service.AccessPrincipalFromContext(r.Context())
			if !scoped && service.LegacyWorkspaceAccessFromContext(r.Context()) && nativeRuntimeFromRequest(r, s.nativeAuth) == nil {
				if len(r.Header.Values("X-AT-Workspace-ID")) > 0 {
					nativeError(w, 400, "workspace selection requires native authentication")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			path := strings.TrimPrefix(r.URL.Path, strings.TrimSuffix(s.config.BasePath, "/")+"/api/v1")
			var policy *BusinessRoutePolicy
			for _, candidate := range workspaceBusinessPolicies() {
				if candidate.Method == r.Method && matchesBusinessPattern(candidate.Pattern, path) {
					copy := candidate
					policy = &copy
					break
				}
			}
			if policy == nil {
				s.requireWorkspacePlatform(!isSharedPlatformRoute(r.Method, path))(next).ServeHTTP(w, r)
				return
			}
			s.workspaceAuthentication(true, "")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				principal, ok := service.AccessPrincipalFromContext(r.Context())
				if !ok {
					workspaceError(w, service.ErrAccessDenied)
					return
				}
				resource := service.AccessResource{WorkspaceID: principal.WorkspaceID}
				if policy.IDParam != "" {
					if policy.Kind == "providers" {
						if s.store == nil {
							nativeError(w, 503, "provider store unavailable")
							return
						}
						v, err := s.store.GetProvider(r.Context(), r.PathValue(policy.IDParam))
						if err != nil {
							workspaceError(w, err)
							return
						}
						if v == nil {
							workspaceError(w, service.ErrAccessResourceNotFound)
							return
						}
						resource = service.AccessResource{Kind: "providers", ID: v.ID, WorkspaceID: v.WorkspaceID}
					} else {
						store, ok := s.store.(service.WorkspaceResourceStorer)
						if !ok {
							nativeError(w, 503, "workspace resource store unavailable")
							return
						}
						v, err := store.GetWorkspaceAccessResource(r.Context(), policy.Kind, r.PathValue(policy.IDParam))
						if err != nil {
							workspaceError(w, err)
							return
						}
						resource = *v
					}
				}
				if !principal.Allows(policy.Capability, resource) {
					workspaceError(w, service.ErrAccessDenied)
					return
				}
				next.ServeHTTP(w, r)
			})).ServeHTTP(w, r)
		})
	}
}
