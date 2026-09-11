package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

// This returns model metadata only, never provider credentials. Actual use is
// admitted through ResolveWorkspaceProviderForUse with the selected model.
func (p *Postgres) ExecutionProviderDefaultModel(ctx context.Context, key string) (string, error) {
	actor, err := p.businessPrincipal(ctx)
	if err != nil {
		return "", err
	}
	var model string
	query := p.goqu.From(p.tableProviders).Select(goqu.L("COALESCE(config->>'model','')")).Where(goqu.Ex{"key": key, "workspace_id": actor.WorkspaceID})
	found, err := query.ScanValContext(ctx, &model)
	if err != nil {
		return "", fmt.Errorf("resolve model metadata: %w", err)
	}
	if !found {
		found, err = p.goqu.From(p.tableProviders).Select(goqu.L("COALESCE(config->>'model','')")).Where(goqu.Ex{"key": key, "workspace_id": "legacy-default"}, goqu.C("id").In(p.goqu.From(p.workspaceTable("workspace_provider_grants")).Select("provider_id").Where(goqu.Ex{"workspace_id": actor.WorkspaceID}))).ScanValContext(ctx, &model)
	}
	if err != nil {
		return "", fmt.Errorf("resolve granted model metadata: %w", err)
	}
	if !found {
		return "", service.ErrAccessDenied
	}
	return model, nil
}
