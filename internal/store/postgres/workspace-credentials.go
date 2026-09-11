package postgres

import (
	"context"
	"fmt"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.WorkspaceCredentialStorer = (*Postgres)(nil)

func (p *Postgres) ResolveVariableForUse(ctx context.Context, key string) (*service.Variable, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := businessPredicate(a, "variables.use", "id")
	if err != nil {
		return nil, err
	}
	var row variableRow
	found, err := p.goqu.From(p.tableVariables).Where(scope, goqu.C("key").Eq(key)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve variable for use: %w", err)
	}
	if !found {
		return nil, nil
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	return variableRowToRecord(row, p.encKey)
}
func (p *Postgres) ResolveConnectionForUse(ctx context.Context, id string) (*service.Connection, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := businessPredicate(a, "connections.use", "id")
	if err != nil {
		return nil, err
	}
	var row connectionRow
	found, err := p.goqu.From(p.tableConnections).Where(scope, goqu.C("id").Eq(id)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve connection for use: %w", err)
	}
	if !found {
		return nil, nil
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	return connectionRowToRecord(row, p.encKey)
}
func (p *Postgres) ResolveNodeConfigForUse(ctx context.Context, id string) (*service.NodeConfig, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := businessPredicate(a, "node_configs.use", "id")
	if err != nil {
		return nil, err
	}
	var row nodeConfigRow
	found, err := p.goqu.From(p.tableNodeConfigs).Where(scope, goqu.C("id").Eq(id)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve node config for use: %w", err)
	}
	if !found {
		return nil, nil
	}
	p.encKeyMu.RLock()
	defer p.encKeyMu.RUnlock()
	return nodeConfigRowToRecord(row, p.encKey)
}

func (p *Postgres) ResolveMCPSetForUse(ctx context.Context, name string) (*service.MCPSet, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := businessPredicate(a, "mcp.use", "id")
	if err != nil {
		return nil, err
	}
	var row mcpSetRow
	found, err := p.goqu.From(p.tableMCPSets).Where(scope, goqu.C("name").Eq(name)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve MCP set for use: %w", err)
	}
	if !found {
		return nil, nil
	}
	return mcpSetRowToRecord(row)
}
func (p *Postgres) ResolveMCPServerForUse(ctx context.Context, name string) (*service.MCPServer, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := businessPredicate(a, "mcp.use", "id")
	if err != nil {
		return nil, err
	}
	var row mcpServerRow
	found, err := p.goqu.From(p.tableMCPServers).Where(scope, goqu.C("name").Eq(name)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve MCP server for use: %w", err)
	}
	if !found {
		return nil, nil
	}
	return mcpServerRowToRecord(row)
}
func (p *Postgres) ResolveBotForUse(ctx context.Context, id string) (*service.BotConfig, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	scope, err := businessPredicate(a, "bots.use", "id")
	if err != nil {
		return nil, err
	}
	var row botConfigRow
	found, err := p.goqu.From(p.tableBotConfigs).Where(scope, goqu.C("id").Eq(id)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("resolve bot for use: %w", err)
	}
	if !found {
		return nil, nil
	}
	return botConfigRowToRecord(row)
}
