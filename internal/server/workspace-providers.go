package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// workspaceProviderInfo deliberately bypasses the legacy bare-key registry.
// Credential resolution and the use-grant check run on every model admission.
func (s *Server) workspaceProviderInfo(ctx context.Context, key, model string) (ProviderInfo, error) {
	store, ok := s.store.(service.WorkspaceProviderStorer)
	if !ok || s.providerFactory == nil {
		return ProviderInfo{}, service.ErrAccessDenied
	}
	var route *service.ProviderRoute
	var err error
	if routes, routeOK := s.store.(service.ProviderRouteStorer); routeOK {
		route, err = routes.ResolveWorkspaceProviderRoute(ctx, key, model)
	} else {
		var record *service.ProviderRecord
		record, err = store.ResolveWorkspaceProviderForUse(ctx, key, model)
		if record != nil {
			route = &service.ProviderRoute{Record: *record, ActualModel: model}
		}
	}
	if err != nil {
		return ProviderInfo{}, err
	}
	provider, err := s.providerFactory(route.Record.Config)
	if err != nil {
		return ProviderInfo{}, fmt.Errorf("create scoped provider: %w", err)
	}
	authReference := route.Record.Key
	if route.Record.Reference != "" {
		authReference = route.Record.Reference
	}
	s.wireClaudeOAuthCallback(authReference, provider, route.Record.WorkspaceID)
	s.wireChatGPTOAuthCallback(authReference, provider, route.Record.WorkspaceID)
	provider = s.providerForRoute(route, provider, "")
	info := NewProviderInfo(provider, route.Record.Config).WithProviderID(route.Record.ID)
	if route.VirtualProviderID != "" {
		info.providerType = "virtual"
		info.defaultModel = model
		info.models = []string{model}
	}
	return info, nil
}

func (s *Server) reloadWorkspaceProvider(ctx context.Context, key string, cfg config.LLMConfig) error {
	if a, ok := service.AccessPrincipalFromContext(ctx); ok {
		if a.WorkspaceID != "legacy-default" {
			return nil
		}
	} else if !service.LegacyWorkspaceAccessFromContext(ctx) {
		return service.ErrWorkspaceRequired
	}
	if err := s.reloadProvider(key, cfg); err != nil {
		return err
	}
	if s.store != nil {
		if record, err := s.store.GetProvider(ctx, key); err == nil && record != nil {
			s.providerMu.Lock()
			info := s.providers[key]
			info.providerID = record.ID
			s.providers[key] = info
			s.providerMu.Unlock()
		}
	}
	return nil
}
func (s *Server) removeWorkspaceProvider(ctx context.Context, key string) {
	if a, ok := service.AccessPrincipalFromContext(ctx); ok {
		if a.WorkspaceID != "legacy-default" {
			return
		}
	} else if !service.LegacyWorkspaceAccessFromContext(ctx) {
		return
	}
	s.removeProvider(key)
}

func (s *Server) ListWorkspaceProviderGrantsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.WorkspaceProviderStorer)
	if !ok {
		nativeError(w, 503, "provider grant store unavailable")
		return
	}
	rows, err := store.ListWorkspaceProviderGrants(r.Context())
	if err != nil {
		workspaceError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rows}, 200)
}
func (s *Server) SaveWorkspaceProviderGrantAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.WorkspaceProviderStorer)
	if !ok {
		nativeError(w, 503, "provider grant store unavailable")
		return
	}
	var v service.WorkspaceProviderGrant
	if !decodeNativeBody(w, r, &v) {
		return
	}
	if err := store.SaveWorkspaceProviderGrant(r.Context(), v); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) DeleteWorkspaceProviderGrantAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.WorkspaceProviderStorer)
	if !ok {
		nativeError(w, 503, "provider grant store unavailable")
		return
	}
	if err := store.DeleteWorkspaceProviderGrant(r.Context(), r.PathValue("id")); err != nil {
		workspaceError(w, err)
		return
	}
	w.WriteHeader(204)
}
