package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
)

// ListAuthLoginLocks reads only lock metadata, never account factors or secrets.
func (p *Postgres) ListAuthLoginLocks(ctx context.Context, ids []string) (map[string]time.Time, error) {
	locks := make(map[string]time.Time)
	if len(ids) == 0 {
		return locks, nil
	}
	var rows []struct {
		UserID string    `db:"user_id"`
		Until  time.Time `db:"locked_until"`
	}
	until := goqu.L("NULLIF(data->>'PasswordLockedUntil', '')::timestamptz")
	if err := p.goqu.From(p.authSecurityTable("auth_security")).
		Select("user_id", until.As("locked_until")).
		Where(goqu.I("user_id").In(ids), until.Gt(goqu.L("clock_timestamp()"))).
		ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list password login locks: %w", err)
	}
	for _, row := range rows {
		locks[row.UserID] = row.Until
	}
	return locks, nil
}
