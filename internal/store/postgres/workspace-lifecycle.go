package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.WorkspaceLifecycleStorer = (*Postgres)(nil)

// Child-first order includes composite-key and execution tables that are not
// represented by workspaceResourceTable. Deletion is one scoped transaction.
var workspaceDeletionTables = []string{
	"workflow_executions",
	"execution_service_bindings", "execution_provenance", "execution_policies",
	"workspace_provider_grants", "workspace_permission_mappings", "workspace_user_permissions", "workspace_user_denied",
	"workspace_permissions", "workspace_invitations", "workspace_memberships", "task_board_settings",
	"chat_messages", "chat_sessions", "token_usage", "tokens",
	"workflow_versions", "triggers", "workflows", "task_labels", "issue_comments", "organization_agents",
	"agent_budgets", "agent_usage", "agent_heartbeats", "heartbeat_runs", "wakeup_requests", "agent_runtime_state",
	"agent_task_sessions", "agent_config_revisions", "cost_events", "llm_calls", "approvals", "tasks", "projects", "goals", "labels",
	"agents", "organizations", "bot_configs", "providers", "skills", "skill_servers", "variables", "node_configs",
	"mcp_servers", "mcp_sets", "marketplaces", "marketplace_sources", "pack_sources", "guides", "connections", "connectors",
	"routing_profiles", "workspace_chat_presets", "trace_export_settings",
}

func (p *Postgres) DeleteWorkspace(ctx context.Context, confirmation string) (*service.WorkspaceDeletion, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin workspace deletion: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "workspace.archive")
	if err != nil {
		return nil, err
	}
	if !a.PlatformAdmin && a.Role != "owner" {
		return nil, service.ErrAccessDenied
	}
	if a.WorkspaceID == "legacy-default" {
		return nil, service.ErrWorkspaceConflict
	}
	var name string
	if _, err := tx.From(p.workspaceTable("workspaces")).Select("name").Where(goqu.Ex{"id": a.WorkspaceID}).ScanValContext(ctx, &name); err != nil {
		return nil, fmt.Errorf("read workspace confirmation: %w", err)
	}
	if confirmation != name {
		return nil, service.ErrWorkspaceConflict
	}
	deleted := &service.WorkspaceDeletion{WorkspaceID: a.WorkspaceID}
	if err := tx.From(p.workspaceTable("bot_configs")).Select("id").Where(goqu.Ex{"workspace_id": a.WorkspaceID}).ScanValsContext(ctx, &deleted.BotIDs); err != nil {
		return nil, fmt.Errorf("list deleted workspace bots: %w", err)
	}
	if err := tx.From(p.workspaceTable("tasks")).Select("id").Where(goqu.Ex{"workspace_id": a.WorkspaceID}).ScanValsContext(ctx, &deleted.TaskIDs); err != nil {
		return nil, fmt.Errorf("list deleted workspace tasks: %w", err)
	}
	for _, table := range workspaceDeletionTables {
		if _, err := tx.Delete(p.workspaceTable(table)).Where(goqu.Ex{"workspace_id": a.WorkspaceID}).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("delete workspace %s: %w", table, err)
		}
	}
	if _, err := tx.Delete(p.workspaceTable("workspaces")).Where(goqu.Ex{"id": a.WorkspaceID}).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("delete workspace: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace deletion: %w", err)
	}
	return deleted, nil
}

func (p *Postgres) workspacePreferenceActor(ctx context.Context, q workspaceReader) (service.AccessPrincipal, error) {
	a, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || a.UserID == "" || a.SessionID == "" {
		return a, service.ErrAccessDenied
	}
	_, err := p.workspaceIdentity(ctx, q, a.UserID, a.SessionID)
	return a, err
}

func (p *Postgres) GetWorkspacePreferences(ctx context.Context) (*service.WorkspacePreferences, error) {
	a, err := p.workspacePreferenceActor(ctx, p.goqu)
	if err != nil {
		return nil, err
	}
	v := &service.WorkspacePreferences{Mode: "default"}
	_, err = p.goqu.From(p.workspaceTable("workspace_preferences")).Select("mode", goqu.COALESCE(goqu.C("workspace_id"), "").As("workspace_id"), goqu.COALESCE(goqu.C("last_workspace_id"), "").As("last_workspace_id")).Where(goqu.Ex{"user_id": a.UserID}).ScanStructContext(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("read workspace preference: %w", err)
	}
	return v, nil
}

func (p *Postgres) SaveWorkspacePreferences(ctx context.Context, v service.WorkspacePreferences) (*service.WorkspacePreferences, error) {
	if v.Mode != "default" && v.Mode != "last_used" && v.Mode != "workspace" {
		return nil, service.ErrWorkspaceConflict
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin workspace preference: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspacePreferenceActor(ctx, tx)
	if err != nil {
		return nil, err
	}
	var selected any
	if v.Mode == "workspace" {
		if _, _, err := p.resolveWorkspaceAccess(ctx, tx, v.WorkspaceID, a.UserID, a.SessionID); err != nil {
			return nil, err
		}
		selected = v.WorkspaceID
	}
	row := goqu.Record{"user_id": a.UserID, "mode": v.Mode, "workspace_id": selected}
	if _, err := tx.Insert(p.workspaceTable("workspace_preferences")).Rows(row).OnConflict(goqu.DoUpdate("user_id", goqu.Record{"mode": v.Mode, "workspace_id": selected})).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("save workspace preference: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workspace preference: %w", err)
	}
	return p.GetWorkspacePreferences(ctx)
}

func (p *Postgres) SelectWorkspace(ctx context.Context, id string) error {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin workspace selection: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspacePreferenceActor(ctx, tx)
	if err != nil {
		return err
	}
	if _, _, err := p.resolveWorkspaceAccess(ctx, tx, id, a.UserID, a.SessionID); err != nil {
		return err
	}
	if _, err := tx.Insert(p.workspaceTable("workspace_preferences")).Rows(goqu.Record{"user_id": a.UserID, "last_workspace_id": id}).OnConflict(goqu.DoUpdate("user_id", goqu.Record{"last_workspace_id": id})).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("save workspace selection: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workspace selection: %w", err)
	}
	return nil
}
