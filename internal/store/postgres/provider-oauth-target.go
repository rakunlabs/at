package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

// lockOAuthProvider resolves an immutable personal reference or a legacy
// workspace/key pair while holding the provider row lock. Personal credentials
// can refresh for their owner or an interactive user granted inference access;
// service-bound machine execution never inherits a human provider.
func (p *Postgres) lockOAuthProvider(ctx context.Context, tx *goqu.TxDatabase, workspace, reference string) (providerRow, error) {
	var row providerRow
	if id, personal := service.ParsePersonalProviderReference(reference); personal {
		actor, err := p.businessPrincipal(ctx)
		if err != nil {
			return row, err
		}
		if provenance, _, ok := service.ExecutionFromContext(ctx); ok && provenance.ServiceID != "" {
			return row, service.ErrAccessDenied
		}
		grants := p.workspaceTable("personal_provider_grants")
		found, err := tx.From(p.tableProviders).
			Select(personalProviderSelect()...).
			Where(
				goqu.Ex{"id": id, "workspace_id": nil},
				goqu.Or(
					goqu.C("owner_user_id").Eq(actor.UserID),
					goqu.C("id").In(tx.From(grants).Select("provider_id").Where(goqu.Or(goqu.C("global").Eq(true), goqu.C("workspace_id").Eq(actor.WorkspaceID)))),
				),
			).
			ForUpdate(goqu.Wait).
			ScanStructContext(ctx, &row)
		if err != nil {
			return row, fmt.Errorf("load personal OAuth provider: %w", err)
		}
		if !found {
			return row, service.ErrAccessDenied
		}
	} else {
		found, err := tx.From(p.tableProviders).
			Where(goqu.Ex{"workspace_id": workspace, "owner_user_id": "", "key": reference}).
			ForUpdate(goqu.Wait).
			ScanStructContext(ctx, &row)
		if err != nil {
			return row, fmt.Errorf("load OAuth provider: %w", err)
		}
		if !found {
			return row, service.ErrAccessDenied
		}
	}

	return row, nil
}
