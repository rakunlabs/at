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
		{"GET", "/info", "workspace.read", "", ""},
		{"GET", "/bots/video-templates", "platform.manage", "", ""},
		{"POST", "/bots/{id}/start", "bots.write", "bots", "id"},
		{"POST", "/bots/{id}/stop", "bots.write", "bots", "id"},
		{"GET", "/bots/{id}/status", "bots.read", "bots", "id"},
		{"GET", "/api-tokens", "tokens.read", "", ""},
		{"POST", "/api-tokens", "tokens.write", "", ""},
		{"PUT", "/api-tokens/{id}", "tokens.write", "tokens", "id"},
		{"PUT", "/api-tokens/{id}/pause", "tokens.write", "tokens", "id"},
		{"POST", "/api-tokens/{id}/rotate", "tokens.write", "tokens", "id"},
		{"DELETE", "/api-tokens/{id}", "tokens.write", "tokens", "id"},
		{"GET", "/api-tokens/{id}/usage", "tokens.read", "tokens", "id"},
		{"POST", "/api-tokens/{id}/usage/reset", "tokens.write", "tokens", "id"},
		// A routing profile is provider configuration, so it reuses the provider
		// capability rather than a new kind every existing bundle would lack.
		{"GET", "/routing-profiles", "providers.read", "", ""},
		{"POST", "/routing-profiles", "providers.write", "", ""},
		{"GET", "/routing-profiles/{id}", "providers.read", "routing_profiles", "id"},
		{"PUT", "/routing-profiles/{id}", "providers.write", "routing_profiles", "id"},
		{"DELETE", "/routing-profiles/{id}", "providers.write", "routing_profiles", "id"},
		{"GET", "/workflow-node-types", "workflows.read", "", ""},
		{"POST", "/workflows/run/{id}", "workflows.execute", "workflows", "id"},
		{"POST", "/workflows/run-stream/{id}", "workflows.execute", "workflows", "id"},
		{"GET", "/workflows/{id}/versions", "workflows.read", "workflows", "id"},
		{"GET", "/workflows/{id}/versions/{version}", "workflows.read", "workflows", "id"},
		{"PUT", "/workflows/{id}/active-version", "workflows.write", "workflows", "id"},
		{"GET", "/workflows/{id}/triggers", "workflows.read", "workflows", "id"},
		{"POST", "/workflows/{id}/triggers", "workflows.write", "workflows", "id"},
		{"GET", "/runs", "workflows.read", "", ""},
		{"POST", "/runs/{id}/cancel", "workflows.execute", "", ""},
		{"GET", "/providers", "providers.read", "", ""},
		{"POST", "/providers", "providers.write", "", ""},
		{"POST", "/providers/claude-auth", "credentials.manage", "", ""},
		{"POST", "/providers/claude-auth/callback", "credentials.manage", "", ""},
		{"POST", "/providers/claude-auth/token", "credentials.manage", "", ""},
		{"POST", "/providers/claude-auth/sync", "platform.manage", "", ""},
		{"POST", "/providers/device-auth", "credentials.manage", "", ""},
		{"GET", "/providers/device-auth-status", "credentials.manage", "", ""},
		{"POST", "/providers/discover-models", "credentials.manage", "", ""},
		{"POST", "/providers/discover-embedding-models", "credentials.manage", "", ""},
		{"POST", "/model-pricing/sync/preview", "platform.manage", "", ""},
		{"POST", "/model-pricing/sync/apply", "platform.manage", "", ""},
		{"POST", "/model-pricing/agent/preview", "platform.manage", "", ""},
		{"GET", "/providers/{key}", "providers.read", "providers", "key"},
		{"PUT", "/providers/{key}", "providers.write", "providers", "key"},
		{"PUT", "/providers/{key}/disable", "providers.write", "providers", "key"},
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
		// The Playground is the per-account model workbench. History and media
		// are owner-scoped in the handlers, so models.use only gates entry —
		// the same rank as every other execution surface, so members keep it
		// and viewers do not.
		{"POST", "/chat/completions", "models.use", "", ""},
		{"GET", "/playground/conversations", "models.use", "", ""},
		{"POST", "/playground/conversations", "models.use", "", ""},
		{"GET", "/playground/conversations/{id}", "models.use", "", ""},
		{"PATCH", "/playground/conversations/{id}", "models.use", "", ""},
		{"DELETE", "/playground/conversations/{id}", "models.use", "", ""},
		{"POST", "/playground/conversations/{id}/fork", "models.use", "", ""},
		{"GET", "/playground/conversations/{id}/messages", "models.use", "", ""},
		{"POST", "/playground/conversations/{id}/messages", "models.use", "", ""},
		{"DELETE", "/playground/conversations/{id}/messages", "models.use", "", ""},
		{"GET", "/playground/defaults", "models.use", "", ""},
		{"PUT", "/playground/defaults", "models.use", "", ""},
		// The Playground's tool plane. These endpoints dispatch server-side
		// tools for a browser-driven loop, so they ride the same `models.use`
		// entry capability as the Playground itself; what a caller may actually
		// run is decided one layer down by the workspace execution policy
		// (CheckExecution: tool/inline_tool class, trusted-host for host tools,
		// `platform.files` for the legacy host-path file tools). Admission and
		// execution are deliberately separate: the capability says "may use the
		// workbench", the policy says "may run this".
		{"GET", "/mcp/builtin-tools", "models.use", "", ""},
		{"POST", "/mcp/call-builtin-tool", "models.use", "", ""},
		{"POST", "/mcp/call-skill-tool", "models.use", "", ""},
		{"GET", "/mcp/set-tools/{name}", "mcp.read", "", ""},
		{"POST", "/mcp/set-tools/{name}/call", "mcp.use", "", ""},
		// Read-only catalogs the capability-admitted surfaces depend on: the
		// Playground tool picker and the Agents editor both need to enumerate
		// skills, MCP sets and connections to configure an agent. Management of
		// all three stays installation administration — only the list is open,
		// and it is already the capability the store enforces on these tables.
		// `ListConnections` blanks credentials for a caller without
		// `credentials.manage`, so the list carries no secret.
		{"GET", "/skills", "skills.read", "", ""},
		{"GET", "/mcp/sets", "mcp.read", "", ""},
		{"GET", "/connections", "connections.read", "", ""},
		// Chat sessions are per-account: rows are owner-scoped in the handlers
		// and the list predicate (an administrator additionally sees ownerless
		// bot/legacy rows), so the capability only gates entry — agents.read
		// to look, agents.execute to create sessions and drive the agentic
		// loop. No Kind/IDParam: the resource decision is ownership, which the
		// admission middleware cannot express.
		{"GET", "/chat/sessions", "agents.read", "", ""},
		{"POST", "/chat/sessions", "agents.execute", "", ""},
		{"GET", "/chat/sessions/{id}", "agents.read", "", ""},
		{"PUT", "/chat/sessions/{id}", "agents.execute", "", ""},
		{"DELETE", "/chat/sessions/{id}", "agents.execute", "", ""},
		{"GET", "/chat/sessions/{id}/messages", "agents.read", "", ""},
		{"POST", "/chat/sessions/{id}/messages", "agents.execute", "", ""},
		{"DELETE", "/chat/sessions/{id}/messages", "agents.execute", "", ""},
		{"POST", "/chat/sessions/{id}/confirm", "agents.execute", "", ""},
		// Media settings are installation configuration; the literal pattern
		// must precede /media/{id}, whose parameter would otherwise swallow it.
		{"GET", "/media/settings", "platform.manage", "", ""},
		{"POST", "/media", "models.use", "", ""},
		{"GET", "/media/{id}", "models.use", "", ""},
		{"DELETE", "/media/{id}", "models.use", "", ""},
		// Usage and traces are workspace data (cost_events and llm_calls carry
		// workspace_id), so they ride their registry capabilities. The role
		// ladder places both at admin rank: they were installation-only
		// surfaces, and trace bodies carry full prompts.
		{"GET", "/usage/summary", "usage.read", "", ""},
		{"GET", "/usage/grouped", "usage.read", "", ""},
		{"GET", "/usage/timeseries", "usage.read", "", ""},
		{"GET", "/usage/budgets", "usage.read", "", ""},
		{"GET", "/llm-calls", "traces.read", "", ""},
		{"GET", "/llm-calls/traces", "traces.read", "", ""},
		{"GET", "/llm-calls/conversations", "traces.read", "", ""},
		{"GET", "/llm-calls/{id}", "traces.read", "", ""},
	}
	for _, kind := range []string{"organizations", "agents", "goals", "projects", "tasks", "labels", "approvals", "workflows", "bots"} {
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
//
// Reading the feature catalog is the other one. It is how the UI decides which
// surfaces exist at all, and `isFeatureEnabled` reports *enabled* until the
// catalog arrives so a slow request never blanks the application. Admin-only
// admission therefore inverted the switch for exactly the accounts that cannot
// change it: a non-administrator's request answered 403, the catalog never
// loaded, and every disabled feature stayed visible and linked. The response is
// installation configuration, not workspace data, and it is already the answer
// to "which pages does this deployment have" that the sidebar must know.
// Writing it (`PUT /features`, `PUT /features/{key}`, the presets) stays
// administration and keeps the default admission.
func sharedPlatformRoutes() []BusinessRoutePolicy {
	return []BusinessRoutePolicy{
		{"GET", "/features", "", "", ""},
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
