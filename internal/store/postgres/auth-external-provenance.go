package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

// recordAuthExternalSession is called by insertAuthSession after family insertion,
// in the same transaction while the user row is locked. It must not acquire the
// provider/admission locks here: provider edits take those before the user lock.
// Provider edits/unlink/recovery always bump user versions and revoke all affected
// families, so they either win before this user lock or revoke after this commit.
func (p *Postgres) recordAuthExternalSession(ctx context.Context, tx *goqu.TxDatabase, s service.AuthSession) error {
	c, ok := service.AuthExternalCompletionFromContext(ctx)
	if !ok {
		return nil
	}
	if c.Deadline.IsZero() || c.ProviderID == "" || c.LinkID == "" {
		return service.ErrAuthConflict
	}
	var id string
	found, err := tx.From(p.externalTable("auth_identity_links").As("l")).Join(p.externalTable("auth_identity_providers").As("p"), goqu.On(goqu.I("l.provider_id").Eq(goqu.I("p.id")))).Select("l.id").Where(goqu.Ex{"l.id": c.LinkID, "l.user_id": s.UserID, "p.id": c.ProviderID, "p.version": c.ProviderVersion, "p.enabled": true}).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("validate external session provenance: %w", err)
	}
	if !found {
		return service.ErrAuthConflict
	}
	if err = checkAuthAdmissionDeadline(ctx, tx, c.Deadline); err != nil {
		return err
	}
	_, err = tx.Insert(p.externalTable("auth_external_session_provenance")).Rows(goqu.Record{"session_id": s.Hash, "user_id": s.UserID, "provider_id": c.ProviderID, "provider_version": c.ProviderVersion, "link_id": c.LinkID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("record external session provenance: %w", err)
	}
	return nil
}
