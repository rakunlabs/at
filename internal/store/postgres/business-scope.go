package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) businessPrincipal(ctx context.Context) (service.AccessPrincipal, error) {
	if a, ok := service.AccessPrincipalFromContext(ctx); ok {
		live, _, err := p.ResolveWorkspaceAccess(ctx, a.WorkspaceID, a.UserID, a.SessionID)
		return live, err
	}
	if service.LegacyWorkspaceAccessFromContext(ctx) {
		return p.legacyBusinessPrincipal(ctx, p.goqu, false)
	}
	return service.AccessPrincipal{}, service.ErrWorkspaceRequired
}

// Each physical table has a declared capability and selector column. The caller's
// filter is always ANDed outside this mandatory workspace/capability predicate.
func (p *Postgres) businessTablePolicy(table interface{}) (string, string, bool) {
	var name string
	switch t := table.(type) {
	case exp.IdentifierExpression:
		name = t.GetTable()
		if name == "" {
			name, _ = t.GetCol().(string)
		}
	case string:
		name = t
	default:
		return "", "", false
	}
	base := p.tableAuthUsers.GetTable()
	if base == "" {
		base, _ = p.tableAuthUsers.GetCol().(string)
	}
	name = strings.TrimPrefix(name, strings.TrimSuffix(base, "auth_users"))
	switch name {
	case "providers":
		return "providers.read", "id", true
	case "tokens":
		return "tokens.read", "id", true
	case "token_usage":
		return "tokens.read", "token_id", true
	case "organizations", "agents", "projects", "goals", "tasks", "labels", "approvals", "workflows", "skills", "variables", "connections":
		return name + ".read", "id", true
	case "issue_comments":
		return "comments.read", "id", true
	case "workflow_versions":
		return "workflows.read", "workflow_id", true
	case "triggers":
		return "workflows.read", "workflow_id", true
	case "node_configs":
		return "workflows.read", "id", true
	case "skill_servers":
		return "skills.read", "id", true
	case "bot_configs":
		return "bots.read", "id", true
	case "mcp_servers", "mcp_sets":
		return "mcp.read", "id", true
	case "marketplaces", "marketplace_sources", "pack_sources", "guides":
		return "packs.read", "id", true
	case "connectors":
		return "connections.read", "slug", true
	case "llm_calls":
		return "traces.read", "id", true
	case "cost_events":
		return "usage.read", "id", true
	case "chat_sessions":
		return "agents.read", "id", true
	case "chat_messages":
		return "agents.read", "session_id", true
	case "heartbeat_runs", "wakeup_requests", "agent_config_revisions":
		return "agents.read", "agent_id", true
	case "agent_budgets", "agent_usage", "agent_heartbeats", "agent_runtime_state", "agent_task_sessions":
		return "agents.read", "agent_id", true
	case "organization_agents":
		return "agents.read", "agent_id", true
	case "task_labels":
		return "tasks.read", "task_id", true
	// The board is a view over tasks, so it rides on the task capabilities
	// rather than inventing a capability kind that existing permission bundles
	// would not carry. Its primary key is the workspace itself.
	case "task_board_settings":
		return "tasks.read", "workspace_id", true
	case "model_pricing", "feature_settings", "user_preferences":
		return "platform.manage", "", false
	}
	return "", "", false
}

func businessPredicate(a service.AccessPrincipal, capability, idColumn string) (exp.Expression, error) {
	if a.WorkspaceID == "" {
		return nil, service.ErrWorkspaceRequired
	}
	workspace := goqu.C("workspace_id").Eq(a.WorkspaceID)
	if a.Allows(capability, service.AccessResource{WorkspaceID: a.WorkspaceID}) {
		return workspace, nil
	}
	var selectors []exp.Expression
	for _, g := range a.Grants {
		if g.Capability != capability || g.ResourceIDs == nil || g.PathPatterns != nil || service.ValidateAccessGrant(g) != nil {
			continue
		}
		for _, id := range g.ResourceIDs {
			if a.Allows(capability, service.AccessResource{WorkspaceID: a.WorkspaceID, ID: id}) {
				selectors = append(selectors, goqu.C(idColumn).Eq(id))
			}
		}
	}
	if len(selectors) == 0 {
		return nil, service.ErrAccessDenied
	}
	return goqu.And(workspace, goqu.Or(selectors...)), nil
}

func (p *Postgres) businessReadScope(ctx context.Context, table interface{}) (exp.Expression, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	cap, id, scoped := p.businessTablePolicy(table)
	if cap == "" {
		return nil, service.ErrAccessDenied
	}
	if !scoped {
		if a.PlatformAdmin {
			return goqu.L("TRUE"), nil
		}
		return nil, service.ErrAccessDenied
	}
	return businessPredicate(a, cap, id)
}

type businessWrite struct {
	tx        *goqu.TxDatabase
	actor     service.AccessPrincipal
	predicate exp.Expression
}

func (p *Postgres) beginBusinessWrite(ctx context.Context, table interface{}, capability, id string) (*businessWrite, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin scoped write: %w", err)
	}
	var a service.AccessPrincipal
	if _, ok := service.AccessPrincipalFromContext(ctx); !ok && service.LegacyWorkspaceAccessFromContext(ctx) {
		a, err = p.legacyBusinessPrincipal(ctx, tx, true)
	} else {
		a, err = p.workspaceActor(ctx, tx, "workspace.read")
	}
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if !a.Allows(capability, service.AccessResource{WorkspaceID: a.WorkspaceID, ID: id}) {
		tx.Rollback()
		return nil, service.ErrAccessDenied
	}
	return &businessWrite{tx: tx, actor: a, predicate: goqu.C("workspace_id").Eq(a.WorkspaceID)}, nil
}

func (p *Postgres) legacyBusinessPrincipal(ctx context.Context, q workspaceReader, lock bool) (service.AccessPrincipal, error) {
	var row struct {
		ID               string `db:"id"`
		ExecutionEnabled bool   `db:"execution_enabled"`
	}
	ds := q.From(p.workspaceTable("workspaces")).Where(goqu.Ex{"id": "legacy-default", "archived": false})
	if lock {
		ds = ds.ForUpdate(goqu.Wait)
	}
	found, err := ds.ScanStructContext(ctx, &row)
	if err != nil {
		return service.AccessPrincipal{}, fmt.Errorf("resolve legacy workspace: %w", err)
	}
	if !found {
		return service.AccessPrincipal{}, service.ErrAccessDenied
	}
	return service.AccessPrincipal{WorkspaceID: row.ID, PlatformAdmin: true, ExecutionDisabled: !row.ExecutionEnabled}, nil
}

func (p *Postgres) businessReference(ctx context.Context, w *businessWrite, table interface{}, column, id string) error {
	if id == "" {
		return nil
	}
	var foundID string
	found, err := w.tx.From(table).Select(column).Where(goqu.C("workspace_id").Eq(w.actor.WorkspaceID), goqu.C(column).Eq(id)).ForKeyShare(goqu.Wait).ScanValContext(ctx, &foundID)
	if err != nil {
		return fmt.Errorf("validate scoped reference: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

func (p *Postgres) beginBusinessNamedWrite(ctx context.Context, table interface{}, column, key, capability string) (*businessWrite, error) {
	w, err := p.beginBusinessWrite(ctx, table, "workspace.read", "")
	if err != nil {
		return nil, err
	}
	var id string
	found, err := w.tx.From(table).Select("id").Where(w.predicate, goqu.C(column).Eq(key)).ForUpdate(goqu.Wait).ScanValContext(ctx, &id)
	if err != nil {
		w.tx.Rollback()
		return nil, fmt.Errorf("lock named resource: %w", err)
	}
	if !found {
		w.tx.Rollback()
		return nil, service.ErrAccessResourceNotFound
	}
	if !w.actor.Allows(capability, service.AccessResource{WorkspaceID: w.actor.WorkspaceID, ID: id}) {
		w.tx.Rollback()
		return nil, service.ErrAccessDenied
	}
	return w, nil
}
