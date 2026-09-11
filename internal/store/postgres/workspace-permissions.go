package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

type workspaceBundleRow struct {
	ID          string `db:"id"`
	WorkspaceID string `db:"workspace_id"`
	Key         string `db:"key"`
	Bundle      string `db:"bundle"`
}
type workspaceMappingRow struct {
	ID           string `db:"id"`
	WorkspaceID  string `db:"workspace_id"`
	ProviderID   string `db:"provider_id"`
	ClaimKind    string `db:"claim_kind"`
	ClaimValue   string `db:"claim_value"`
	PermissionID string `db:"permission_id"`
}

func (r workspaceMappingRow) record() service.PermissionMapping {
	return service.PermissionMapping{ID: r.ID, WorkspaceID: r.WorkspaceID, ProviderID: r.ProviderID, ClaimKind: r.ClaimKind, ClaimValue: r.ClaimValue, PermissionID: r.PermissionID}
}

func (p *Postgres) workspaceBundles(ctx context.Context, q workspaceReader, id string) ([]service.PermissionBundle, error) {
	var rows []workspaceBundleRow
	if err := q.From(p.workspaceTable("workspace_permissions")).Where(goqu.Ex{"workspace_id": id}).Order(goqu.C("key").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}
	out := make([]service.PermissionBundle, 0, len(rows))
	for _, r := range rows {
		var b service.PermissionBundle
		if err := json.Unmarshal([]byte(r.Bundle), &b); err != nil {
			return nil, fmt.Errorf("decode permission: %w", err)
		}
		b.ID = r.ID
		b.WorkspaceID = r.WorkspaceID
		b.Key = r.Key
		out = append(out, b)
	}
	return out, nil
}
func (p *Postgres) ListPermissions(ctx context.Context) ([]service.PermissionBundle, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin permission list: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.read")
	if err != nil {
		return nil, err
	}
	return p.workspaceBundles(ctx, tx, a.WorkspaceID)
}
func delegableBundle(a service.AccessPrincipal, b service.PermissionBundle) bool {
	gs, err := b.Grants()
	if err != nil {
		return false
	}
	for _, g := range gs {
		if !service.CanDelegateAccess(a, g) {
			return false
		}
	}
	return true
}
func (p *Postgres) workspacePermission(ctx context.Context, tx *goqu.TxDatabase, a service.AccessPrincipal, id string) (*service.PermissionBundle, error) {
	bs, err := p.workspaceBundles(ctx, tx, a.WorkspaceID)
	if err != nil {
		return nil, err
	}
	for _, b := range bs {
		if b.ID == id {
			if !delegableBundle(a, b) {
				return nil, service.ErrAccessDenied
			}
			return &b, nil
		}
	}
	return nil, service.ErrAccessResourceNotFound
}
func (p *Postgres) SavePermission(ctx context.Context, b service.PermissionBundle) (*service.PermissionBundle, error) {
	if b.Key == "" || strings.TrimSpace(b.Key) != b.Key || len(b.Key) > 100 || len(b.Name) > 200 || len(b.Description) > 4000 || len(b.Keys) > 100 {
		return nil, service.ErrWorkspaceConflict
	}
	if _, err := b.Grants(); err != nil {
		return nil, err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin permission save: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.manage")
	if err != nil {
		return nil, err
	}
	if b.WorkspaceID != "" && b.WorkspaceID != a.WorkspaceID || !delegableBundle(a, b) {
		return nil, service.ErrAccessDenied
	}
	b.WorkspaceID = a.WorkspaceID
	if err = p.validatePermissionResources(ctx, tx, a, b); err != nil {
		return nil, err
	}
	create := b.ID == ""
	if create {
		b.ID = ulid.Make().String()
	} else {
		if _, err = p.workspacePermission(ctx, tx, a, b.ID); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("encode permission: %w", err)
	}
	row := goqu.Record{"id": b.ID, "workspace_id": a.WorkspaceID, "key": b.Key, "bundle": string(encoded)}
	if create {
		_, err = tx.Insert(p.workspaceTable("workspace_permissions")).Rows(row).Executor().ExecContext(ctx)
	} else {
		_, err = tx.Update(p.workspaceTable("workspace_permissions")).Set(goqu.Record{"key": b.Key, "bundle": string(encoded)}).Where(goqu.Ex{"id": b.ID, "workspace_id": a.WorkspaceID}).Executor().ExecContext(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("save permission: %w", err)
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit permission: %w", err)
	}
	return &b, nil
}
func (p *Postgres) DeletePermission(ctx context.Context, id string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin permission delete: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.manage")
	if err != nil {
		return err
	}
	if _, err = p.workspacePermission(ctx, tx, a, id); err != nil {
		return err
	}
	if _, err = tx.Delete(p.workspaceTable("workspace_permissions")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete permission: %w", err)
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *Postgres) workspacePermissionTarget(ctx context.Context, tx *goqu.TxDatabase, a service.AccessPrincipal, userID string, write bool) error {
	var m service.WorkspaceMembership
	found, err := tx.From(p.workspaceTable("workspace_memberships")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": userID}).ScanStructContext(ctx, &m)
	if err != nil {
		return fmt.Errorf("find permission member: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	if write && !a.PlatformAdmin && service.WorkspaceRoleRank(m.Role) > service.WorkspaceRoleRank(a.Role) {
		return service.ErrAccessDenied
	}
	return nil
}
func (p *Postgres) GetUserPermissions(ctx context.Context, userID string) ([]string, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin user permissions: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.read")
	if err != nil {
		return nil, err
	}
	if err = p.workspacePermissionTarget(ctx, tx, a, userID, false); err != nil {
		return nil, err
	}
	ids := []string{}
	if err = tx.From(p.workspaceTable("workspace_user_permissions")).Select("permission_id").Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": userID}).Order(goqu.C("permission_id").Asc()).ScanValsContext(ctx, &ids); err != nil {
		return nil, fmt.Errorf("read user permissions: %w", err)
	}
	return ids, nil
}
func (p *Postgres) SetUserPermissions(ctx context.Context, userID string, ids []string) error {
	if len(ids) > 100 {
		return service.ErrWorkspaceConflict
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin assign permissions: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.manage")
	if err != nil {
		return err
	}
	if err = p.workspacePermissionTarget(ctx, tx, a, userID, true); err != nil {
		return err
	}
	var old []string
	if err = tx.From(p.workspaceTable("workspace_user_permissions")).Select("permission_id").Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": userID}).ScanValsContext(ctx, &old); err != nil {
		return fmt.Errorf("read previous permissions: %w", err)
	}
	for _, id := range append(slices.Clone(ids), old...) {
		if _, err = p.workspacePermission(ctx, tx, a, id); err != nil {
			return err
		}
	}
	if _, err = tx.Delete(p.workspaceTable("workspace_user_permissions")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": userID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("clear assigned permissions: %w", err)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if _, err = tx.Insert(p.workspaceTable("workspace_user_permissions")).Rows(goqu.Record{"workspace_id": a.WorkspaceID, "user_id": userID, "permission_id": id}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("assign permission: %w", err)
		}
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *Postgres) SetUserDenied(ctx context.Context, userID string, keys []string) error {
	if len(keys) > 100 {
		return service.ErrWorkspaceConflict
	}
	for _, key := range keys {
		c, ok := service.KnownAccessCapability(key)
		if !ok || c.PlatformOnly {
			return service.ErrAccessDenied
		}
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin denied permissions: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.manage")
	if err != nil {
		return err
	}
	if err = p.workspacePermissionTarget(ctx, tx, a, userID, true); err != nil {
		return err
	}
	var old []string
	if err = tx.From(p.workspaceTable("workspace_user_denied")).Select("capability").Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": userID}).ScanValsContext(ctx, &old); err != nil {
		return fmt.Errorf("read previous denies: %w", err)
	}
	// Removing a deny restores authority, so requires the same delegation ceiling.
	for _, key := range old {
		if !slices.Contains(keys, key) && !service.CanDelegateAccess(a, service.AccessGrant{Capability: key}) {
			return service.ErrAccessDenied
		}
	}
	if _, err = tx.Delete(p.workspaceTable("workspace_user_denied")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "user_id": userID}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("clear denied permissions: %w", err)
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err = tx.Insert(p.workspaceTable("workspace_user_denied")).Rows(goqu.Record{"workspace_id": a.WorkspaceID, "user_id": userID, "capability": key}).Executor().ExecContext(ctx); err != nil {
			return fmt.Errorf("save denied permission: %w", err)
		}
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *Postgres) ListPermissionMappings(ctx context.Context) ([]service.PermissionMapping, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin mapping list: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.read")
	if err != nil {
		return nil, err
	}
	var rows []workspaceMappingRow
	if err = tx.From(p.workspaceTable("workspace_permission_mappings")).Where(goqu.Ex{"workspace_id": a.WorkspaceID}).Order(goqu.C("id").Asc()).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list mappings: %w", err)
	}
	out := make([]service.PermissionMapping, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.record())
	}
	return out, nil
}
func (p *Postgres) SavePermissionMapping(ctx context.Context, m service.PermissionMapping) (*service.PermissionMapping, error) {
	if m.ID != "" || m.ClaimValue == "" || len(m.ClaimValue) > 1024 || !slices.Contains([]string{"roles", "groups", "permissions", "scope", "scopes"}, m.ClaimKind) {
		return nil, service.ErrWorkspaceConflict
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin mapping save: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.manage")
	if err != nil {
		return nil, err
	}
	if m.WorkspaceID != "" && m.WorkspaceID != a.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	m.WorkspaceID = a.WorkspaceID
	if _, err = p.workspacePermission(ctx, tx, a, m.PermissionID); err != nil {
		return nil, err
	}
	n, err := tx.From(p.workspaceTable("auth_identity_providers")).Where(goqu.Ex{"id": m.ProviderID, "enabled": true}).CountContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("validate mapping provider: %w", err)
	}
	if n != 1 {
		return nil, service.ErrAccessDenied
	}
	m.ID = ulid.Make().String()
	r := workspaceMappingRow{ID: m.ID, WorkspaceID: m.WorkspaceID, ProviderID: m.ProviderID, ClaimKind: m.ClaimKind, ClaimValue: m.ClaimValue, PermissionID: m.PermissionID}
	if _, err = tx.Insert(p.workspaceTable("workspace_permission_mappings")).Rows(r).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("save mapping: %w", err)
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit mapping: %w", err)
	}
	return &m, nil
}
func (p *Postgres) DeletePermissionMapping(ctx context.Context, id string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin mapping delete: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "permissions.manage")
	if err != nil {
		return err
	}
	var r workspaceMappingRow
	found, err := tx.From(p.workspaceTable("workspace_permission_mappings")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "id": id}).ScanStructContext(ctx, &r)
	if err != nil {
		return fmt.Errorf("read mapping: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	if _, err = p.workspacePermission(ctx, tx, a, r.PermissionID); err != nil {
		return err
	}
	if _, err = tx.Delete(p.workspaceTable("workspace_permission_mappings")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "id": id}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("delete mapping: %w", err)
	}
	if err = p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return err
	}
	return tx.Commit()
}
