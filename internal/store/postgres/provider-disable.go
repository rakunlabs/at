package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

// SetProviderDisabled parks or resumes a provider without reading, decrypting
// or rewriting its credentials: the flag is patched into the config JSONB in
// place, so an availability change can never damage stored keys, model lists or
// OAuth state. Disabling a shared Default-workspace provider affects every
// workspace, so it carries the same platform-admin guard as sharing itself.
func (p *Postgres) SetProviderDisabled(ctx context.Context, key string, disabled bool, updatedBy string) error {
	w, err := p.beginBusinessNamedWrite(ctx, p.tableProviders, "key", key, "providers.write")
	if err != nil {
		return err
	}
	defer w.tx.Rollback()

	if err := p.checkProviderSharingWrite(ctx, w, key, false); err != nil {
		return err
	}

	stmt, _, err := p.goqu.Update(p.tableProviders).Set(
		goqu.Record{
			"config":     goqu.L("jsonb_set(config, '{disabled}', to_jsonb(?::boolean), true)", disabled),
			"updated_at": time.Now().UTC(),
			"updated_by": updatedBy,
		},
	).Where(w.predicate, goqu.I("key").Eq(key)).ToSQL()
	if err != nil {
		return fmt.Errorf("build provider disable update: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, stmt)
	if err != nil {
		return fmt.Errorf("update provider availability: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check provider availability update: %w", err)
	}
	if affected == 0 {
		return service.ErrAccessResourceNotFound
	}
	if err := w.tx.Commit(); err != nil {
		return fmt.Errorf("commit provider availability update: %w", err)
	}
	return nil
}
