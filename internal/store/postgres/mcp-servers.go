package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

type mcpServerRow struct {
	ID        string          `db:"id"`
	Name      string          `db:"name"`
	Config    json.RawMessage `db:"config"`
	CreatedAt time.Time       `db:"created_at"`
	UpdatedAt time.Time       `db:"updated_at"`
	CreatedBy sql.NullString  `db:"created_by"`
	UpdatedBy sql.NullString  `db:"updated_by"`
}

func (p *Postgres) ListMCPServers(ctx context.Context, q *query.Query) (*service.ListResult[service.MCPServer], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableMCPServers, q, "id", "name", "config", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list mcp servers query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list mcp servers: %w", err)
	}
	defer rows.Close()

	var items []service.MCPServer
	for rows.Next() {
		var row mcpServerRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan mcp server row: %w", err)
		}

		rec, err := mcpServerRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.MCPServer]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetMCPServer(ctx context.Context, id string) (*service.MCPServer, error) {
	query, _, err := p.goqu.From(p.tableMCPServers).
		Select("id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp server query: %w", err)
	}

	var row mcpServerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp server %q: %w", id, err)
	}

	return mcpServerRowToRecord(row)
}

func (p *Postgres) GetMCPServerByName(ctx context.Context, name string) (*service.MCPServer, error) {
	query, _, err := p.goqu.From(p.tableMCPServers).
		Select("id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp server by name query: %w", err)
	}

	var row mcpServerRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp server by name %q: %w", name, err)
	}

	return mcpServerRowToRecord(row)
}

func (p *Postgres) CreateMCPServer(ctx context.Context, s service.MCPServer) (*service.MCPServer, error) {
	configJSON, err := json.Marshal(s.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableMCPServers).Rows(
		goqu.Record{
			"id":         id,
			"name":       s.Name,
			"config":     configJSON,
			"created_at": now,
			"updated_at": now,
			"created_by": s.CreatedBy,
			"updated_by": s.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert mcp server query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create mcp server %q: %w", s.Name, err)
	}

	return &service.MCPServer{
		ID:        id,
		Name:      s.Name,
		Config:    s.Config,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
		CreatedBy: s.CreatedBy,
		UpdatedBy: s.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateMCPServer(ctx context.Context, id string, s service.MCPServer) (*service.MCPServer, error) {
	configJSON, err := json.Marshal(s.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server config: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableMCPServers).Set(
		goqu.Record{
			"name":       s.Name,
			"config":     configJSON,
			"updated_at": now,
			"updated_by": s.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update mcp server query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update mcp server %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return p.GetMCPServer(ctx, id)
}

func (p *Postgres) DeleteMCPServer(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableMCPServers).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete mcp server query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete mcp server %q: %w", id, err)
	}

	return nil
}

func mcpServerRowToRecord(row mcpServerRow) (*service.MCPServer, error) {
	var cfg service.MCPServerConfig
	if len(row.Config) > 0 {
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal mcp server config for %q: %w", row.ID, err)
		}
	}

	return &service.MCPServer{
		ID:        row.ID,
		Name:      row.Name,
		Config:    cfg,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
		CreatedBy: row.CreatedBy.String,
		UpdatedBy: row.UpdatedBy.String,
	}, nil
}
