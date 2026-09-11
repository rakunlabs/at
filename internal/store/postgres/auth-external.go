package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"
	"github.com/oklog/ulid/v2"

	atcrypto "github.com/rakunlabs/at/internal/crypto"
	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthExternalStorer = (*Postgres)(nil)

// Derive schema/prefix from an initialized auth table, including isolated tests.
func (p *Postgres) externalTable(suffix string) exp.IdentifierExpression {
	name := p.tableAuthUsers.GetTable()
	if name == "" {
		name, _ = p.tableAuthUsers.GetCol().(string)
	}
	t := goqu.T(strings.TrimSuffix(name, "auth_users") + suffix)
	if schema := p.tableAuthUsers.GetSchema(); schema != "" {
		return t.Schema(schema)
	}
	return t
}

type externalProviderRow struct {
	ID      string `db:"id"`
	Version int64  `db:"version"`
	Enabled bool   `db:"enabled"`
	Config  string `db:"config"`
	Secret  string `db:"secret"`
}

func externalProviderRecord(row externalProviderRow, key []byte) (*service.AuthIdentityProvider, error) {
	var v service.AuthIdentityProvider
	if err := json.Unmarshal([]byte(row.Config), &v); err != nil {
		return nil, fmt.Errorf("decode identity provider: %w", err)
	}
	v.ID, v.Version, v.Enabled = row.ID, row.Version, row.Enabled
	v.HasClientSecret = row.Secret != ""
	if row.Secret != "" {
		if !atcrypto.IsEncrypted(row.Secret) {
			return nil, fmt.Errorf("identity provider secret is not encrypted")
		}
		var err error
		v.ClientSecret, err = atcrypto.Decrypt(row.Secret, key)
		if err != nil {
			return nil, fmt.Errorf("decrypt identity provider: %w", err)
		}
	}
	return &v, nil
}

func (p *Postgres) ListAuthIdentityProviders(ctx context.Context) ([]service.AuthIdentityProvider, error) {
	var rows []externalProviderRow
	if err := p.goqu.From(p.externalTable("auth_identity_providers")).Order(goqu.C("id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list identity providers: %w", err)
	}
	result := make([]service.AuthIdentityProvider, 0, len(rows))
	for _, row := range rows {
		// Lists never decrypt credentials, including installation administration.
		secret := row.Secret
		row.Secret = ""
		v, err := externalProviderRecord(row, nil)
		if err != nil {
			return nil, err
		}
		v.HasClientSecret = secret != ""
		result = append(result, *v)
	}
	return result, nil
}

func (p *Postgres) GetAuthIdentityProvider(ctx context.Context, id string) (*service.AuthIdentityProvider, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	var row externalProviderRow
	found, err := p.goqu.From(p.externalTable("auth_identity_providers")).Where(goqu.Ex{"id": id}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get identity provider: %w", err)
	}
	if !found {
		return nil, nil
	}
	return externalProviderRecord(row, p.encKey)
}

func (p *Postgres) SaveAuthIdentityProvider(ctx context.Context, v service.AuthIdentityProvider, secret *string) (*service.AuthIdentityProvider, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	if len(p.encKey) != 32 {
		return nil, fmt.Errorf("identity providers require encryption")
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("begin identity provider: %w", err)
	}
	defer tx.Rollback()
	// A common admission lock keeps all external operations in one lock order.
	if err := p.lockExternalAdmission(ctx, tx); err != nil {
		return nil, err
	}
	table := p.externalTable("auth_identity_providers")
	var row externalProviderRow
	create := v.ID == ""
	if create {
		v.ID = ulid.Make().String()
		v.Version = 1
	} else {
		found, e := tx.From(table).Where(goqu.Ex{"id": v.ID, "version": v.Version}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
		if e != nil {
			return nil, fmt.Errorf("lock identity provider: %w", e)
		}
		if !found {
			return nil, service.ErrAuthConflict
		}
		old, e := externalProviderRecord(row, p.encKey)
		if e != nil {
			return nil, e
		}
		if old.Issuer != v.Issuer || old.ClientID != v.ClientID || old.Mode != v.Mode || old.SubjectClaim != v.SubjectClaim {
			n, e := tx.From(p.externalTable("auth_identity_links")).Where(goqu.Ex{"provider_id": v.ID}).CountContext(ctx)
			if e != nil {
				return nil, fmt.Errorf("count provider links: %w", e)
			}
			if n > 0 {
				return nil, service.ErrAuthConflict
			}
		}
		v.Version++
	}
	sealed := row.Secret
	if secret != nil {
		sealed, err = atcrypto.Encrypt(*secret, p.encKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt identity provider: %w", err)
		}
	}
	v.ClientSecret = ""
	v.HasClientSecret = sealed != ""
	config, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode identity provider: %w", err)
	}
	values := goqu.Record{"version": v.Version, "enabled": v.Enabled, "config": string(config), "secret": sealed}
	if create {
		values["id"] = v.ID
		_, err = tx.Insert(table).Rows(values).Executor().ExecContext(ctx)
	} else {
		_, err = tx.Update(table).Set(values).Where(goqu.Ex{"id": v.ID}).Executor().ExecContext(ctx)
		if err == nil {
			users := tx.From(p.externalTable("auth_identity_links")).Select("user_id").Where(goqu.Ex{"provider_id": v.ID})
			_, err = tx.Update(p.tableAuthUsers).Set(goqu.Record{"session_version": goqu.L("session_version + 1")}).Where(goqu.C("id").In(users)).Executor().ExecContext(ctx)
			if err == nil {
				_, err = tx.Delete(p.tableAuthSessions).Where(goqu.C("user_id").In(users)).Executor().ExecContext(ctx)
			}
		}
	}
	if err != nil {
		return nil, fmt.Errorf("save identity provider: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit identity provider: %w", err)
	}
	return &v, nil
}

func (p *Postgres) lockExternalAdmission(ctx context.Context, tx *goqu.TxDatabase) error {
	var singleton bool
	found, err := tx.From(p.externalTable("auth_external_admission")).Select("singleton").ForUpdate(goqu.Wait).ScanValContext(ctx, &singleton)
	if err != nil {
		return fmt.Errorf("lock external admission: %w", err)
	}
	if !found {
		return service.ErrAuthConflict
	}
	return nil
}

func (p *Postgres) SaveAuthExternalFlow(ctx context.Context, f service.AuthExternalFlow) error {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	if len(p.encKey) != 32 {
		return fmt.Errorf("external flows require encryption")
	}
	if len(f.Hash) != 64 || len(f.Source) != 64 || len(f.Payload) > 16384 || !json.Valid(f.Payload) || f.ExpiresAt.After(time.Now().Add(7*time.Minute)) {
		return service.ErrAuthConflict
	}
	sealed, err := atcrypto.Encrypt(string(f.Payload), p.encKey)
	if err != nil {
		return fmt.Errorf("encrypt external flow: %w", err)
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin external flow: %w", err)
	}
	defer tx.Rollback()
	if err = p.lockExternalAdmission(ctx, tx); err != nil {
		return err
	}
	if err = p.admitExternalSource(ctx, tx, f.Source); err != nil {
		return err
	}
	table := p.externalTable("auth_external_flows")
	if _, err = tx.Delete(table).Where(goqu.C("expires_at").Lte(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("expire external flows: %w", err)
	}
	total, err := tx.From(table).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count external flows: %w", err)
	}
	if total >= 4096 {
		return service.ErrAuthSessionLimit
	}
	n, err := tx.From(table).Where(goqu.Ex{"source": f.Source}).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count external source: %w", err)
	}
	if n >= 10 {
		return service.ErrAuthSessionLimit
	}
	if err = p.checkExternalProvider(ctx, tx, f.ProviderID, f.ProviderVersion); err != nil {
		return err
	}
	if err = checkAuthAdmissionDeadline(ctx, tx, f.ExpiresAt); err != nil {
		return err
	}
	var userID any
	if f.UserID != "" {
		userID = f.UserID
	}
	_, err = tx.Insert(table).Rows(goqu.Record{"hash": f.Hash, "user_id": userID, "provider_id": f.ProviderID, "provider_version": f.ProviderVersion, "source": f.Source, "expires_at": f.ExpiresAt, "payload": sealed}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("persist external flow: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit external flow: %w", err)
	}
	return nil
}

func (p *Postgres) ConsumeAuthExternalFlow(ctx context.Context, hash, provider string, version int64) (*service.AuthExternalFlow, error) {
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	var row struct {
		Payload   string    `db:"payload"`
		ExpiresAt time.Time `db:"expires_at"`
		Live      bool      `db:"live"`
	}
	// DELETE RETURNING is the cross-replica one-use fence. Even failed decrypts burn it.
	found, err := p.goqu.Delete(p.externalTable("auth_external_flows")).Where(goqu.Ex{"hash": hash, "provider_id": provider, "provider_version": version}).Returning("payload", "expires_at", goqu.L("expires_at > clock_timestamp()").As("live")).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("consume external flow: %w", err)
	}
	if !found || !row.Live {
		return nil, service.ErrAuthConflict
	}
	if !atcrypto.IsEncrypted(row.Payload) {
		return nil, fmt.Errorf("external flow is not encrypted")
	}
	plain, err := atcrypto.Decrypt(row.Payload, p.encKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt external flow: %w", err)
	}
	return &service.AuthExternalFlow{Hash: hash, ProviderID: provider, ProviderVersion: version, ExpiresAt: row.ExpiresAt, Payload: json.RawMessage(plain)}, nil
}

func (p *Postgres) checkExternalProvider(ctx context.Context, tx *goqu.TxDatabase, id string, version int64) error {
	var actual string
	found, err := tx.From(p.externalTable("auth_identity_providers")).Select("id").Where(goqu.Ex{"id": id, "version": version, "enabled": true}).ForUpdate(goqu.Wait).ScanValContext(ctx, &actual)
	if err != nil {
		return fmt.Errorf("lock external provider: %w", err)
	}
	if !found {
		return service.ErrAuthConflict
	}
	return nil
}

func (p *Postgres) lockExternalAccount(ctx context.Context, tx *goqu.TxDatabase, a service.AuthExternalAccount) (*service.AuthUser, error) {
	var row authUserRow
	found, err := tx.From(p.tableAuthUsers).Select("id", "username", "password_hash", "admin", "disabled", "session_version").Where(goqu.Ex{"id": a.UserID, "session_version": a.Version, "disabled": false}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("lock linked account: %w", err)
	}
	if !found {
		return nil, service.ErrAuthConflict
	}
	if a.SessionID != "" {
		var id string
		found, err = tx.From(p.tableAuthSessions).Select("hash").Where(goqu.Ex{"hash": a.SessionID, "user_id": a.UserID, "version": a.Version, "transport": "web"}, goqu.C("expires_at").Gt(goqu.L("clock_timestamp()"))).ForUpdate(goqu.Wait).ScanValContext(ctx, &id)
		if err != nil {
			return nil, fmt.Errorf("lock linking session: %w", err)
		}
		if !found {
			return nil, service.ErrAuthConflict
		}
	}
	return authUserRowToRecord(row), nil
}

func (p *Postgres) CompleteAuthExternalIdentity(ctx context.Context, l service.AuthIdentityLink, version int64, account *service.AuthExternalAccount, deadline time.Time) (*service.AuthUser, *service.AuthIdentityLink, error) {
	if l.Subject == "" || len(l.Subject) > 1024 || len(l.Email) > 512 || len(l.AssertedPermissions) > 8192 || !json.Valid(l.AssertedPermissions) {
		return nil, nil, service.ErrAuthConflict
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("begin external identity: %w", err)
	}
	defer tx.Rollback()
	if err = p.lockExternalAdmission(ctx, tx); err != nil {
		return nil, nil, err
	}
	if err = p.checkExternalProvider(ctx, tx, l.ProviderID, version); err != nil {
		return nil, nil, err
	}
	var pr externalProviderRow
	if _, err = tx.From(p.externalTable("auth_identity_providers")).Where(goqu.Ex{"id": l.ProviderID}).ScanStructContext(ctx, &pr); err != nil {
		return nil, nil, fmt.Errorf("read identity namespace: %w", err)
	}
	pr.Secret = ""
	provider, err := externalProviderRecord(pr, nil)
	if err != nil {
		return nil, nil, err
	}
	namespace := provider.Issuer
	if provider.Mode == "oauth2" {
		namespace = "oauth2:" + provider.ID
	}
	if l.Issuer != namespace {
		return nil, nil, service.ErrAuthConflict
	}
	table := p.externalTable("auth_identity_links")
	var existing service.AuthIdentityLink
	found, err := tx.From(table).Where(goqu.Ex{"provider_id": l.ProviderID, "issuer": l.Issuer, "subject": l.Subject}).ScanStructContext(ctx, &existing)
	if err != nil {
		return nil, nil, fmt.Errorf("find identity link: %w", err)
	}
	if l.ID != "" && (!found || existing.ID != l.ID) {
		return nil, nil, service.ErrAuthConflict
	}
	var u *service.AuthUser
	if account != nil {
		if account.SessionID == "" {
			return nil, nil, service.ErrAuthConflict
		}
		u, err = p.lockExternalAccount(ctx, tx, *account)
		if found && existing.UserID != account.UserID {
			return nil, nil, service.ErrAuthConflict
		}
	} else if found {
		var row authUserRow
		ok, e := tx.From(p.tableAuthUsers).Select("id", "username", "password_hash", "admin", "disabled", "session_version").Where(goqu.Ex{"id": existing.UserID, "disabled": false}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
		if e != nil {
			return nil, nil, fmt.Errorf("lock external user: %w", e)
		}
		if !ok {
			return nil, nil, service.ErrAuthConflict
		}
		u = authUserRowToRecord(row)
	} else {
		// Hard bound on JIT accounts, independent of process-local rate limit.
		n, e := tx.From(p.tableAuthUsers).Where(goqu.C("username").Like("external-%")).CountContext(ctx)
		if e != nil {
			return nil, nil, fmt.Errorf("count external users: %w", e)
		}
		if n >= 1000 {
			return nil, nil, service.ErrAuthSessionLimit
		}
		id := ulid.Make().String()
		u = &service.AuthUser{ID: id, Username: "external-" + strings.ToLower(id), SessionVersion: 1}
		_, err = tx.Insert(p.tableAuthUsers).Rows(goqu.Record{"id": u.ID, "username": u.Username, "password_hash": "", "admin": false, "disabled": false, "session_version": u.SessionVersion}).Executor().ExecContext(ctx)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("admit external user: %w", err)
	}
	if found {
		// Recovery takes the user lock and removes identity links. Recheck after
		// obtaining that lock rather than adopting a link read before recovery.
		var liveID string
		live, e := tx.From(table).Select("id").Where(goqu.Ex{"id": existing.ID, "user_id": u.ID}).ScanValContext(ctx, &liveID)
		if e != nil {
			return nil, nil, fmt.Errorf("recheck external identity: %w", e)
		}
		if !live {
			return nil, nil, service.ErrAuthConflict
		}
		// Refresh metadata from this verified assertion without changing identity.
		existing.Email, existing.EmailVerified, existing.AssertedPermissions = l.Email, l.EmailVerified, l.AssertedPermissions
		if _, e = tx.Update(table).Set(goqu.Record{"email": existing.Email, "email_verified": existing.EmailVerified, "asserted_permissions": string(existing.AssertedPermissions)}).Where(goqu.Ex{"id": existing.ID}).Executor().ExecContext(ctx); e != nil {
			return nil, nil, fmt.Errorf("update asserted identity metadata: %w", e)
		}
		l = existing
	} else {
		l.ID = ulid.Make().String()
		l.UserID = u.ID
		_, err = tx.Insert(table).Rows(goqu.Record{"id": l.ID, "provider_id": l.ProviderID, "issuer": l.Issuer, "subject": l.Subject, "user_id": l.UserID, "email": l.Email, "email_verified": l.EmailVerified, "asserted_permissions": string(l.AssertedPermissions)}).Executor().ExecContext(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("create identity link: %w", err)
		}
	}
	if deadline.IsZero() {
		return nil, nil, service.ErrAuthConflict
	}
	if err = checkAuthAdmissionDeadline(ctx, tx, deadline); err != nil {
		return nil, nil, err
	}
	if account != nil {
		var live bool
		ok, e := tx.From(p.tableAuthSessions).Select(goqu.L("expires_at > clock_timestamp()")).Where(goqu.Ex{"hash": account.SessionID, "user_id": account.UserID, "version": account.Version}).ScanValContext(ctx, &live)
		if e != nil {
			return nil, nil, fmt.Errorf("recheck linking session expiry: %w", e)
		}
		if !ok || !live {
			return nil, nil, service.ErrAuthConflict
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, fmt.Errorf("commit external identity: %w", err)
	}
	return u, &l, nil
}

func (p *Postgres) ListAuthIdentityLinks(ctx context.Context, user string) ([]service.AuthIdentityLink, error) {
	rows := make([]service.AuthIdentityLink, 0)
	if err := p.goqu.From(p.externalTable("auth_identity_links")).Where(goqu.Ex{"user_id": user}).Order(goqu.C("id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list identity links: %w", err)
	}
	return rows, nil
}

func (p *Postgres) UnlinkAuthIdentity(ctx context.Context, id string, a service.AuthExternalAccount) error {
	if a.SessionID == "" {
		return service.ErrAuthConflict
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin unlink: %w", err)
	}
	defer tx.Rollback()
	if err = p.lockExternalAdmission(ctx, tx); err != nil {
		return err
	}
	u, err := p.lockExternalAccount(ctx, tx, a)
	if err != nil {
		return err
	}
	table := p.externalTable("auth_identity_links")
	if u.PasswordHash == "" {
		n, e := tx.From(p.tableAuthPasskeys).Where(goqu.Ex{"user_id": u.ID}).CountContext(ctx)
		if e != nil {
			return fmt.Errorf("count primary passkeys: %w", e)
		}
		if n == 0 {
			n, e = tx.From(table.As("l")).Join(p.externalTable("auth_identity_providers").As("p"), goqu.On(goqu.I("l.provider_id").Eq(goqu.I("p.id")))).Where(goqu.Ex{"l.user_id": u.ID, "p.enabled": true}, goqu.I("l.id").Neq(id)).CountContext(ctx)
			if e != nil {
				return fmt.Errorf("count primary identities: %w", e)
			}
			if n == 0 {
				return service.ErrAuthConflict
			}
		}
	}
	result, err := tx.Delete(table).Where(goqu.Ex{"id": id, "user_id": u.ID}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("unlink identity: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("unlink identity rows: %w", err)
	}
	if n != 1 {
		return service.ErrAuthConflict
	}
	if _, err = tx.Update(p.tableAuthUsers).Set(goqu.Record{"session_version": goqu.L("session_version + 1")}).Where(goqu.Ex{"id": u.ID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("fence unlinked account: %w", err)
	}
	if _, err = tx.Delete(p.tableAuthSessions).Where(goqu.Ex{"user_id": u.ID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("revoke unlinked sessions: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit unlink: %w", err)
	}
	return nil
}

// CheckAuthExternalAdmission must run before the user lock in common completion.
// Caller retains this transaction through final session/challenge insertion.
func (p *Postgres) CheckAuthExternalAdmission(ctx context.Context, tx *goqu.TxDatabase, c service.AuthExternalCompletion, user string) error {
	if err := p.lockExternalAdmission(ctx, tx); err != nil {
		return err
	}
	if err := p.checkExternalProvider(ctx, tx, c.ProviderID, c.ProviderVersion); err != nil {
		return err
	}
	var id string
	found, err := tx.From(p.externalTable("auth_identity_links")).Select("id").Where(goqu.Ex{"id": c.LinkID, "provider_id": c.ProviderID, "user_id": user}).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("check primary identity: %w", err)
	}
	if !found {
		return service.ErrAuthConflict
	}
	return checkAuthAdmissionDeadline(ctx, tx, c.Deadline)
}

// Called with encKeyMu held by the existing shared key-rotation transaction.
func (p *Postgres) rotateAuthExternalSecrets(ctx context.Context, tx *sql.Tx, oldKey, newKey []byte) error {
	for _, item := range []struct{ table, key, column string }{{"auth_identity_providers", "id", "secret"}, {"auth_external_flows", "hash", "payload"}} {
		var rows []struct {
			ID     string `db:"id"`
			Secret string `db:"secret"`
		}
		table := p.externalTable(item.table)
		query, args, err := p.goqu.From(table).Select(goqu.C(item.key), goqu.C(item.column)).ForUpdate(goqu.Wait).Prepared(true).ToSQL()
		if err != nil {
			return fmt.Errorf("build external rotation: %w", err)
		}
		result, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("read external rotation: %w", err)
		}
		for result.Next() {
			var row struct {
				ID     string `db:"id"`
				Secret string `db:"secret"`
			}
			if err := result.Scan(&row.ID, &row.Secret); err != nil {
				result.Close()
				return fmt.Errorf("scan external rotation: %w", err)
			}
			rows = append(rows, row)
		}
		result.Close()
		if err := result.Err(); err != nil {
			return fmt.Errorf("iterate external rotation: %w", err)
		}
		if len(rows) > 0 && len(newKey) != 32 {
			return fmt.Errorf("external authentication requires encryption")
		}
		for _, r := range rows {
			if r.Secret == "" {
				continue
			}
			if !atcrypto.IsEncrypted(r.Secret) {
				return fmt.Errorf("external secret is not encrypted")
			}
			plain, err := atcrypto.Decrypt(r.Secret, oldKey)
			if err != nil {
				return fmt.Errorf("decrypt external rotation: %w", err)
			}
			sealed, err := atcrypto.Encrypt(plain, newKey)
			if err != nil {
				return fmt.Errorf("encrypt external rotation: %w", err)
			}
			query, args, err := p.goqu.Update(table).Set(goqu.Record{item.column: sealed}).Where(goqu.Ex{item.key: r.ID}).Prepared(true).ToSQL()
			if err != nil {
				return fmt.Errorf("build external rotation update: %w", err)
			}
			if _, err = tx.ExecContext(ctx, query, args...); err != nil {
				return fmt.Errorf("write external rotation: %w", err)
			}
		}
	}
	return nil
}

// The external admission row is held by the caller. Fixed-window counters remain
// after callback consumption, so sequential initiations cannot evade the limit.
func (p *Postgres) admitExternalSource(ctx context.Context, tx *goqu.TxDatabase, source string) error {
	table := p.externalTable("auth_external_sources")
	if _, err := tx.Delete(table).Where(goqu.C("expires_at").Lte(goqu.L("clock_timestamp()"))).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("expire external sources: %w", err)
	}
	var attempts int
	found, err := tx.From(table).Select("attempts").Where(goqu.Ex{"hash": source}).ScanValContext(ctx, &attempts)
	if err != nil {
		return fmt.Errorf("read external source: %w", err)
	}
	if found {
		if attempts >= 30 {
			return service.ErrAuthSessionLimit
		}
		_, err = tx.Update(table).Set(goqu.Record{"attempts": goqu.L("attempts + 1")}).Where(goqu.Ex{"hash": source}).Executor().ExecContext(ctx)
	} else {
		count, e := tx.From(table).CountContext(ctx)
		if e != nil {
			return fmt.Errorf("count external sources: %w", e)
		}
		if count >= 4096 {
			return service.ErrAuthSessionLimit
		}
		_, err = tx.Insert(table).Rows(goqu.Record{"hash": source, "attempts": 1, "expires_at": goqu.L("clock_timestamp() + interval '5 minutes'")}).Executor().ExecContext(ctx)
	}
	if err != nil {
		return fmt.Errorf("record external source: %w", err)
	}
	return nil
}
