package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/service"
)

// bindRuntimePrincipal accepts only the principal installed by workspace auth.
// It captures the live versions once; actions must match this run's ceiling.
func (s *Server) bindRuntimePrincipal(ctx context.Context, source string) (context.Context, error) {
	if _, _, ok := service.ExecutionFromContext(ctx); ok {
		return ctx, nil
	}
	if _, ok := service.AccessPrincipalFromContext(ctx); !ok && service.LegacyWorkspaceAccessFromContext(ctx) {
		return s.bindLegacyRuntime(ctx, source)
	}
	liveCtx, err := s.revalidateAccessPrincipal(ctx)
	if err != nil {
		return nil, fmt.Errorf("runtime principal revalidation: %w: %w", service.ErrExecutionDenied, err)
	}
	p, ok := service.AccessPrincipalFromContext(liveCtx)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	store, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	policy, err := store.GetExecutionPolicy(liveCtx, p.WorkspaceID)
	if err != nil || policy == nil {
		return nil, fmt.Errorf("load runtime policy: %w", err)
	}
	return s.BindRuntimeExecution(liveCtx, service.ExecutionProvenance{RunID: ulid.Make().String(), UserID: p.UserID, WorkspaceID: p.WorkspaceID, SessionID: p.SessionID, MembershipVersion: p.MembershipVersion, PolicyVersion: policy.Version, Source: source}, s.revalidateRuntimeExecution)
}

// BindPlatformRuntimeExecution is the parent's opt-in compatibility hook for
// authenticated installation-administrator routes. It requires an already
// configured trusted-host workspace policy and a live native platform admin;
// neither an absent principal nor an ordinary workspace owner can enter it.
func (s *Server) BindPlatformRuntimeExecution(ctx context.Context, source string) (context.Context, error) {
	ctx, err := s.bindRuntimePrincipal(ctx, source)
	if err != nil {
		return nil, err
	}
	p, root, ok := service.ExecutionFromContext(ctx)
	if !ok || p.ServiceID != "" || p.SessionID == "" {
		return nil, service.ErrExecutionDenied
	}
	return service.BindPlatformExecution(ctx, p, root, s.revalidateRuntimeExecution)
}

// registerRuntimeRoutes must replace these routes in the legacy admin group;
// it is separate so server.go's owner can wire one explicit route inventory.
func (s *Server) registerRuntimeRoutes(mux *ada.Server, base string) {
	for _, route := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{"GET", "/api/v1/files/browse", s.FileBrowseAPI},
		{"GET", "/api/v1/files/serve", s.FileServeAPI},
		{"HEAD", "/api/v1/files/serve", s.FileServeAPI},
		{"POST", "/api/v1/files/upload", s.FileUploadAPI},
		{"DELETE", "/api/v1/files", s.FileDeleteAPI},
		{"GET", "/api/v1/workspaces/{workspace}/execution-policy", s.RuntimeExecutionPolicyAPI},
		{"PUT", "/api/v1/workspaces/{workspace}/execution-policy", s.RuntimeExecutionPolicyAPI},
		{"POST", "/api/v1/triggers/{id}/execution-binding", s.RuntimeTriggerBindingAPI},
		{"POST", "/api/v1/bots/{id}/execution-binding", s.RuntimeBotBindingAPI},
		{"DELETE", "/api/v1/triggers/{id}/execution-binding", s.RuntimeTriggerBindingAPI},
		{"DELETE", "/api/v1/bots/{id}/execution-binding", s.RuntimeBotBindingAPI},
		{"GET", "/api/v1/bots/{id}/execution-binding", s.RuntimeBotBindingAPI},
		{"GET", "/api/v1/mcp/servers/{id}/execution-binding", s.RuntimeMCPBindingAPI},
		{"POST", "/api/v1/mcp/servers/{id}/execution-binding", s.RuntimeMCPBindingAPI},
		{"DELETE", "/api/v1/mcp/servers/{id}/execution-binding", s.RuntimeMCPBindingAPI},
	} {
		handler := s.runtimeRouteHandler(route.handler)
		mux.HandleWithMethod(route.method, base+route.path, handler.ServeHTTP)
	}
}

// Parent middleware may already have authenticated an installation-compatible
// or in-process principal. Only those private context markers skip repeating
// workspace cookie/header admission; unbound HTTP requests retain the full gate.
func (s *Server) runtimeRouteHandler(next http.Handler) http.Handler {
	runtime := s.runtimeAuthentication(next)
	workspace := s.workspaceAuthentication(true, "")(runtime)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, bound := service.ExecutionFromContext(r.Context())
		_, human := service.AccessPrincipalFromContext(r.Context())
		if bound || (!human && service.LegacyWorkspaceAccessFromContext(r.Context())) {
			runtime.ServeHTTP(w, r)
			return
		}
		workspace.ServeHTTP(w, r)
	})
}

func (s *Server) runtimeRequest(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	ctx, err := s.bindRuntimePrincipal(r.Context(), "api")
	if err != nil {
		httpResponse(w, "runtime identity unavailable", http.StatusForbidden)
		return r, false
	}
	return r.WithContext(ctx), true
}

// runtimeAuthentication belongs after workspaceAuthentication. Parent routing
// can wrap any explicitly admitted runtime endpoint with this middleware.
func (s *Server) runtimeAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, err := s.bindRuntimePrincipal(r.Context(), "api")
		if err != nil {
			httpResponse(w, "runtime identity unavailable", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) revalidateRuntimeExecution(ctx context.Context, provenance service.ExecutionProvenance, action service.ExecutionAction) (service.ExecutionValidation, error) {
	old, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || old.UserID != provenance.UserID || old.WorkspaceID != provenance.WorkspaceID || old.SessionID != provenance.SessionID {
		return service.ExecutionValidation{}, service.ErrExecutionDenied
	}
	var err error
	if provenance.ServiceID != "" {
		services, ok := s.store.(service.ExecutionServiceStorer)
		if !ok {
			return service.ExecutionValidation{}, service.ErrExecutionDenied
		}
		binding, loadErr := services.GetExecutionServiceBinding(ctx, "id", provenance.ServiceID)
		if loadErr != nil {
			return service.ExecutionValidation{}, loadErr
		}
		if binding == nil || binding.Revoked || binding.UserID != provenance.UserID || binding.WorkspaceID != provenance.WorkspaceID || binding.Version != provenance.ServiceVersion {
			return service.ExecutionValidation{}, service.ErrExecutionDenied
		}
		workspaces, ok := s.store.(service.WorkspaceStorer)
		if !ok {
			return service.ExecutionValidation{}, service.ErrExecutionDenied
		}
		live, _, resolveErr := workspaces.ResolveWorkspaceAccess(ctx, binding.WorkspaceID, binding.UserID, "")
		err = resolveErr
		ctx = service.WithAccessPrincipal(ctx, live)
	} else {
		ctx, err = s.revalidateAccessPrincipal(ctx)
	}
	if err != nil {
		return service.ExecutionValidation{}, err
	}
	p, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || p.UserID != provenance.UserID || p.WorkspaceID != provenance.WorkspaceID || p.SessionID != provenance.SessionID {
		return service.ExecutionValidation{}, service.ErrExecutionDenied
	}
	store, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return service.ExecutionValidation{}, service.ErrExecutionDenied
	}
	policy, err := store.GetExecutionPolicy(ctx, p.WorkspaceID)
	if err != nil {
		return service.ExecutionValidation{}, err
	}
	if policy == nil {
		return service.ExecutionValidation{}, service.ErrExecutionDenied
	}
	v := service.ExecutionValidation{Policy: *policy, MembershipVersion: p.MembershipVersion, PlatformAdmin: p.PlatformAdmin}
	r := service.AccessResource{WorkspaceID: p.WorkspaceID, Path: action.Path, ID: action.ResourceID}
	if action.WorkspaceID != "" && action.WorkspaceID != p.WorkspaceID {
		return v, nil
	}
	if action.Kind == "file" {
		if strings.HasPrefix(action.Path, ".at-llm-audit/") || strings.HasPrefix(action.Path, ".at-tool-output/") {
			if !p.Allows("traces.read", r) {
				return v, nil
			}
		}
		cap := action.Name
		if cap == "files.host" {
			cap = "platform.files"
		}
		r.Kind = "files"
		v.Allowed = p.Allows(cap, r)
		return v, nil
	}
	if action.Kind == "resource" {
		switch action.Name {
		case "execution.run":
			v.Allowed = p.Allows("workspace.read", r)
			return v, nil
		case "execution.policy.manage":
			v.Allowed = p.PlatformAdmin && p.Allows("execution.configure", r)
			return v, nil
		case "execution.host":
			// The platform's persisted policy is the host grant. Workspace role
			// edits cannot create this grant. It remains a trusted deployment,
			// with daemon authority, rather than cross-tenant isolation.
			v.Allowed = !p.ExecutionDisabled && policy.Mode == service.ExecutionTrustedHost && policy.GrantedBy != "" && p.Allows("agents.execute", r)
			return v, nil
		}
	}
	if p.ExecutionDisabled {
		return v, nil
	}
	if action.Kind == "resource" && action.Name == "providers.use" {
		if providers, ok := s.store.(service.WorkspaceProviderStorer); ok {
			if action.Model == "" {
				v.Allowed = p.Allows("workspace.read", r)
				return v, nil
			}
			_, err := providers.ResolveWorkspaceProviderForUse(ctx, action.ResourceID, action.Model)
			v.Allowed = err == nil
			return v, err
		}
	}
	if action.Kind == "tool" || action.Kind == "node" || action.Kind == "handler" || action.Kind == "inline_tool" {
		// Admission to the owning task/workflow/agent and specific file or
		// credential actions is checked independently at each boundary.
		v.Allowed = p.Allows("workspace.read", r)
		return v, nil
	}
	kind, cap := "", ""
	switch action.Kind {
	case "skill_tool":
		kind, cap = "skills", "skills.read"
	case "mcp_tool":
		kind, cap = "mcp", "mcp.read"
	case "delegate":
		kind, cap = "agents", "agents.execute"
		if action.ResourceKind == "workflows" {
			kind, cap = "workflows", "workflows.execute"
		}
	case "resource":
		switch action.Name {
		case "providers.use":
			kind, cap = "providers", "models.use"
		case "skills.use":
			kind, cap = "skills", "skills.read"
		case "variables.read":
			kind, cap = "variables", "variables.read"
		case "connections.use":
			kind, cap = "connections", "connections.read"
		case "agents.run":
			kind, cap = "agents", "agents.execute"
		case "workflows.run":
			kind, cap = "workflows", "workflows.execute"
		case "tasks.run":
			kind, cap = "tasks", "tasks.execute"
		case "triggers.use":
			kind, cap = "triggers", "workflows.read"
		case "bots.use":
			kind, cap = "bots", "bots.read"
		case "organizations.run":
			kind, cap = "organizations", "organizations.read"
		case "chats.run":
			kind, cap = "chats", "agents.execute"
		case "mcp.use":
			kind, cap = "mcp", "mcp.read"
		case "mcp_servers.use":
			kind, cap = "mcp_servers", "mcp.read"
		case "node_configs.use":
			kind, cap = "node_configs", "credentials.manage"
		}
	}
	resolver, ok := s.store.(service.ExecutionResourceResolver)
	if !ok || kind == "" {
		return v, nil
	}
	r, err = resolver.ResolveExecutionResource(ctx, kind, action.ResourceID)
	if err != nil {
		return v, err
	}
	v.Allowed = p.Allows(cap, r)
	return v, nil
}
