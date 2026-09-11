package postgres

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) LegacyExecutionValidation(ctx context.Context, action service.ExecutionAction) (service.ExecutionValidation, error) {
	if !service.LegacyWorkspaceAccessFromContext(ctx) {
		return service.ExecutionValidation{}, service.ErrExecutionDenied
	}
	if _, ok := service.AccessPrincipalFromContext(ctx); ok {
		return service.ExecutionValidation{}, service.ErrExecutionDenied
	}
	actor, err := p.legacyBusinessPrincipal(ctx, p.goqu, false)
	if err != nil {
		return service.ExecutionValidation{}, err
	}
	policy, err := p.GetExecutionPolicy(ctx, "legacy-default")
	if err != nil {
		return service.ExecutionValidation{}, err
	}
	// Explicit legacy installation admission preserves the previously trusted
	// deployment only until an operator installs a versioned workspace policy.
	// New native/scoped workspaces still default to restricted.
	if policy.Version == 0 {
		policy.Mode = service.ExecutionTrustedHost
		policy.GrantedBy = "explicit-legacy-installation"
	}
	allowed := !actor.ExecutionDisabled || action.Kind == "file" || (action.Kind == "resource" && (action.Name == "execution.run" || (action.Name == "execution.host" && action.ResourceKind == "file")))
	return service.ExecutionValidation{Policy: *policy, PlatformAdmin: true, Allowed: allowed}, nil
}
