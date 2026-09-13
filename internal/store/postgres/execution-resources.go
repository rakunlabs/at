package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) ResolveExecutionResource(ctx context.Context, kind, key string) (service.AccessResource, error) {
	principal, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || principal.WorkspaceID == "" || key == "" {
		return service.AccessResource{}, service.ErrAccessDenied
	}
	table, alias := "", ""
	switch kind {
	case "providers":
		table, alias = "providers", "key"
	case "skills":
		table, alias = "skills", "name"
	case "variables":
		table, alias = "variables", "key"
	case "agents":
		table = "agents"
	case "workflows":
		table, alias = "workflows", "name"
	case "connections":
		table = "connections"
	case "tasks":
		table = "tasks"
	case "triggers":
		table = "triggers"
	case "bots":
		table = "bot_configs"
	case "organizations":
		table = "organizations"
	case "chats":
		table = "chat_sessions"
	case "node_configs":
		table = "node_configs"
	case "mcp":
		table, alias = "mcp_sets", "name"
	case "mcp_servers":
		table, alias = "mcp_servers", "name"
	default:
		return service.AccessResource{}, service.ErrAccessDenied
	}
	where := goqu.Or(goqu.Ex{"id": key})
	if alias != "" {
		where = goqu.Or(goqu.Ex{"id": key}, goqu.Ex{alias: key})
	}
	if kind == "mcp" && (strings.HasPrefix(key, "https://") || strings.HasPrefix(key, "http://")) {
		urls, _ := json.Marshal([]string{key})
		upstreams, _ := json.Marshal([]map[string]string{{"url": key}})
		where = goqu.Or(goqu.L("urls @> ?::jsonb", string(urls)), goqu.L("config->'mcp_upstreams' @> ?::jsonb", string(upstreams)))
	}
	var rows []struct {
		ID          string `db:"id"`
		WorkspaceID string `db:"workspace_id"`
	}
	err := p.goqu.From(p.executionTable(table)).Select("id", "workspace_id").Where(goqu.Ex{"workspace_id": principal.WorkspaceID}, where).Limit(2).ScanStructsContext(ctx, &rows)
	if err != nil {
		return service.AccessResource{}, fmt.Errorf("resolve execution resource: %w", err)
	}
	if len(rows) != 1 {
		return service.AccessResource{}, service.ErrAccessDenied
	}
	return service.AccessResource{Kind: kind, ID: rows[0].ID, WorkspaceID: rows[0].WorkspaceID}, nil
}
