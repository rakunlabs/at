package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/ada/middleware/auth/strategy/passkey"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthPasskeyStorer = (*Postgres)(nil)

func (p *Postgres) ListAuthPasskeys(ctx context.Context, userID string) ([]service.AuthPasskey, error) {
	var rows []struct {
		ID         string     `db:"id"`
		Name       string     `db:"name"`
		Credential []byte     `db:"credential"`
		SignCount  uint32     `db:"sign_count"`
		CreatedAt  time.Time  `db:"created_at"`
		LastUsedAt *time.Time `db:"last_used_at"`
	}
	if err := p.goqu.From(p.tableAuthPasskeys).Select("id", "name", "credential", "sign_count", "created_at", "last_used_at").Where(goqu.Ex{"user_id": userID}).Order(goqu.I("id").Asc()).Limit(20).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list auth passkeys: %w", err)
	}
	result := make([]service.AuthPasskey, 0, len(rows))
	for _, row := range rows {
		key := service.AuthPasskey{ID: row.ID, Name: row.Name, UserID: userID, CreatedAt: row.CreatedAt, LastUsedAt: row.LastUsedAt}
		if err := json.Unmarshal(row.Credential, &key.Credential); err != nil {
			return nil, fmt.Errorf("decode auth passkey: %w", err)
		}
		key.Credential.SignCount = row.SignCount
		result = append(result, key)
	}
	return result, nil
}

func (p *Postgres) SaveAuthChallenge(ctx context.Context, c service.AuthChallenge) error {
	if c.Purpose != "login" && c.Purpose != "enroll" {
		return service.ErrAuthConflict
	}
	if c.Data.Expires.IsZero() || !c.Data.Expires.After(time.Now()) || c.Data.Expires.After(time.Now().Add(5*time.Minute)) {
		return service.ErrAuthConflict
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin auth challenge: %w", err)
	}
	defer tx.Rollback()
	// One database lock bounds the pool across replicas, not just this process.
	var claimed bool
	if _, err := tx.From(p.tableAuthBootstrap).Select("claimed").ForUpdate(goqu.Wait).ScanValContext(ctx, &claimed); err != nil {
		return fmt.Errorf("lock auth challenge pool: %w", err)
	}
	if _, err := tx.Delete(p.tableAuthChallenges).Where(goqu.I("expires_at").Lte(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("prune auth challenges: %w", err)
	}
	count, err := tx.From(p.tableAuthChallenges).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count auth challenges: %w", err)
	}
	if count >= 4096 {
		return service.ErrAuthConflict
	}
	// Anonymous callers cannot spend a victim's enrollment quota or reserve
	// every global slot. Only authenticated enrollment has a per-user limit.
	pool := tx.From(p.tableAuthChallenges).Where(goqu.L("data->>'Purpose' = ?", c.Purpose))
	limit := int64(3840)
	if c.Purpose == "enroll" {
		pool = pool.Where(goqu.Ex{"user_id": c.UserID})
		limit = 5
	}
	count, err = pool.CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count auth challenge pool: %w", err)
	}
	if count >= limit {
		return service.ErrAuthConflict
	}
	blob, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode auth challenge: %w", err)
	}
	if _, err := tx.Insert(p.tableAuthChallenges).Rows(goqu.Record{"hash": c.Hash, "user_id": c.UserID, "expires_at": c.Data.Expires, "data": goqu.L("?::jsonb", string(blob))}).Prepared(true).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("save auth challenge: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit auth challenge: %w", err)
	}
	return nil
}

func (p *Postgres) ConsumeAuthChallenge(ctx context.Context, hash string) (*service.AuthChallenge, error) {
	// DELETE RETURNING is one atomic consume, including expired/invalid attempts.
	query, args, err := p.goqu.Delete(p.tableAuthChallenges).Where(goqu.Ex{"hash": hash}).Returning("data", "expires_at").Prepared(true).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build consume auth challenge: %w", err)
	}
	var blob []byte
	var expires time.Time
	if err := p.db.QueryRowContext(ctx, query, args...).Scan(&blob, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("consume auth challenge: %w", err)
	}
	var c service.AuthChallenge
	if err := json.Unmarshal(blob, &c); err != nil {
		return nil, fmt.Errorf("decode auth challenge: %w", err)
	}
	if !expires.After(time.Now()) || !c.Data.Expires.After(time.Now()) {
		return nil, nil
	}
	return &c, nil
}

// User-first locking matches session issuance and credential revocation. Holding
// the enrollment session row also serializes completion against logout.
func (p *Postgres) passkeyTransaction(ctx context.Context, userID string, version int64, sessionHash string, deadline time.Time, fn func(*goqu.TxDatabase) error) error {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin auth passkey: %w", err)
	}
	defer tx.Rollback()
	var id string
	found, err := tx.From(p.tableAuthUsers).Select("id").Where(goqu.Ex{"id": userID, "session_version": version, "disabled": false}).ForUpdate(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("lock auth passkey user: %w", err)
	}
	if !found {
		return service.ErrAuthConflict
	}
	if sessionHash != "" {
		var live struct {
			ExpiresAt time.Time `db:"expires_at"`
		}
		found, err = tx.From(p.tableAuthSessions).Select("expires_at").Where(goqu.Ex{"hash": sessionHash, "user_id": userID, "version": version}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &live)
		if err != nil {
			return fmt.Errorf("lock auth passkey session: %w", err)
		}
		if !found {
			return service.ErrAuthConflict
		}
		if deadline.IsZero() || live.ExpiresAt.Before(deadline) {
			deadline = live.ExpiresAt
		}
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, deadline); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return fmt.Errorf("update auth passkey: %w", err)
	}
	// The credential write can also wait for a lock. Expiration must roll it
	// back rather than commit a stale counter update or enrollment.
	if err := checkAuthAdmissionDeadline(ctx, tx, deadline); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit auth passkey: %w", err)
	}
	return nil
}

func (p *Postgres) CreateAuthPasskey(ctx context.Context, c service.AuthChallenge, key passkey.Credential) error {
	if c.Purpose != "enroll" || c.SessionHash == "" || string(key.UserHandle) != c.UserID || !c.Data.Expires.After(time.Now()) {
		return service.ErrAuthConflict
	}
	return p.passkeyTransaction(ctx, c.UserID, c.Version, c.SessionHash, c.Data.Expires, func(tx *goqu.TxDatabase) error {
		count, err := tx.From(p.tableAuthPasskeys).Where(goqu.Ex{"user_id": c.UserID}).CountContext(ctx)
		if err != nil {
			return err
		}
		if count >= 20 {
			return service.ErrAuthConflict
		}
		blob, err := json.Marshal(key)
		if err != nil {
			return err
		}
		// BYTEA must be bound as bytes, never interpolated as a UTF-8 literal.
		_, err = tx.Insert(p.tableAuthPasskeys).Rows(goqu.Record{"id": ulid.Make().String(), "user_id": c.UserID, "name": c.Name, "credential_id": key.ID, "credential": goqu.L("?::jsonb", string(blob)), "sign_count": int64(key.SignCount)}).Prepared(true).Executor().ExecContext(ctx)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return service.ErrAuthConflict
		}
		return err
	})
}

func (p *Postgres) AdvanceAuthPasskey(ctx context.Context, userID, id string, version int64, old, next uint32, deadline time.Time) error {
	if deadline.IsZero() || (old != 0 || next != 0) && next <= old {
		return service.ErrAuthConflict
	}
	return p.passkeyTransaction(ctx, userID, version, "", deadline, func(tx *goqu.TxDatabase) error {
		result, err := tx.Update(p.tableAuthPasskeys).Set(goqu.Record{"sign_count": int64(next), "last_used_at": time.Now().UTC()}).Where(goqu.Ex{"id": id, "user_id": userID, "sign_count": int64(old)}).Executor().ExecContext(ctx)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return service.ErrAuthConflict
		}
		return nil
	})
}

func (p *Postgres) DeleteAuthPasskey(ctx context.Context, userID, id string, version int64, sessionHash string) error {
	return p.passkeyTransaction(ctx, userID, version, sessionHash, time.Time{}, func(tx *goqu.TxDatabase) error {
		result, err := tx.Delete(p.tableAuthPasskeys).Where(goqu.Ex{"id": id, "user_id": userID}).Executor().ExecContext(ctx)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return service.ErrAuthConflict
		}
		if _, err := tx.Update(p.tableAuthUsers).Set(goqu.Record{"session_version": goqu.L("session_version + 1")}).Where(goqu.Ex{"id": userID}).Executor().ExecContext(ctx); err != nil {
			return err
		}
		_, err = tx.Delete(p.tableAuthSessions).Where(goqu.Ex{"user_id": userID}).Executor().ExecContext(ctx)
		return err
	})
}
