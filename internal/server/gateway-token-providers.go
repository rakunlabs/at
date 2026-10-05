package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/service"
)

// gatewayTokenPrincipal binds the owner of a personal API token as the access
// principal of its workspace. The in-memory gateway registry holds only
// Default-workspace providers by bare key, so without it a personal provider
// (provider:<id>) or a provider of a non-default workspace was neither listed
// by /gateway/v1/models nor resolvable, although Chats offers it to the same
// account. Access is resolved live on every call, so revoked memberships and
// grants apply immediately. Shared workspace tokens carry no account and keep
// the registry-only behaviour.
func (s *Server) gatewayTokenPrincipal(ctx context.Context, auth *authResult) (context.Context, bool) {
	if auth == nil || auth.token == nil || auth.token.OwnerUserID == "" || auth.token.WorkspaceID == "" {
		return ctx, false
	}
	workspaces, ok := s.store.(service.WorkspaceStorer)
	if !ok {
		return ctx, false
	}
	principal, _, err := workspaces.ResolveWorkspaceAccess(ctx, auth.token.WorkspaceID, auth.token.OwnerUserID, "")
	if err != nil {
		slog.Debug("gateway token owner has no workspace access", "token_id", auth.token.ID, "error", err)
		return ctx, false
	}
	return service.WithAccessPrincipal(ctx, principal), true
}

// gatewayTokenCatalogModels lists the models of the token owner's workspace
// catalog that are not already advertised. Personal providers are listed under
// their immutable reference (provider:<id>), which is also how they resolve.
func (s *Server) gatewayTokenCatalogModels(ctx context.Context, auth *authResult, listed map[string]bool) []wire.ModelData {
	catalogStore, ok := s.store.(service.WorkspaceProviderCatalogStorer)
	if !ok {
		return nil
	}
	pctx, ok := s.gatewayTokenPrincipal(ctx, auth)
	if !ok {
		return nil
	}
	catalog, err := catalogStore.ListWorkspaceProviderCatalog(pctx)
	if err != nil {
		slog.Error("list gateway token provider catalog failed", "error", err)
		return nil
	}
	var models []wire.ModelData
	for _, entry := range catalog {
		// Virtual providers are listed by the gateway's own virtual catalog;
		// decision services never belong in a chat model picker.
		if entry.Type == "virtual" || entry.Type == "systemone" {
			continue
		}
		key := entry.Key
		if entry.Reference != "" {
			key = entry.Reference
		}
		for _, m := range entry.Models {
			fullID := key + "/" + m
			if m == "" || listed[fullID] || !auth.isModelAllowed(key, fullID) {
				continue
			}
			listed[fullID] = true
			model := wire.ModelData{ID: fullID, Object: "model", OwnedBy: key, Mode: "chat"}
			applyResolvedModelCapabilities(&model, entry.ModelCapabilities[m])
			models = append(models, model)
		}
	}
	return models
}

// gatewayTokenProviderInfo resolves a provider the registry does not hold
// through the token owner's workspace catalog, with the same admission as
// Chats and agent execution (model grants, disabled state, personal grants).
func (s *Server) gatewayTokenProviderInfo(ctx context.Context, auth *authResult, key, model string) (ProviderInfo, error) {
	routes, ok := s.store.(service.ProviderRouteStorer)
	if !ok || s.providerFactory == nil {
		return ProviderInfo{}, service.ErrAccessDenied
	}
	pctx, ok := s.gatewayTokenPrincipal(ctx, auth)
	if !ok {
		return ProviderInfo{}, service.ErrAccessDenied
	}
	route, err := routes.ResolveWorkspaceProviderRoute(pctx, key, model)
	if err != nil {
		return ProviderInfo{}, err
	}
	if route == nil {
		return ProviderInfo{}, service.ErrAccessResourceNotFound
	}
	cfg := route.Record.Config
	if route.VirtualProviderID == "" && len(cfg.Models) > 0 && !slices.Contains(cfg.Models, model) && cfg.Model != model {
		return ProviderInfo{}, fmt.Errorf("model %q is not available for provider %q", model, key)
	}
	provider, err := s.cachedWorkspaceProvider(&route.Record)
	if err != nil {
		return ProviderInfo{}, err
	}
	provider = s.providerForRoute(route, provider, auth.token.OwnerUserID)
	info := NewProviderInfo(provider, cfg).WithProviderID(route.Record.ID)
	if route.VirtualProviderID != "" {
		info.providerType = "virtual"
		info.defaultModel = model
		info.models = []string{model}
	}
	return info, nil
}

// gatewayTokenProviderError maps a catalog resolution failure onto the
// gateway's provider errors without revealing which providers exist.
func gatewayTokenProviderError(key string, err error) error {
	if errors.Is(err, service.ErrProviderDisabled) {
		return providerDisabledError{key: key}
	}
	return fmt.Errorf("provider %q not found", key)
}
