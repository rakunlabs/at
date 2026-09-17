package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) workspaceTable(suffix string) exp.IdentifierExpression {
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

type workspaceReader interface {
	From(...interface{}) *goqu.SelectDataset
}

func (p *Postgres) workspaceIdentity(ctx context.Context, q workspaceReader, userID, sessionID string) (*authUserRow, error) {
	var u authUserRow
	found, err := q.From(p.tableAuthUsers).Where(goqu.Ex{"id": userID, "disabled": false}).ScanStructContext(ctx, &u)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace user: %w", err)
	}
	if !found {
		return nil, service.ErrAccessDenied
	}
	if sessionID != "" {
		n, err := q.From(p.tableAuthSessions).Where(goqu.Ex{"hash": sessionID, "user_id": userID, "version": u.SessionVersion}, goqu.C("expires_at").Gt(time.Now())).CountContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace session: %w", err)
		}
		if n != 1 {
			return nil, service.ErrAccessDenied
		}
	}
	return &u, nil
}

func (p *Postgres) ResolveWorkspaceAccess(ctx context.Context, workspaceID, userID, sessionID string) (service.AccessPrincipal, service.EffectiveAccess, error) {
	if workspaceID == "legacy-default" {
		if err := p.ensureDefaultWorkspaceOwner(ctx, userID, sessionID); err != nil {
			return service.AccessPrincipal{}, service.EffectiveAccess{}, err
		}
	}
	principal, report, err := p.resolveWorkspaceAccessSnapshot(ctx, workspaceID, userID, sessionID)
	// Admission is attempted only on the denied path, so a member's request pays
	// nothing for it, and only after the read transaction has released its share
	// lock on the workspace row that admission needs to take exclusively.
	if errors.Is(err, service.ErrAccessDenied) {
		if admitted, e := p.ensureMappedMemberships(ctx, userID, sessionID, workspaceID); e == nil && admitted {
			return p.resolveWorkspaceAccessSnapshot(ctx, workspaceID, userID, sessionID)
		}
	}
	return principal, report, err
}

func (p *Postgres) resolveWorkspaceAccessSnapshot(ctx context.Context, workspaceID, userID, sessionID string) (service.AccessPrincipal, service.EffectiveAccess, error) {
	// A transaction supplies one coherent snapshot for the effective report.
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return service.AccessPrincipal{}, service.EffectiveAccess{}, fmt.Errorf("begin workspace resolution: %w", err)
	}
	defer tx.Rollback()
	var w service.Workspace
	found, err := tx.From(p.workspaceTable("workspaces")).Select("id", "name", "archived", "execution_enabled", "created_at").Where(goqu.Ex{"id": workspaceID, "archived": false}).ForShare(goqu.Wait).ScanStructContext(ctx, &w)
	if err != nil {
		return service.AccessPrincipal{}, service.EffectiveAccess{}, fmt.Errorf("lock workspace resolution: %w", err)
	}
	if !found {
		return service.AccessPrincipal{}, service.EffectiveAccess{}, service.ErrAccessDenied
	}
	principal, report, err := p.resolveWorkspaceAccess(ctx, tx, workspaceID, userID, sessionID)
	if err != nil {
		return principal, report, err
	}
	return principal, report, tx.Commit()
}

// Bootstrap can occur after migrations. Only a live installation administrator
// gets this compatibility ownership; ordinary accounts never auto-join.
func (p *Postgres) ensureDefaultWorkspaceOwner(ctx context.Context, userID, sessionID string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin default owner: %w", err)
	}
	defer tx.Rollback()
	u, err := p.workspaceIdentity(ctx, tx, userID, sessionID)
	if err != nil {
		return err
	}
	if !u.Admin {
		return nil
	}
	var id string
	found, err := tx.From(p.workspaceTable("workspaces")).Select("id").Where(goqu.Ex{"id": "legacy-default", "archived": false}).ForUpdate(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		return fmt.Errorf("lock default workspace: %w", err)
	}
	if !found {
		return nil
	}
	_, err = tx.Insert(p.workspaceTable("workspace_memberships")).Rows(goqu.Record{"workspace_id": id, "user_id": u.ID, "role": "owner", "status": "active"}).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("create default owner: %w", err)
	}
	return tx.Commit()
}

// ensureMappedMemberships admits an external identity that matches a mapping
// carrying an admission role. Scope it to one workspace for a request that named
// one, or pass an empty ID to sweep every workspace during discovery — without
// the sweep a first-time single sign-on user would see an empty workspace list
// and never send the header that would admit them.
//
// Deliberate limits: a workspace that already has ANY membership row for the
// user is skipped, so a revoked membership is never resurrected and an
// administrator's explicit role is never overwritten; archived workspaces are
// skipped; disabled providers do not match; and the role is re-validated here,
// not trusted from the row.
func (p *Postgres) ensureMappedMemberships(ctx context.Context, userID, sessionID, workspaceID string) (bool, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin claim admission: %w", err)
	}
	defer tx.Rollback()
	u, err := p.workspaceIdentity(ctx, tx, userID, sessionID)
	if err != nil {
		return false, err
	}
	var links []service.AuthIdentityLink
	if err = tx.From(p.workspaceTable("auth_identity_links")).Where(goqu.Ex{"user_id": u.ID}).ScanStructsContext(ctx, &links); err != nil {
		return false, fmt.Errorf("read admission identities: %w", err)
	}
	if len(links) == 0 {
		return false, nil
	}
	where := []exp.Expression{goqu.C("admit_role").Neq("")}
	if workspaceID != "" {
		where = append(where, goqu.C("workspace_id").Eq(workspaceID))
	}
	var mappings []workspaceMappingRow
	if err = tx.From(p.workspaceTable("workspace_permission_mappings")).Where(where...).Order(goqu.C("id").Asc()).ScanStructsContext(ctx, &mappings); err != nil {
		return false, fmt.Errorf("read admission mappings: %w", err)
	}
	if len(mappings) == 0 {
		return false, nil
	}
	enabled := map[string]bool{}
	var providers []string
	if err = tx.From(p.workspaceTable("auth_identity_providers")).Select("id").Where(goqu.Ex{"enabled": true}).ScanValsContext(ctx, &providers); err != nil {
		return false, fmt.Errorf("read admission providers: %w", err)
	}
	for _, id := range providers {
		enabled[id] = true
	}
	// Highest matching role per workspace, in a stable workspace order.
	admit := map[string]workspaceMappingRow{}
	var order []string
	for _, m := range mappings {
		if !enabled[m.ProviderID] || !service.ValidWorkspaceAdmissionRole(m.AdmitRole) || m.AdmitRole == "" {
			continue
		}
		matched := false
		for _, link := range links {
			if link.ProviderID == m.ProviderID && workspaceClaimMatches(link, m.ClaimKind, m.ClaimValue) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		best, seen := admit[m.WorkspaceID]
		if !seen {
			order = append(order, m.WorkspaceID)
		}
		if !seen || service.WorkspaceRoleRank(m.AdmitRole) > service.WorkspaceRoleRank(best.AdmitRole) {
			admit[m.WorkspaceID] = m
		}
	}
	sort.Strings(order)
	admitted := false
	for _, id := range order {
		m := admit[id]
		var locked string
		found, e := tx.From(p.workspaceTable("workspaces")).Select("id").Where(goqu.Ex{"id": id, "archived": false}).ForUpdate(goqu.Wait).ScanValContext(ctx, &locked)
		if e != nil {
			return false, fmt.Errorf("lock admission workspace: %w", e)
		}
		if !found {
			continue
		}
		n, e := tx.From(p.workspaceTable("workspace_memberships")).Where(goqu.Ex{"workspace_id": id, "user_id": u.ID}).CountContext(ctx)
		if e != nil {
			return false, fmt.Errorf("read admission membership: %w", e)
		}
		if n > 0 {
			continue
		}
		if _, e = tx.Insert(p.workspaceTable("workspace_memberships")).Rows(goqu.Record{"workspace_id": id, "user_id": u.ID, "role": m.AdmitRole, "status": "active"}).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx); e != nil {
			return false, fmt.Errorf("admit workspace member: %w", e)
		}
		admitted = true
		slog.Info("workspace claim admission", "workspace_id", id, "user_id", u.ID, "role", m.AdmitRole, "provider_id", m.ProviderID, "mapping_id", m.ID, "claim_kind", m.ClaimKind)
	}
	if !admitted {
		return false, nil
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("commit claim admission: %w", err)
	}
	return true, nil
}

func (p *Postgres) resolveWorkspaceAccess(ctx context.Context, q workspaceReader, workspaceID, userID, sessionID string) (service.AccessPrincipal, service.EffectiveAccess, error) {
	var principal service.AccessPrincipal
	var report service.EffectiveAccess
	if workspaceID == "" {
		return principal, report, service.ErrWorkspaceRequired
	}
	u, err := p.workspaceIdentity(ctx, q, userID, sessionID)
	if err != nil {
		return principal, report, err
	}
	var w service.Workspace
	found, err := q.From(p.workspaceTable("workspaces")).Select("id", "name", "archived", "execution_enabled", "created_at").Where(goqu.Ex{"id": workspaceID, "archived": false}).ScanStructContext(ctx, &w)
	if err != nil {
		return principal, report, fmt.Errorf("resolve workspace: %w", err)
	}
	if !found {
		return principal, report, service.ErrAccessDenied
	}
	var m service.WorkspaceMembership
	found, err = q.From(p.workspaceTable("workspace_memberships")).Where(goqu.Ex{"workspace_id": workspaceID, "user_id": userID, "status": "active"}).ScanStructContext(ctx, &m)
	if err != nil {
		return principal, report, fmt.Errorf("resolve membership: %w", err)
	}
	if !found && !u.Admin {
		return principal, report, service.ErrAccessDenied
	}
	principal = service.AccessPrincipal{UserID: userID, WorkspaceID: workspaceID, SessionID: sessionID, Role: m.Role, MembershipVersion: m.Version, PlatformAdmin: u.Admin}
	principal.ExecutionDisabled = !w.ExecutionEnabled
	report = service.EffectiveAccess{UserID: userID, WorkspaceID: workspaceID, Role: m.Role, MembershipVersion: m.Version, Capabilities: []string{}, Sources: []service.AccessGrantSource{}, Denied: []string{}, ExecutionEnabled: w.ExecutionEnabled}
	for _, g := range service.WorkspaceRoleGrants(m.Role) {
		report.Sources = append(report.Sources, service.AccessGrantSource{AccessGrant: g, Source: "role:" + m.Role})
	}
	var ids []string
	if err := q.From(p.workspaceTable("workspace_user_permissions")).Select("permission_id").Where(goqu.Ex{"workspace_id": workspaceID, "user_id": userID}).ScanValsContext(ctx, &ids); err != nil {
		return principal, report, fmt.Errorf("resolve assigned permissions: %w", err)
	}
	bundles, err := p.workspaceBundles(ctx, q, workspaceID)
	if err != nil {
		return principal, report, err
	}
	for _, b := range bundles {
		if slices.Contains(ids, b.ID) {
			gs, e := b.Grants()
			if e != nil {
				return principal, report, e
			}
			for _, g := range gs {
				report.Sources = append(report.Sources, service.AccessGrantSource{AccessGrant: g, Source: "direct", PermissionID: b.ID})
			}
		}
	}
	var mappings []workspaceMappingRow
	if err := q.From(p.workspaceTable("workspace_permission_mappings")).Where(goqu.Ex{"workspace_id": workspaceID}).ScanStructsContext(ctx, &mappings); err != nil {
		return principal, report, fmt.Errorf("resolve permission mappings: %w", err)
	}
	if len(mappings) > 0 {
		var links []service.AuthIdentityLink
		if err := q.From(p.workspaceTable("auth_identity_links")).Where(goqu.Ex{"user_id": userID}).ScanStructsContext(ctx, &links); err != nil {
			return principal, report, fmt.Errorf("resolve mapped identity: %w", err)
		}
		for _, mapping := range mappings {
			var enabled bool
			if _, err := q.From(p.workspaceTable("auth_identity_providers")).Select("enabled").Where(goqu.Ex{"id": mapping.ProviderID}).ScanValContext(ctx, &enabled); err != nil {
				return principal, report, fmt.Errorf("resolve mapping provider: %w", err)
			}
			if !enabled {
				continue
			}
			matched := false
			for _, link := range links {
				if link.ProviderID == mapping.ProviderID && workspaceClaimMatches(link, mapping.ClaimKind, mapping.ClaimValue) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			for _, b := range bundles {
				if b.ID != mapping.PermissionID {
					continue
				}
				gs, e := b.Grants()
				if e != nil {
					return principal, report, e
				}
				for _, g := range gs {
					report.Sources = append(report.Sources, service.AccessGrantSource{AccessGrant: g, Source: "mapping", PermissionID: b.ID, ProviderID: mapping.ProviderID, MappingID: mapping.ID})
				}
			}
		}
	}
	if err := q.From(p.workspaceTable("workspace_user_denied")).Select("capability").Where(goqu.Ex{"workspace_id": workspaceID, "user_id": userID}).Order(goqu.C("capability").Asc()).ScanValsContext(ctx, &report.Denied); err != nil {
		return principal, report, fmt.Errorf("resolve denied capabilities: %w", err)
	}
	principal.Denied = slices.Clone(report.Denied)
	report.Patterns = make(map[string][]string)
	for _, source := range report.Sources {
		principal.Grants = append(principal.Grants, source.AccessGrant)
		patterns, exists := report.Patterns[source.Capability]
		if !exists || source.PathPatterns == nil {
			report.Patterns[source.Capability] = slices.Clone(source.PathPatterns)
		} else if patterns != nil {
			for _, pattern := range source.PathPatterns {
				if !slices.Contains(patterns, pattern) {
					patterns = append(patterns, pattern)
				}
			}
			report.Patterns[source.Capability] = patterns
		}
		if !slices.Contains(report.Denied, source.Capability) && !slices.Contains(report.Capabilities, source.Capability) {
			report.Capabilities = append(report.Capabilities, source.Capability)
		}
	}
	if u.Admin {
		for _, c := range service.AccessCapabilities() {
			report.Patterns[c.Key] = nil
			if !slices.Contains(report.Capabilities, c.Key) {
				report.Capabilities = append(report.Capabilities, c.Key)
			}
		}
	}
	if !w.ExecutionEnabled {
		report.Capabilities = slices.DeleteFunc(report.Capabilities, func(cap string) bool {
			return cap == "models.use" || strings.HasSuffix(cap, ".execute")
		})
	}
	sort.Strings(report.Capabilities)
	return principal, report, nil
}

func workspaceClaimMatches(link service.AuthIdentityLink, kind, value string) bool {
	var raw struct {
		ProviderID string                     `json:"provider_id"`
		Roles      []string                   `json:"roles"`
		Scopes     []string                   `json:"scopes"`
		Claims     map[string]json.RawMessage `json:"claims"`
	}
	if json.Unmarshal(link.AssertedPermissions, &raw) != nil || raw.ProviderID != link.ProviderID {
		return false
	}
	if kind == "roles" && slices.Contains(raw.Roles, value) || kind == "scopes" && slices.Contains(raw.Scopes, value) {
		return true
	}
	var values []string
	if json.Unmarshal(raw.Claims[kind], &values) == nil {
		return slices.Contains(values, value)
	}
	if kind == "scope" {
		var scope string
		if json.Unmarshal(raw.Claims[kind], &scope) == nil {
			return slices.Contains(strings.Fields(scope), value)
		}
	}
	return false
}

func (p *Postgres) workspaceActor(ctx context.Context, tx *goqu.TxDatabase, capability string) (service.AccessPrincipal, error) {
	actor, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || actor.WorkspaceID == "" {
		return actor, service.ErrWorkspaceRequired
	}
	var w service.Workspace
	found, err := tx.From(p.workspaceTable("workspaces")).Select("id", "name", "archived", "execution_enabled", "created_at").Where(goqu.Ex{"id": actor.WorkspaceID, "archived": false}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &w)
	if err != nil {
		return actor, fmt.Errorf("lock workspace policy: %w", err)
	}
	if !found {
		return actor, service.ErrAccessDenied
	}
	live, _, err := p.resolveWorkspaceAccess(ctx, tx, actor.WorkspaceID, actor.UserID, actor.SessionID)
	if err != nil {
		return live, err
	}
	if !live.Allows(capability, service.AccessResource{WorkspaceID: live.WorkspaceID}) {
		return live, service.ErrAccessDenied
	}
	return live, nil
}
