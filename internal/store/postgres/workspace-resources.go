package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.WorkspaceResourceStorer = (*Postgres)(nil)

// Only these tables have additive ownership and an immutable ID column. Names
// are not SQL supplied by callers. Composite-key resources require their parent.
func workspaceResourceTable(kind string) string {
	switch kind {
	case "organizations", "agents", "projects", "goals", "tasks", "labels", "approvals", "workflows", "workflow_versions", "triggers", "skills", "skill_servers", "variables", "connections", "bots", "providers", "tokens", "node_configs", "chat_sessions", "chat_messages", "mcp_servers", "mcp_sets", "marketplaces", "marketplace_sources", "pack_sources", "guides", "connectors", "llm_calls", "heartbeat_runs", "wakeup_requests", "agent_config_revisions", "cost_events":
		if kind == "bots" {
			return "bot_configs"
		}
		return kind
	case "comments":
		return "issue_comments"
	case "permissions":
		return "workspace_permissions"
	}
	return ""
}

func (p *Postgres) workspaceResource(ctx context.Context, q workspaceReader, actor service.AccessPrincipal, kind, id string) (*service.AccessResource, error) {
	table := workspaceResourceTable(kind)
	if table == "" || id == "" || actor.WorkspaceID == "" {
		return nil, service.ErrAccessResourceNotFound
	}
	var row struct {
		ID          string `db:"id"`
		WorkspaceID string `db:"workspace_id"`
		OwnerID     string `db:"owner_id"`
	}
	selects := []any{goqu.C("id"), goqu.C("workspace_id"), goqu.V("").As("owner_id")}
	where := goqu.Ex{"id": id, "workspace_id": actor.WorkspaceID}
	if kind == "connectors" {
		selects[0] = goqu.C("slug").As("id")
		delete(where, "id")
		where["slug"] = id
	}
	found, err := q.From(p.workspaceTable(table)).Select(selects...).Where(where).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace resource: %w", err)
	}
	if !found {
		return nil, service.ErrAccessResourceNotFound
	}
	return &service.AccessResource{Kind: kind, ID: row.ID, WorkspaceID: row.WorkspaceID, OwnerID: row.OwnerID}, nil
}

func (p *Postgres) GetWorkspaceAccessResource(ctx context.Context, kind, id string) (*service.AccessResource, error) {
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin resource resolution: %w", err)
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "workspace.read")
	if err != nil {
		return nil, err
	}
	return p.workspaceResource(ctx, tx, a, kind, id)
}

func (p *Postgres) validatePermissionResources(ctx context.Context, tx *goqu.TxDatabase, a service.AccessPrincipal, b service.PermissionBundle) error {
	for capability, ids := range b.ResourceIDs {
		kind, _, _ := strings.Cut(capability, ".")
		for _, id := range ids {
			kinds := []string{kind}
			switch kind {
			case "mcp":
				kinds = []string{"mcp_sets", "mcp_servers"}
			case "packs":
				kinds = []string{"pack_sources", "marketplaces", "marketplace_sources", "guides"}
			case "credentials":
				kinds = []string{"providers", "connections", "variables", "node_configs", "bots", "mcp_sets", "mcp_servers"}
			case "models":
				kinds = []string{"providers"}
			}
			found := false
			for _, candidate := range kinds {
				if _, err := p.workspaceResource(ctx, tx, a, candidate, id); err == nil {
					found = true
					break
				} else if !errors.Is(err, service.ErrAccessResourceNotFound) {
					return err
				}
			}
			if !found && kind == "models" {
				n, err := tx.From(p.workspaceTable("workspace_provider_grants")).Where(goqu.Ex{"workspace_id": a.WorkspaceID, "provider_id": id}).CountContext(ctx)
				if err != nil {
					return fmt.Errorf("validate model binding selector: %w", err)
				}
				found = n == 1
				if !found {
					n, err := tx.From(p.tableProviders).Where(goqu.Ex{"workspace_id": "legacy-default", "id": id}, goqu.L("config->>'shared_with_all_workspaces' = 'true'")).CountContext(ctx)
					if err != nil {
						return fmt.Errorf("validate shared model selector: %w", err)
					}
					found = n == 1
				}
			}
			if !found {
				return service.ErrAccessResourceNotFound
			}
		}
	}
	return nil
}
