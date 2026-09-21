package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.AuthUserDirectory = (*Postgres)(nil)

// Records keyed on the account that the foreign keys do not reach. Two reasons
// a row lands here: the reference was declared without ON DELETE CASCADE
// (workspace membership and everything keyed on it, invitations the account
// issued), or the table carries a user ID with no foreign key at all
// (execution bindings, recovery events, mobile requests, Playground history).
// Order is child-first so no delete trips a constraint of its own.
var authUserDeletionTables = []struct {
	table  string
	column string
}{
	{"workspace_user_permissions", "user_id"},
	{"workspace_user_denied", "user_id"},
	{"workspace_memberships", "user_id"},
	{"workspace_invitations", "issuer_id"},
	{"workspace_preferences", "user_id"},
	{"execution_service_bindings", "user_id"},
	{"execution_service_bindings", "owner_user_id"},
	{"auth_recovery_events", "user_id"},
	{"auth_mobile_requests", "user_id"},
	{"playground_conversations", "owner_user_id"},
	{"chat_sessions", "owner_user_id"},
	{"agents", "owner_user_id"},
	{"tokens", "owner_user_id"},
}

// DeleteAuthUser removes an account permanently. Disable stays the reversible
// option; this exists for accounts that should leave no record at all.
//
// media_objects is deliberately not swept: its rows are only bookkeeping for
// blobs in the configured media backend, which a database transaction cannot
// reach. Deleting the rows would strand the blobs with nothing pointing at
// them, so they are left owner-scoped and unreachable instead.
func (p *Postgres) DeleteAuthUser(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	tx, err := p.goqu.BeginTx(ctx, &sql.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin delete auth user: %w", err)
	}
	defer tx.Rollback()
	// Same serialization point as InvalidateAuthUser: two administrators must
	// not be able to remove each other concurrently and lock AT out.
	var claimed bool
	if _, err := tx.From(p.tableAuthBootstrap).Select("claimed").ForUpdate(goqu.Wait).ScanValContext(ctx, &claimed); err != nil {
		return false, fmt.Errorf("lock auth administration: %w", err)
	}
	var target authUserRow
	found, err := tx.From(p.tableAuthUsers).Select("admin", "disabled").Where(goqu.Ex{"id": id}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &target)
	if err != nil {
		return false, fmt.Errorf("get delete target: %w", err)
	}
	if !found {
		return false, nil
	}
	if target.Admin && !target.Disabled {
		count, err := tx.From(p.tableAuthUsers).Where(goqu.Ex{"admin": true, "disabled": false}).CountContext(ctx)
		if err != nil {
			return false, fmt.Errorf("count active administrators: %w", err)
		}
		if count <= 1 {
			return false, service.ErrAuthConflict
		}
	}
	for _, t := range authUserDeletionTables {
		if _, err := tx.Delete(p.workspaceTable(t.table)).Where(goqu.Ex{t.column: id}).Executor().ExecContext(ctx); err != nil {
			return false, fmt.Errorf("delete auth user %s: %w", t.table, err)
		}
	}
	if _, err := tx.Delete(p.tableAuthUsers).Where(goqu.Ex{"id": id}).Executor().ExecContext(ctx); err != nil {
		return false, fmt.Errorf("delete auth user: %w", err)
	}
	// The last-external-administrator guard is a deferred constraint trigger,
	// so it reports at COMMIT rather than at the DELETE.
	if err := tx.Commit(); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23514" {
			return false, service.ErrAuthConflict
		}
		return false, fmt.Errorf("commit delete auth user: %w", err)
	}
	return true, nil
}

// ListAuthUserIdentities reports the external identities behind a page of
// accounts, including current provider names and email verification metadata
// even when the account username is a legacy generated name.
func (p *Postgres) ListAuthUserIdentities(ctx context.Context, ids []string) (map[string][]service.AuthUserIdentity, error) {
	out := map[string][]service.AuthUserIdentity{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		UserID        string `db:"user_id"`
		ProviderID    string `db:"provider_id"`
		Subject       string `db:"subject"`
		Username      string `db:"username"`
		Email         string `db:"email"`
		EmailVerified bool   `db:"email_verified"`
	}
	err := p.goqu.From(p.externalTable("auth_identity_links")).
		Select("user_id", "provider_id", "subject", "username", "email", "email_verified").
		Where(goqu.I("user_id").In(ids)).Order(goqu.I("id").Asc()).ScanStructsContext(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list auth user identities: %w", err)
	}
	for _, row := range rows {
		out[row.UserID] = append(out[row.UserID], service.AuthUserIdentity{
			ProviderID: row.ProviderID, Subject: row.Subject, Username: row.Username, Email: row.Email, EmailVerified: row.EmailVerified,
		})
	}
	return out, nil
}

// ListAuthUserWorkspaces reports every membership of one account, revoked rows
// included: "revoked" is why an account sees nothing, and claim admission never
// restores one, so hiding it would hide the explanation.
func (p *Postgres) ListAuthUserWorkspaces(ctx context.Context, id string) ([]service.AuthUserWorkspace, error) {
	rows := make([]service.AuthUserWorkspace, 0)
	if id == "" {
		return rows, nil
	}
	m, w := p.workspaceTable("workspace_memberships").As("m"), p.workspaceTable("workspaces").As("w")
	err := p.goqu.From(m).Join(w, goqu.On(goqu.I("w.id").Eq(goqu.I("m.workspace_id")))).
		Select(goqu.I("m.workspace_id").As("workspace_id"), goqu.I("w.name").As("name"), goqu.I("m.role").As("role"), goqu.I("m.status").As("status")).
		Where(goqu.Ex{"m.user_id": id}).Order(goqu.I("w.name").Asc()).ScanStructsContext(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list auth user workspaces: %w", err)
	}
	return rows, nil
}
