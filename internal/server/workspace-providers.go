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
	record, err := store.ResolveWorkspaceProviderForUse(ctx, key, model)
	if err != nil {
		return ProviderInfo{}, err
	}
	provider, err := s.providerFactory(record.Config)
	if err != nil {
		return ProviderInfo{}, fmt.Errorf("create scoped provider: %w", err)
	}
	s.wireClaudeOAuthCallback(record.Key, provider, record.WorkspaceID)
	return NewProviderInfo(provider, record.Config), nil
}

func (s *Server) reloadWorkspaceProvider(ctx context.Context, key string, cfg config.LLMConfig) error {
	if a, ok := service.AccessPrincipalFromContext(ctx); ok {
		if a.WorkspaceID != "legacy-default" {
			return nil
		}
	} else if !service.LegacyWorkspaceAccessFromContext(ctx) {
		return service.ErrWorkspaceRequired
	}
	return s.reloadProvider(key, cfg)
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
