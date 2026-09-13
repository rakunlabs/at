package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) GetGatewayMCPRoute(ctx context.Context, name, workspace string) (*service.MCPServer, error) {
	where := goqu.Ex{"name": name}
	if workspace == "" {
		where["public"] = true
	} else {
		where["workspace_id"] = workspace
	}
	var rows []struct {
		ID          string `db:"id"`
		WorkspaceID string `db:"workspace_id"`
		Name        string `db:"name"`
		Public      bool   `db:"public"`
	}
	if err := p.goqu.From(p.tableMCPServers).Select("id", "workspace_id", "name", "public").Where(where).Limit(2).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("resolve gateway MCP route: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	if len(rows) != 1 {
		return nil, service.ErrWorkspaceConflict
	}
	return &service.MCPServer{ID: rows[0].ID, WorkspaceID: rows[0].WorkspaceID, Name: rows[0].Name, Public: rows[0].Public}, nil
}

func (p *Postgres) checkMachineSubject(ctx context.Context, kind, id, action string) error {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: action, ResourceID: id}); err != nil {
		return err
	}
	principal, _, ok := service.ExecutionFromContext(ctx)
	if !ok || principal.ServiceID == "" {
		return service.ErrExecutionDenied
	}
	binding, err := p.GetExecutionServiceBinding(ctx, kind, id)
	if err != nil {
		return err
	}
	if binding == nil || binding.Revoked || binding.ID != principal.ServiceID || binding.Version != principal.ServiceVersion || binding.WorkspaceID != principal.WorkspaceID || binding.UserID != principal.UserID {
		return service.ErrExecutionDenied
	}
	return nil
}

func (p *Postgres) GetExecutionMCPServer(ctx context.Context, id string) (*service.MCPServer, error) {
	if err := p.checkMachineSubject(ctx, "mcp", id, "mcp_servers.use"); err != nil {
		return nil, err
	}
	principal, _, _ := service.ExecutionFromContext(ctx)
	var row mcpServerRow
	_, err := p.goqu.From(p.tableMCPServers).Select("id", "workspace_id", "name", "description", "public", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by").Where(goqu.Ex{"id": id, "workspace_id": principal.WorkspaceID}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("load execution MCP server: %w", err)
	}
	if row.ID == "" {
		return nil, nil
	}
	return mcpServerRowToRecord(row)
}

func (p *Postgres) GetExecutionBotConfig(ctx context.Context, id string) (*service.BotConfig, error) {
	if err := p.checkMachineSubject(ctx, "bot", id, "bots.use"); err != nil {
		return nil, err
	}
	principal, _, _ := service.ExecutionFromContext(ctx)
	query, _, err := p.goqu.From(p.tableBotConfigs).Select(botConfigColumns...).Where(goqu.Ex{"id": id, "workspace_id": principal.WorkspaceID}).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build execution bot config query: %w", err)
	}
	var row botConfigRow
	if err := scanBotConfigRow(p.db.QueryRowContext(ctx, query), &row); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("load execution bot config: %w", err)
	}
	return botConfigRowToRecord(row)
}
