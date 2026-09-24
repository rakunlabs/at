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
	// The disabled flag is read alongside the model so a parked provider fails
	// when an agent starts, with its real reason, instead of looking absent.
	metadataSelect := []any{
		goqu.L("COALESCE(config->>'model','')").As("model"),
		goqu.L("COALESCE(config->>'disabled','') = 'true'").As("disabled"),
	}
	var meta struct {
		Model    string `db:"model"`
		Disabled bool   `db:"disabled"`
	}
	personalID, personalReference := service.ParsePersonalProviderReference(key)
	if provenance, _, ok := service.ExecutionFromContext(ctx); personalReference && ok && provenance.ServiceID != "" {
		return "", service.ErrAccessDenied
	}
	query := p.goqu.From(p.tableProviders).Select(metadataSelect...).Where(goqu.Ex{"key": key, "workspace_id": actor.WorkspaceID, "owner_user_id": ""})
	if personalReference {
		grants := p.workspaceTable("personal_provider_grants")
		query = p.goqu.From(p.tableProviders).Select(metadataSelect...).Where(
			goqu.Ex{"id": personalID, "workspace_id": nil},
			goqu.Or(
				goqu.C("owner_user_id").Eq(actor.UserID),
				goqu.C("id").In(p.goqu.From(grants).Select("provider_id").Where(goqu.Or(goqu.C("global").Eq(true), goqu.C("workspace_id").Eq(actor.WorkspaceID)))),
			),
		)
	}
	found, err := query.ScanStructContext(ctx, &meta)
	if err != nil {
		return "", fmt.Errorf("resolve model metadata: %w", err)
	}
	if !found && !personalReference {
		found, err = p.goqu.From(p.tableProviders).Select(metadataSelect...).Where(goqu.Ex{"key": key, "workspace_id": "legacy-default"}, goqu.Or(goqu.L("config->>'shared_with_all_workspaces' = 'true'"), goqu.C("id").In(p.goqu.From(p.workspaceTable("workspace_provider_grants")).Select("provider_id").Where(goqu.Ex{"workspace_id": actor.WorkspaceID})))).ScanStructContext(ctx, &meta)
	}
	if err != nil {
		return "", fmt.Errorf("resolve granted model metadata: %w", err)
	}
	if !found {
		return "", service.ErrAccessDenied
	}
	if meta.Disabled {
		return "", service.ErrProviderDisabled
	}
	return meta.Model, nil
}
