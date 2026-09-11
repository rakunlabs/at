package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthStorer = (*Postgres)(nil)

type authUserRow struct {
	ID             string `db:"id"`
	Username       string `db:"username"`
	PasswordHash   string `db:"password_hash"`
	Admin          bool   `db:"admin"`
	Disabled       bool   `db:"disabled"`
	SessionVersion int64  `db:"session_version"`
}

func authUserRowToRecord(r authUserRow) *service.AuthUser {
	return &service.AuthUser{ID: r.ID, Username: r.Username, PasswordHash: r.PasswordHash, Admin: r.Admin, Disabled: r.Disabled, SessionVersion: r.SessionVersion}
}

func (p *Postgres) GetAuthUser(ctx context.Context, username string) (*service.AuthUser, error) {
	var row authUserRow
	found, err := p.goqu.From(p.tableAuthUsers).Select("id", "username", "password_hash", "admin", "disabled", "session_version").Where(goqu.Ex{"username": username}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get auth user: %w", err)
	}
	if !found {
		return nil, nil
	}
	return authUserRowToRecord(row), nil
}

func (p *Postgres) GetAuthUserByID(ctx context.Context, id string) (*service.AuthUser, error) {
	var row authUserRow
	found, err := p.goqu.From(p.tableAuthUsers).Select("id", "username", "password_hash", "admin", "disabled", "session_version").Where(goqu.Ex{"id": id}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get auth user by ID: %w", err)
	}
	if !found {
		return nil, nil
	}
	return authUserRowToRecord(row), nil
}

func (p *Postgres) ListAuthUsers(ctx context.Context, after string, limit uint) ([]service.AuthUser, error) {
	if limit == 0 || limit > 101 {
		return nil, fmt.Errorf("list auth users: limit must be 1-101")
	}
	var rows []authUserRow
	err := p.goqu.From(p.tableAuthUsers).Select("id", "username", "admin", "disabled").Where(goqu.I("id").Gt(after)).Order(goqu.I("id").Asc()).Limit(limit).ScanStructsContext(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list auth users: %w", err)
	}
	users := make([]service.AuthUser, 0, len(rows))
	for _, row := range rows {
		users = append(users, *authUserRowToRecord(row))
	}
	return users, nil
}

func (p *Postgres) EnableAuthUser(ctx context.Context, id string) (bool, error) {
	return p.updateAuthUserCredentials(ctx, id, goqu.Record{"disabled": false}, nil)
}

func (p *Postgres) SetAuthUserPassword(ctx context.Context, id, hash string, version *int64) (bool, error) {
	if hash == "" {
		return false, fmt.Errorf("set auth user password: empty hash")
	}
	return p.updateAuthUserCredentials(ctx, id, goqu.Record{"password_hash": hash}, version)
}

func (p *Postgres) updateAuthUserCredentials(ctx context.Context, id string, values goqu.Record, version *int64) (bool, error) {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin auth credential update: %w", err)
	}
	defer tx.Rollback()
	where := goqu.Ex{"id": id}
	if version != nil {
		// Compare against the version whose current password was verified. The
		// UPDATE row lock also serializes this operation with session issuance.
		where["session_version"] = *version
		where["disabled"] = false
	}
	values["session_version"] = goqu.L("session_version + 1")
	result, err := tx.Update(p.tableAuthUsers).Set(values).Where(where).Executor().ExecContext(ctx)
	if err != nil {
		return false, fmt.Errorf("update auth credentials: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("update auth credential rows: %w", err)
	}
	if n == 0 {
		if version != nil {
			return false, service.ErrAuthConflict
		}
		return false, nil
	}
	if _, err := tx.Delete(p.tableAuthSessions).Where(goqu.Ex{"user_id": id}).Executor().ExecContext(ctx); err != nil {
		return false, fmt.Errorf("revoke auth credential sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit auth credential update: %w", err)
	}
	return true, nil
}

func (p *Postgres) CreateAuthUser(ctx context.Context, u service.AuthUser, bootstrap bool) (*service.AuthUser, error) {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin auth user: %w", err)
	}
	defer tx.Rollback()
	if bootstrap {
		result, err := tx.Update(p.tableAuthBootstrap).Set(goqu.Record{"claimed": true}).Where(goqu.Ex{"singleton": true, "claimed": false}).Executor().ExecContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("claim auth bootstrap: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("claim auth bootstrap rows: %w", err)
		}
		if n != 1 {
			return nil, service.ErrAuthConflict
		}
		u.Admin = true
	}
	u.ID = ulid.Make().String()
	_, err = tx.Insert(p.tableAuthUsers).Rows(goqu.Record{"id": u.ID, "username": u.Username, "password_hash": u.PasswordHash, "admin": u.Admin}).Executor().ExecContext(ctx)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return nil, service.ErrAuthConflict
		}
		return nil, fmt.Errorf("create auth user: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit auth user: %w", err)
	}
	return p.GetAuthUser(ctx, u.Username)
}

func (p *Postgres) CreateAuthSession(ctx context.Context, s service.AuthSession) error {
	// Serialize issuance with revocation/disable. A login verified before a
	// revocation must not be able to mint a session after it with a new version.
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin auth session: %w", err)
	}
	defer tx.Rollback()
	var id string
	found, err := tx.From(p.tableAuthUsers).Select("id").Where(goqu.Ex{"id": s.UserID, "disabled": false, "session_version": s.Version}).ForUpdate(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("lock auth session user: %w", err)
	}
	if !found {
		return service.ErrAuthConflict
	}
	if err := p.insertAuthSession(ctx, tx, s); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit auth session: %w", err)
	}
	return nil
}

// Caller holds the user lock, including during mobile code redemption.
func (p *Postgres) insertAuthSession(ctx context.Context, tx *goqu.TxDatabase, s service.AuthSession) error {
	if err := checkAuthAdmissionDeadline(ctx, tx, s.AdmissionDeadline); err != nil {
		return err
	}
	// User lock makes the cap replica-safe without deleting replay tombstones.
	count, err := tx.From(p.tableAuthSessions).Where(goqu.Ex{"user_id": s.UserID}, goqu.I("expires_at").Gt(goqu.L("clock_timestamp()"))).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count live auth sessions: %w", err)
	}
	if count >= 20 {
		return service.ErrAuthSessionLimit
	}
	if s.Transport == "" {
		s.Transport = "web"
	}
	_, err = tx.Insert(p.tableAuthSessions).Rows(goqu.Record{"hash": s.Hash, "user_id": s.UserID, "version": s.Version, "expires_at": s.ExpiresAt, "remember": s.Remember, "transport": s.Transport}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("create auth session: %w", err)
	}
	if s.AccessHash != "" && s.RefreshHash != "" {
		if err := p.insertAuthCredentials(ctx, tx, s); err != nil {
			return err
		}
	}
	// External provenance commits with the family so a disabled provider or a
	// removed link cannot leave an orphaned externally authenticated session.
	if err := p.recordAuthExternalSession(ctx, tx, s); err != nil {
		return err
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, s.ExpiresAt); err != nil {
		return err
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, s.AdmissionDeadline); err != nil {
		return err
	}
	return nil
}

// Use database wall time after locks, not transaction-start CURRENT_TIMESTAMP
// or only a context timeout. A zero deadline is ordinary password admission.
func checkAuthAdmissionDeadline(ctx context.Context, tx *goqu.TxDatabase, deadline time.Time) error {
	if deadline.IsZero() {
		return nil
	}
	var valid bool
	if _, err := tx.Select(goqu.L("clock_timestamp() < ?::timestamptz", deadline)).Prepared(true).ScanValContext(ctx, &valid); err != nil {
		return fmt.Errorf("check auth admission deadline: %w", err)
	}
	if !valid {
		return service.ErrAuthConflict
	}
	return nil
}

func (p *Postgres) ResolveAuthSession(ctx context.Context, hash string) (*service.AuthUser, time.Time, error) {
	u, s := p.tableAuthUsers.As("u"), p.tableAuthSessions.As("s")
	query, args, err := p.goqu.From(s).Join(u, goqu.On(goqu.I("u.id").Eq(goqu.I("s.user_id")), goqu.I("u.session_version").Eq(goqu.I("s.version")))).Select("u.id", "u.username", "u.admin", "u.session_version", "s.expires_at").Where(goqu.Ex{"s.hash": hash, "u.disabled": false}, goqu.I("s.expires_at").Gt(goqu.L("CURRENT_TIMESTAMP"))).Prepared(true).ToSQL()
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("build resolve auth session: %w", err)
	}
	var user service.AuthUser
	var expires time.Time
	err = p.db.QueryRowContext(ctx, query, args...).Scan(&user.ID, &user.Username, &user.Admin, &user.SessionVersion, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("resolve auth session: %w", err)
	}
	return &user, expires, nil
}

func (p *Postgres) DeleteAuthSession(ctx context.Context, hash string) error {
	_, err := p.goqu.Delete(p.tableAuthSessions).Where(goqu.Ex{"hash": hash}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("delete auth session: %w", err)
	}
	return nil
}

func (p *Postgres) InvalidateAuthUser(ctx context.Context, id string, disable bool) (bool, error) {
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin invalidate auth user: %w", err)
	}
	defer tx.Rollback()
	values := goqu.Record{"session_version": goqu.L("session_version + 1")}
	if disable {
		// Serialize administrator disables across replicas to prevent two
		// administrators concurrently disabling each other and locking out AT.
		var claimed bool
		if _, err := tx.From(p.tableAuthBootstrap).Select("claimed").ForUpdate(goqu.Wait).ScanValContext(ctx, &claimed); err != nil {
			return false, fmt.Errorf("lock auth administration: %w", err)
		}
		var target authUserRow
		found, err := tx.From(p.tableAuthUsers).Select("admin", "disabled").Where(goqu.Ex{"id": id}).ScanStructContext(ctx, &target)
		if err != nil {
			return false, fmt.Errorf("get disable target: %w", err)
		}
		if found && target.Admin && !target.Disabled {
			count, err := tx.From(p.tableAuthUsers).Where(goqu.Ex{"admin": true, "disabled": false}).CountContext(ctx)
			if err != nil {
				return false, fmt.Errorf("count active administrators: %w", err)
			}
			if count <= 1 {
				return false, service.ErrAuthConflict
			}
		}
		values["disabled"] = true
	}
	result, err := tx.Update(p.tableAuthUsers).Set(values).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx)
	if err != nil {
		return false, fmt.Errorf("invalidate auth user: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("invalidate auth user rows: %w", err)
	}
	if _, err := tx.Delete(p.tableAuthSessions).Where(goqu.Ex{"user_id": id}).Executor().ExecContext(ctx); err != nil {
		return false, fmt.Errorf("revoke auth user sessions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit invalidate auth user: %w", err)
	}
	return n != 0, nil
}
