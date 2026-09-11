package postgres

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.WorkspaceStorer = (*Postgres)(nil)

func (p *Postgres) GetWorkspace(ctx context.Context) (*service.Workspace, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin workspace detail: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "workspace.read")
	if err != nil {
		return nil, err
	}
	var w service.Workspace
	found, err := tx.From(p.workspaceTable("workspaces")).Select("id", "name", "archived", "execution_enabled", "created_at").Where(goqu.Ex{"id": a.WorkspaceID, "archived": false}).ScanStructContext(ctx, &w)
	if err != nil {
		return nil, fmt.Errorf("get workspace: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	w.Role = a.Role
	return &w, nil
}

func (p *Postgres) ListWorkspaces(ctx context.Context) ([]service.Workspace, error) {
	a, ok := service.AccessPrincipalFromContext(ctx)
	if !ok {
		return nil, service.ErrAccessDenied
	}
	u, err := p.workspaceIdentity(ctx, p.goqu, a.UserID, a.SessionID)
	if err != nil {
		return nil, err
	}
	if u.Admin {
		if err := p.ensureDefaultWorkspaceOwner(ctx, u.ID, a.SessionID); err != nil {
			return nil, err
		}
	}
	w := p.workspaceTable("workspaces").As("w")
	m := p.workspaceTable("workspace_memberships").As("m")
	q := p.goqu.From(w).Select(goqu.I("w.id"), goqu.I("w.name"), goqu.I("w.archived"), goqu.I("w.execution_enabled"), goqu.I("w.created_at"), goqu.COALESCE(goqu.I("m.role"), "").As("role")).LeftJoin(m, goqu.On(goqu.Ex{"m.workspace_id": goqu.I("w.id"), "m.user_id": u.ID, "m.status": "active"})).Where(goqu.Ex{"w.archived": false}).Order(goqu.I("w.id").Asc())
	if !u.Admin {
		q = q.Where(goqu.I("m.user_id").IsNotNull())
	}
	rows := []service.Workspace{}
	if err := q.ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list workspaces: %w", err)
	}
	return rows, nil
}

func (p *Postgres) CreateWorkspace(ctx context.Context, name, ownerID string) (*service.Workspace, error) {
	a, ok := service.AccessPrincipalFromContext(ctx)
	if !ok {
		return nil, service.ErrAccessDenied
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 || ownerID == "" {
		return nil, service.ErrWorkspaceConflict
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin create workspace: %w", err)
	}
	defer tx.Rollback()
	u, err := p.workspaceIdentity(ctx, tx, a.UserID, a.SessionID)
	if err != nil {
		return nil, err
	}
	if !u.Admin {
		return nil, service.ErrAccessDenied
	}
	if _, err = p.workspaceIdentity(ctx, tx, ownerID, ""); err != nil {
		return nil, err
	}
	w := service.Workspace{ID: ulid.Make().String(), Name: name, CreatedAt: time.Now().UTC()}
	if _, err = tx.Insert(p.workspaceTable("workspaces")).Rows(goqu.Record{"id": w.ID, "name": w.Name, "created_at": w.CreatedAt}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("create workspace: %w", err)
	}
	if _, err = tx.Insert(p.workspaceTable("workspace_memberships")).Rows(goqu.Record{"workspace_id": w.ID, "user_id": ownerID, "role": "owner"}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("create workspace owner: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace: %w", err)
	}
	return &w, nil
}

func (p *Postgres) UpdateWorkspace(ctx context.Context, w service.Workspace) (*service.Workspace, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update workspace: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "workspace.write")
	if err != nil {
		return nil, err
	}
	if w.ID != a.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if w.Archived && (!a.Allows("workspace.archive", service.AccessResource{WorkspaceID: w.ID}) || !a.PlatformAdmin && a.Role != "owner") {
		return nil, service.ErrAccessDenied
	}
	var old service.Workspace
	if _, err = tx.From(p.workspaceTable("workspaces")).Select("id", "name", "archived", "execution_enabled", "created_at").Where(goqu.Ex{"id": w.ID}).ScanStructContext(ctx, &old); err != nil {
		return nil, fmt.Errorf("read workspace configuration: %w", err)
	}
	if old.ExecutionEnabled != w.ExecutionEnabled && !a.Allows("execution.configure", service.AccessResource{WorkspaceID: w.ID}) {
		return nil, service.ErrAccessDenied
	}
	w.Name = strings.TrimSpace(w.Name)
	if w.Name == "" || len(w.Name) > 200 {
		return nil, service.ErrWorkspaceConflict
	}
	if _, err = tx.Update(p.workspaceTable("workspaces")).Set(goqu.Record{"name": w.Name, "archived": w.Archived, "execution_enabled": w.ExecutionEnabled}).Where(goqu.Ex{"id": a.WorkspaceID}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("update workspace: %w", err)
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return nil, err
	}
	w.CreatedAt = old.CreatedAt
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace update: %w", err)
	}
	return &w, nil
}

func (p *Postgres) ListWorkspaceMembers(ctx context.Context) ([]service.WorkspaceMembership, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list members: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "members.read")
	if err != nil {
		return nil, err
	}
	rows := []service.WorkspaceMembership{}
	if err = tx.From(p.workspaceTable("workspace_memberships")).Where(goqu.Ex{"workspace_id": a.WorkspaceID}).Order(goqu.C("user_id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list workspace members: %w", err)
	}
	return rows, nil
}

func workspaceMayGrantRole(a service.AccessPrincipal, role string) bool {
	rank := service.WorkspaceRoleRank(role)
	if rank == 0 {
		return false
	}
	if a.PlatformAdmin {
		return true
	}
	if rank > service.WorkspaceRoleRank(a.Role) || role == "owner" && a.Role != "owner" {
		return false
	}
	for _, g := range service.WorkspaceRoleGrants(role) {
		if !service.CanDelegateAccess(a, g) {
			return false
		}
	}
	return true
}

func (p *Postgres) SetWorkspaceMember(ctx context.Context, m service.WorkspaceMembership) error {
	if m.Status != "active" && m.Status != "revoked" || service.WorkspaceRoleRank(m.Role) == 0 {
		return service.ErrWorkspaceConflict
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin membership change: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "members.manage")
	if err != nil {
		return err
	}
	if m.WorkspaceID != a.WorkspaceID || !workspaceMayGrantRole(a, m.Role) {
		return service.ErrAccessDenied
	}
	if _, err = p.workspaceIdentity(ctx, tx, m.UserID, ""); err != nil {
		return err
	}
	var old service.WorkspaceMembership
	found, err := tx.From(p.workspaceTable("workspace_memberships")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": m.UserID}).ScanStructContext(ctx, &old)
	if err != nil {
		return fmt.Errorf("read membership: %w", err)
	}
	if found && !a.PlatformAdmin && service.WorkspaceRoleRank(old.Role) > service.WorkspaceRoleRank(a.Role) {
		return service.ErrAccessDenied
	}
	if old.Role == "owner" && old.Status == "active" && (m.Role != "owner" || m.Status != "active") {
		n, err := tx.From(p.workspaceTable("workspace_memberships")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "role": "owner", "status": "active"}).CountContext(ctx)
		if err != nil {
			return fmt.Errorf("count workspace owners: %w", err)
		}
		if n <= 1 {
			return service.ErrWorkspaceConflict
		}
	}
	row := goqu.Record{"workspace_id": a.WorkspaceID, "user_id": m.UserID, "role": m.Role, "status": m.Status}
	if _, err = tx.Insert(p.workspaceTable("workspace_memberships")).Rows(row).OnConflict(goqu.DoUpdate("workspace_id,user_id", goqu.Record{"role": m.Role, "status": m.Status, "version": goqu.L("? + 1", p.workspaceTable("workspace_memberships").Col("version"))})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("save workspace membership: %w", err)
	}
	return tx.Commit()
}

func (p *Postgres) CreateWorkspaceInvitation(ctx context.Context, v service.WorkspaceInvitation) (*service.WorkspaceInvitation, error) {
	v.Email = strings.ToLower(strings.TrimSpace(v.Email))
	if (v.UserID == "") == (v.Email == "") || len(v.TokenHash) != 64 || !v.ExpiresAt.After(time.Now()) || v.ExpiresAt.After(time.Now().Add(7*24*time.Hour)) {
		return nil, service.ErrWorkspaceConflict
	}
	if v.Email != "" {
		parsed, err := mail.ParseAddress(v.Email)
		if err != nil || parsed.Address != v.Email {
			return nil, service.ErrWorkspaceConflict
		}
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin invitation: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "members.manage")
	if err != nil {
		return nil, err
	}
	if v.WorkspaceID != a.WorkspaceID || !workspaceMayGrantRole(a, v.Role) {
		return nil, service.ErrAccessDenied
	}
	if v.UserID != "" {
		if _, err = p.workspaceIdentity(ctx, tx, v.UserID, ""); err != nil {
			return nil, err
		}
	}
	v.ID = ulid.Make().String()
	v.IssuerID = a.UserID
	v.Consumed = false
	if _, err = tx.Insert(p.workspaceTable("workspace_invitations")).Rows(v).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("create invitation: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit invitation: %w", err)
	}
	return &v, nil
}

func (p *Postgres) ListWorkspaceInvitations(ctx context.Context) ([]service.WorkspaceInvitation, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin list invitations: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "members.manage")
	if err != nil {
		return nil, err
	}
	rows := []service.WorkspaceInvitation{}
	if err = tx.From(p.workspaceTable("workspace_invitations")).Where(goqu.Ex{"workspace_id": a.WorkspaceID}).Order(goqu.C("id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list workspace invitations: %w", err)
	}
	return rows, nil
}

func (p *Postgres) AcceptWorkspaceInvitation(ctx context.Context, hash string) (*service.WorkspaceMembership, error) {
	a, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || len(hash) != 64 {
		return nil, service.ErrAccessDenied
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin accept invitation: %w", err)
	}
	defer tx.Rollback()
	var v service.WorkspaceInvitation
	found, err := tx.From(p.workspaceTable("workspace_invitations")).Where(goqu.Ex{"token_hash": hash}).ScanStructContext(ctx, &v)
	if err != nil {
		return nil, fmt.Errorf("find invitation: %w", err)
	}
	if !found {
		return nil, service.ErrAccessDenied
	}
	// All policy mutations lock workspace first, then invitation. Re-read after lock.
	var w service.Workspace
	found, err = tx.From(p.workspaceTable("workspaces")).Select("id", "name", "archived", "execution_enabled", "created_at").Where(goqu.Ex{"id": v.WorkspaceID, "archived": false}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &w)
	if err != nil {
		return nil, fmt.Errorf("lock invitation workspace: %w", err)
	}
	if !found {
		return nil, service.ErrAccessDenied
	}
	found, err = tx.From(p.workspaceTable("workspace_invitations")).Where(goqu.Ex{"id": v.ID, "consumed": false}, goqu.C("expires_at").Gt(time.Now())).ForUpdate(goqu.Wait).ScanStructContext(ctx, &v)
	if err != nil {
		return nil, fmt.Errorf("lock invitation: %w", err)
	}
	if !found || !v.ExpiresAt.After(time.Now()) {
		return nil, service.ErrAccessDenied
	}
	if _, err = p.workspaceIdentity(ctx, tx, a.UserID, a.SessionID); err != nil {
		return nil, err
	}
	issuer, _, err := p.resolveWorkspaceAccess(ctx, tx, v.WorkspaceID, v.IssuerID, "")
	if err != nil || !issuer.Allows("members.manage", service.AccessResource{WorkspaceID: v.WorkspaceID}) || !workspaceMayGrantRole(issuer, v.Role) {
		return nil, service.ErrAccessDenied
	}
	if v.UserID != "" && v.UserID != a.UserID {
		return nil, service.ErrAccessDenied
	}
	if v.Email != "" {
		// Eligibility comes only from verified links on THIS user, never account lookup.
		n, err := tx.From(p.workspaceTable("auth_identity_links").As("l")).Join(p.workspaceTable("auth_identity_providers").As("p"), goqu.On(goqu.Ex{"l.provider_id": goqu.I("p.id"), "p.enabled": true})).Where(goqu.Ex{"l.user_id": a.UserID, "l.email_verified": true}, goqu.L("lower(?) = ?", goqu.I("l.email"), v.Email)).CountContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("verify invitation eligibility: %w", err)
		}
		if n == 0 {
			return nil, service.ErrAccessDenied
		}
	}
	var m service.WorkspaceMembership
	found, err = tx.From(p.workspaceTable("workspace_memberships")).Where(goqu.Ex{"workspace_id": v.WorkspaceID, "user_id": a.UserID}).ScanStructContext(ctx, &m)
	if err != nil {
		return nil, fmt.Errorf("read invited membership: %w", err)
	}
	if found {
		return nil, service.ErrWorkspaceConflict
	}
	m = service.WorkspaceMembership{WorkspaceID: v.WorkspaceID, UserID: a.UserID, Role: v.Role, Status: "active", Version: 1}
	if _, err = tx.Insert(p.workspaceTable("workspace_memberships")).Rows(m).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("accept workspace membership: %w", err)
	}
	if _, err = tx.Update(p.workspaceTable("workspace_invitations")).Set(goqu.Record{"consumed": true}).Where(goqu.Ex{"id": v.ID}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("consume invitation: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit invitation acceptance: %w", err)
	}
	return &m, nil
}

func (p *Postgres) bumpWorkspacePolicy(ctx context.Context, tx *goqu.TxDatabase, id string) error {
	if _, err := tx.Update(p.workspaceTable("workspace_memberships")).Set(goqu.Record{"version": goqu.L("version + 1")}).Where(goqu.Ex{"workspace_id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("bump workspace policy: %w", err)
	}
	return nil
}
