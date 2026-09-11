package server

import (
	"context"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

type legacyExecutionStorer interface {
	LegacyExecutionValidation(context.Context, service.ExecutionAction) (service.ExecutionValidation, error)
}

// This path requires the workspace owner's explicit installation marker,
// installed only after the parent's legacy/platform authentication gate. It is
// not selected from a missing cookie, request header, or missing principal.
func (s *Server) bindLegacyRuntime(ctx context.Context, source string) (context.Context, error) {
	if !service.LegacyWorkspaceAccessFromContext(ctx) {
		return nil, service.ErrExecutionDenied
	}
	if _, ok := service.AccessPrincipalFromContext(ctx); ok {
		return nil, service.ErrExecutionDenied
	}
	store, ok := s.store.(legacyExecutionStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	validate := func(ctx context.Context, p service.ExecutionProvenance, a service.ExecutionAction) (service.ExecutionValidation, error) {
		if p.WorkspaceID != "legacy-default" || p.UserID != "legacy-installation" || p.ServiceID != "" {
			return service.ExecutionValidation{}, service.ErrExecutionDenied
		}
		return store.LegacyExecutionValidation(ctx, a)
	}
	v, err := store.LegacyExecutionValidation(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"})
	if err != nil {
		return nil, err
	}
	ctx, err = s.BindRuntimeExecution(ctx, service.ExecutionProvenance{RunID: ulid.Make().String(), UserID: "legacy-installation", WorkspaceID: "legacy-default", Source: source, PolicyVersion: v.Policy.Version}, validate)
	if err != nil {
		return nil, err
	}
	p, root, _ := service.ExecutionFromContext(ctx)
	return service.BindPlatformExecution(ctx, p, root, validate)
}
