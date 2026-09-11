package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/oklog/ulid/v2"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthSecurityStorer = (*Postgres)(nil)

// A bounded durable source pool stops replica hopping. Only the socket peer is
// trusted; deployments behind a proxy deliberately share that proxy's budget.
func (p *Postgres) AdmitAuthSecuritySource(ctx context.Context, hash string) (bool, error) {
	if len(hash) != 64 {
		return false, service.ErrAuthConflict
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin security admission: %w", err)
	}
	defer tx.Rollback()
	var claimed bool
	if _, err := tx.From(p.tableAuthBootstrap).Select("claimed").ForUpdate(goqu.Wait).ScanValContext(ctx, &claimed); err != nil {
		return false, err
	}
	table := p.authSecurityTable("auth_security_sources")
	if _, err := tx.Delete(table).Where(goqu.I("expires_at").Lte(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx); err != nil {
		return false, err
	}
	var attempts int
	found, err := tx.From(table).Select("attempts").Where(goqu.Ex{"hash": hash}).ScanValContext(ctx, &attempts)
	if err != nil {
		return false, err
	}
	if !found {
		count, err := tx.From(table).CountContext(ctx)
		if err != nil {
			return false, err
		}
		if count >= 4096 {
			return false, nil
		}
		if _, err := tx.Insert(table).Rows(goqu.Record{"hash": hash, "attempts": 1, "expires_at": goqu.L("clock_timestamp() + interval '5 minutes'")}).Executor().ExecContext(ctx); err != nil {
			return false, err
		}
	} else if attempts < 120 {
		if _, err := tx.Update(table).Set(goqu.Record{"attempts": goqu.L("attempts + 1")}).Where(goqu.Ex{"hash": hash}).Executor().ExecContext(ctx); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return attempts < 120, nil
}

func (p *Postgres) authSecurityTable(name string) exp.IdentifierExpression {
	return goqu.T(strings.TrimSuffix(fmt.Sprint(p.tableAuthUsers.GetTable()), "auth_users") + name)
}

func transformAuthSecrets(s *service.AuthSecurityState, key []byte, encrypt bool) error {
	apply := func(v *string) error {
		if *v == "" {
			return nil
		}
		if len(key) != 32 {
			return fmt.Errorf("account security requires encryption")
		}
		var err error
		if encrypt {
			*v, err = atcrypto.Encrypt(*v, key)
		} else {
			if !atcrypto.IsEncrypted(*v) {
				return fmt.Errorf("unencrypted account factor")
			}
			*v, err = atcrypto.Decrypt(*v, key)
		}
		return err
	}
	if err := apply(&s.Secret); err != nil {
		return err
	}
	for i := range s.Transactions {
		if err := apply(&s.Transactions[i].Secret); err != nil {
			return err
		}
	}
	return nil
}

// UpdateAuthSecurity serializes all security operations with ordinary issuance,
// refresh, disable and password changes by taking the existing user lock first.
// sessionID/version are mandatory for self actions; empty/-1 is reserved for
// primary-login and recovery code that validates its own version-bound token.
func (p *Postgres) UpdateAuthSecurity(ctx context.Context, userID, sessionID string, version int64, fn func(*service.AuthSecurityUpdate) error) error {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin account security: %w", err)
	}
	defer tx.Rollback()
	var row authUserRow
	if completion, ok := service.AuthExternalCompletionFromContext(ctx); ok {
		if err := p.CheckAuthExternalAdmission(ctx, tx, completion, userID); err != nil {
			return err
		}
	}
	actor, hasActor := ctx.Value(authRecoveryActorKey{}).(authRecoveryActor)
	if hasActor {
		// Deterministic ordering prevents two admins recovering each other from
		// deadlocking. Ordinary auth operations lock only one account.
		var ids []string
		if err := tx.From(p.tableAuthUsers).Select("id").Where(goqu.I("id").In(actor.ID, userID)).Order(goqu.I("id").Asc()).ForUpdate(goqu.Wait).ScanValsContext(ctx, &ids); err != nil {
			return fmt.Errorf("lock recovery accounts: %w", err)
		}
	}
	found, err := tx.From(p.tableAuthUsers).Select("id", "username", "password_hash", "admin", "disabled", "session_version").Where(goqu.Ex{"id": userID}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return fmt.Errorf("lock account security: %w", err)
	}
	if !found || (version >= 0 && (row.SessionVersion != version || row.Disabled)) {
		return service.ErrAuthConflict
	}
	u := service.AuthSecurityUpdate{User: *authUserRowToRecord(row)}
	if completion, ok := service.AuthExternalCompletionFromContext(ctx); ok {
		u.Deadline = completion.Deadline
	}
	if _, err := tx.Select(goqu.L("clock_timestamp()")).ScanValContext(ctx, &u.Now); err != nil {
		return fmt.Errorf("read security clock: %w", err)
	}
	if hasActor {
		u.Deadline, err = p.consumeRecoveryActor(ctx, tx, actor, u.Now)
		if err != nil {
			return err
		}
	}
	if sessionID != "" {
		var id string
		ok, err := tx.From(p.tableAuthSessions).Select("hash").Where(goqu.Ex{"hash": sessionID, "user_id": userID, "version": row.SessionVersion, "transport": "web"}, goqu.I("expires_at").Gt(u.Now)).ScanValContext(ctx, &id)
		if err != nil {
			return fmt.Errorf("check security session: %w", err)
		}
		if !ok || row.Disabled {
			return service.ErrAuthConflict
		}
	}
	var blob []byte
	found, err = tx.From(p.authSecurityTable("auth_security")).Select("data").Where(goqu.Ex{"user_id": userID}).ScanValContext(ctx, &blob)
	if err != nil {
		return fmt.Errorf("read security state: %w", err)
	}
	if found {
		if err := json.Unmarshal(blob, &u.State); err != nil {
			return fmt.Errorf("decode security state: %w", err)
		}
		if err := transformAuthSecrets(&u.State, p.encKey, false); err != nil {
			return fmt.Errorf("decrypt security state: %w", err)
		}
	}
	live := u.State.Transactions[:0]
	for _, c := range u.State.Transactions {
		if c.Expires.After(u.Now) && c.Version == row.SessionVersion {
			live = append(live, c)
		}
	}
	u.State.Transactions = live
	if err := fn(&u); err != nil {
		return err
	}
	if len(u.State.Transactions) > 20 {
		return service.ErrAuthConflict
	}
	if u.Revoke || u.ResetPassword != "" || u.PasswordHash != "" {
		values := goqu.Record{"session_version": goqu.L("session_version + 1")}
		if u.PasswordHash != "" {
			values["password_hash"] = u.PasswordHash
		}
		if u.ResetPassword != "" {
			values["password_hash"] = u.ResetPassword
			u.State = service.AuthSecurityState{}
		}
		if _, err := tx.Update(p.tableAuthUsers).Set(values).Where(goqu.Ex{"id": userID}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("invalidate security version: %w", err)
		}
		for _, table := range []string{"auth_sessions", "auth_challenges"} {
			if _, err := tx.Delete(p.authSecurityTable(table)).Where(goqu.Ex{"user_id": userID}).Executor().ExecContext(ctx); err != nil {
				return fmt.Errorf("revoke security state: %w", err)
			}
		}
		u.State.Transactions = nil
		if u.ResetPassword != "" {
			if _, err := tx.Delete(p.tableAuthPasskeys).Where(goqu.Ex{"user_id": userID}).Executor().ExecContext(ctx); err != nil {
				return fmt.Errorf("reset passkeys: %w", err)
			}
			// The SSO tables are additive and may not yet be installed. Once
			// present they participate in this same credential replacement.
			for _, name := range []string{"auth_identity_links", "auth_external_flows"} {
				var exists bool
				if _, err := tx.Select(goqu.L("to_regclass(?) IS NOT NULL", fmt.Sprint(p.authSecurityTable(name).GetTable()))).ScanValContext(ctx, &exists); err != nil {
					return fmt.Errorf("inspect identity reset table: %w", err)
				}
				if exists {
					if _, err := tx.Delete(p.authSecurityTable(name)).Where(goqu.Ex{"user_id": userID}).Executor().ExecContext(ctx); err != nil {
						return fmt.Errorf("reset external identity: %w", err)
					}
				}
			}
		}
	}
	if u.Session != nil {
		if u.Revoke || u.User.Disabled || u.Session.UserID != userID || u.Session.Version != row.SessionVersion {
			return service.ErrAuthConflict
		}
		if err := p.insertAuthSession(ctx, tx, *u.Session); err != nil {
			return err
		}
	}
	if err := transformAuthSecrets(&u.State, p.encKey, true); err != nil {
		return fmt.Errorf("encrypt security state: %w", err)
	}
	blob, err = json.Marshal(u.State)
	if err != nil {
		return fmt.Errorf("encode security state: %w", err)
	}
	if _, err := tx.Insert(p.authSecurityTable("auth_security")).Rows(goqu.Record{"user_id": userID, "data": goqu.L("?::jsonb", string(blob))}).OnConflict(goqu.DoUpdate("user_id", goqu.Record{"data": goqu.L("EXCLUDED.data")})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("write security state: %w", err)
	}
	if u.RecoveryAction != "" {
		if _, err := tx.Insert(p.authSecurityTable("auth_recovery_events")).Rows(goqu.Record{"id": ulid.Make().String(), "user_id": userID, "source": u.RecoverySource, "action": u.RecoveryAction}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("record recovery event: %w", err)
		}
	}
	if err := checkAuthAdmissionDeadline(ctx, tx, u.Deadline); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account security: %w", err)
	}
	return nil
}

// rotateAuthSecurityKey is called inside RotateEncryptionKey while encKeyMu is
// held, before committing the shared SQL transaction. Disabling encryption is
// refused while an active or pending TOTP secret exists.
func (p *Postgres) rotateAuthSecurityKey(ctx context.Context, tx *sql.Tx, oldKey, newKey []byte) error {
	q, _, err := p.goqu.From(p.authSecurityTable("auth_security")).Select("user_id", "data").ForUpdate(goqu.Wait).ToSQL()
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return fmt.Errorf("read security rotation: %w", err)
	}
	type record struct {
		id   string
		data []byte
	}
	var records []record
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.id, &r.data); err != nil {
			rows.Close()
			return err
		}
		records = append(records, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range records {
		var s service.AuthSecurityState
		if err := json.Unmarshal(r.data, &s); err != nil {
			return err
		}
		if err := transformAuthSecrets(&s, oldKey, false); err != nil {
			return err
		}
		if err := transformAuthSecrets(&s, newKey, true); err != nil {
			return err
		}
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		q, args, err := p.goqu.Update(p.authSecurityTable("auth_security")).Set(goqu.Record{"data": goqu.L("?::jsonb", string(b))}).Where(goqu.Ex{"user_id": r.id}).Prepared(true).ToSQL()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, q, args...); err != nil {
			return err
		}
	}
	return nil
}
