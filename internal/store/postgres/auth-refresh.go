package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthCredentialStorer = (*Postgres)(nil)

func (p *Postgres) insertAuthCredentials(ctx context.Context, tx *goqu.TxDatabase, s service.AuthSession) error {
	_, err := tx.Insert(p.tableAuthCredentials).Rows(
		goqu.Record{"hash": s.AccessHash, "session_id": s.Hash, "kind": "access", "expires_at": s.AccessExpiresAt},
		goqu.Record{"hash": s.RefreshHash, "session_id": s.Hash, "kind": "refresh", "expires_at": s.ExpiresAt},
	).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("insert auth credentials: %w", err)
	}
	return nil
}

func (p *Postgres) ResolveAuthAccess(ctx context.Context, hash string) (*service.AuthUser, *service.AuthSession, error) {
	var row struct {
		ID              string    `db:"id"`
		Username        string    `db:"username"`
		Admin           bool      `db:"admin"`
		Version         int64     `db:"version"`
		SessionID       string    `db:"session_id"`
		ExpiresAt       time.Time `db:"expires_at"`
		AccessExpiresAt time.Time `db:"access_expires_at"`
		Remember        bool      `db:"remember"`
		Transport       string    `db:"transport"`
	}
	found, err := p.goqu.From(p.tableAuthCredentials.As("c")).Join(p.tableAuthSessions.As("s"), goqu.On(goqu.I("s.hash").Eq(goqu.I("c.session_id")))).Join(p.tableAuthUsers.As("u"), goqu.On(goqu.I("u.id").Eq(goqu.I("s.user_id")), goqu.I("u.session_version").Eq(goqu.I("s.version")))).Select("u.id", "u.username", "u.admin", "s.version", "c.session_id", "s.expires_at", "s.remember", "s.transport", goqu.I("c.expires_at").As("access_expires_at")).Where(goqu.Ex{"c.hash": hash, "c.kind": "access", "u.disabled": false}, goqu.I("s.expires_at").Gt(goqu.L("clock_timestamp()")), goqu.I("c.expires_at").Gt(goqu.L("clock_timestamp()"))).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve auth access: %w", err)
	}
	if !found {
		return nil, nil, nil
	}
	return &service.AuthUser{ID: row.ID, Username: row.Username, Admin: row.Admin, SessionVersion: row.Version}, &service.AuthSession{Transport: row.Transport, Hash: row.SessionID, UserID: row.ID, Version: row.Version, ExpiresAt: row.ExpiresAt, AccessExpiresAt: row.AccessExpiresAt, Remember: row.Remember}, nil
}

// Lock the user before the family, matching password/passkey invalidation.
// Re-read the credential after locks: two contenders must see consumed=true.
func (p *Postgres) RotateAuthRefresh(ctx context.Context, hash, access, refresh string) (*service.AuthUser, *service.AuthSession, error) {
	return p.RotateAuthRefreshTransport(ctx, hash, access, refresh, "web")
}

func (p *Postgres) RotateAuthRefreshTransport(ctx context.Context, hash, access, refresh, transport string) (*service.AuthUser, *service.AuthSession, error) {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("begin auth rotation: %w", err)
	}
	defer tx.Rollback()
	var candidate struct {
		SessionID string `db:"session_id"`
		UserID    string `db:"user_id"`
	}
	found, err := tx.From(p.tableAuthCredentials.As("c")).Join(p.tableAuthSessions.As("s"), goqu.On(goqu.I("s.hash").Eq(goqu.I("c.session_id")))).Select("c.session_id", "s.user_id").Where(goqu.Ex{"c.hash": hash, "c.kind": "refresh"}).ScanStructContext(ctx, &candidate)
	if err != nil {
		return nil, nil, fmt.Errorf("find refresh family: %w", err)
	}
	if !found {
		return nil, nil, nil
	}
	var user authUserRow
	found, err = tx.From(p.tableAuthUsers).Select("id", "username", "admin", "disabled", "session_version").Where(goqu.Ex{"id": candidate.UserID}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &user)
	if err != nil {
		return nil, nil, fmt.Errorf("lock refresh user: %w", err)
	}
	if !found || user.Disabled {
		return nil, nil, nil
	}
	var family struct {
		Version      int64     `db:"version"`
		ExpiresAt    time.Time `db:"expires_at"`
		Remember     bool      `db:"remember"`
		RefreshAfter time.Time `db:"refresh_after"`
		Transport    string    `db:"transport"`
	}
	found, err = tx.From(p.tableAuthSessions).Select("version", "expires_at", "remember", "refresh_after", "transport").Where(goqu.Ex{"hash": candidate.SessionID}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &family)
	if err != nil {
		return nil, nil, fmt.Errorf("lock refresh family: %w", err)
	}
	if !found || family.Version != user.SessionVersion {
		return nil, nil, nil
	}
	if family.Transport != transport {
		return nil, nil, nil
	}
	var consumed bool
	found, err = tx.From(p.tableAuthCredentials).Select("consumed").Where(goqu.Ex{"hash": hash, "kind": "refresh", "session_id": candidate.SessionID}).ScanValContext(ctx, &consumed)
	if err != nil {
		return nil, nil, fmt.Errorf("read refresh credential: %w", err)
	}
	if !found {
		return nil, nil, nil
	}
	if consumed {
		// Deleting the entire family is safe: all future presentations are unknown.
		if _, err = tx.Delete(p.tableAuthSessions).Where(goqu.Ex{"hash": candidate.SessionID}).Executor().ExecContext(ctx); err != nil {
			return nil, nil, fmt.Errorf("revoke replayed family: %w", err)
		}
		if err = tx.Commit(); err != nil {
			return nil, nil, fmt.Errorf("commit replay revocation: %w", err)
		}
		return nil, nil, nil
	}
	if err = checkAuthAdmissionDeadline(ctx, tx, family.ExpiresAt); err != nil {
		if err == service.ErrAuthConflict {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	var now time.Time
	if _, err = tx.Select(goqu.L("clock_timestamp()")).ScanValContext(ctx, &now); err != nil {
		return nil, nil, fmt.Errorf("read rotation time: %w", err)
	}
	// Check replay first: a rate limit must never become a replay grace window.
	// This persisted guard bounds tombstone growth across every replica.
	if now.Before(family.RefreshAfter) {
		return nil, nil, &service.AuthRefreshEarlyError{RetryAfter: family.RefreshAfter.Sub(now)}
	}
	if _, err = tx.Update(p.tableAuthSessions).Set(goqu.Record{"refresh_after": now.Add(5 * time.Minute)}).Where(goqu.Ex{"hash": candidate.SessionID}).Executor().ExecContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("set refresh interval: %w", err)
	}
	s := &service.AuthSession{Hash: candidate.SessionID, UserID: user.ID, Version: family.Version, ExpiresAt: family.ExpiresAt, Remember: family.Remember, AccessHash: access, RefreshHash: refresh, AccessExpiresAt: now.Add(10 * time.Minute)}
	s.Transport = transport
	if s.AccessExpiresAt.After(s.ExpiresAt) {
		s.AccessExpiresAt = s.ExpiresAt
	}
	if _, err = tx.Update(p.tableAuthCredentials).Set(goqu.Record{"consumed": true}).Where(goqu.Ex{"hash": hash}).Executor().ExecContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("consume refresh: %w", err)
	}
	if _, err = tx.Delete(p.tableAuthCredentials).Where(goqu.Ex{"session_id": s.Hash, "kind": "access"}).Executor().ExecContext(ctx); err != nil {
		return nil, nil, fmt.Errorf("retire access: %w", err)
	}
	if err = p.insertAuthCredentials(ctx, tx, *s); err != nil {
		return nil, nil, err
	}
	if err = checkAuthAdmissionDeadline(ctx, tx, s.ExpiresAt); err != nil {
		if err == service.ErrAuthConflict {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit auth rotation: %w", err)
	}
	return authUserRowToRecord(user), s, nil
}

func (p *Postgres) RevokeAuthCredential(ctx context.Context, hash string) error {
	return p.RevokeAuthCredentialTransport(ctx, hash, "web", "")
}

func (p *Postgres) RevokeAuthCredentialTransport(ctx context.Context, hash, transport, kind string) error {
	credentials := p.goqu.From(p.tableAuthCredentials).Select("session_id").Where(goqu.Ex{"hash": hash})
	if kind != "" {
		credentials = credentials.Where(goqu.Ex{"kind": kind})
	}
	_, err := p.goqu.Delete(p.tableAuthSessions).Where(goqu.Ex{"transport": transport}, goqu.I("hash").In(credentials)).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("revoke auth credential: %w", err)
	}
	return nil
}

// Delete credentials before families so even a very large family's cascade is
// bounded. Refresh tombstones expire only at the immutable family deadline.
func (p *Postgres) CleanupAuthCredentials(ctx context.Context, limit uint) (int64, error) {
	if limit == 0 || limit > 1000 {
		return 0, fmt.Errorf("auth cleanup limit must be 1-1000")
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("begin auth cleanup: %w", err)
	}
	defer tx.Rollback()
	selected := tx.From(p.tableAuthCredentials).Select("hash").Where(goqu.I("expires_at").Lte(goqu.L("clock_timestamp()"))).Order(goqu.I("expires_at").Asc()).Limit(limit).ForUpdate(goqu.SkipLocked)
	result, err := tx.Delete(p.tableAuthCredentials).Where(goqu.I("hash").In(selected)).Executor().ExecContext(ctx)
	if err != nil {
		return 0, fmt.Errorf("clean auth credentials: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count auth cleanup: %w", err)
	}
	families := tx.From(p.tableAuthSessions).Select("hash").Where(goqu.I("expires_at").Lte(goqu.L("clock_timestamp()")), goqu.L("NOT EXISTS ?", tx.From(p.tableAuthCredentials).Select(goqu.L("1")).Where(goqu.I("session_id").Eq(p.tableAuthSessions.Col("hash"))))).Limit(limit).ForUpdate(goqu.SkipLocked)
	if _, err = tx.Delete(p.tableAuthSessions).Where(goqu.I("hash").In(families)).Executor().ExecContext(ctx); err != nil {
		return 0, fmt.Errorf("clean auth families: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit auth cleanup: %w", err)
	}
	return n, nil
}
