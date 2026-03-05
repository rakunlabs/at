package sqlite3

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
	ID        string         `db:"id"`
	Name      string         `db:"name"`
	Config    sql.NullString `db:"config"`
	CreatedAt string         `db:"created_at"`
	UpdatedAt string         `db:"updated_at"`
	CreatedBy sql.NullString `db:"created_by"`
	UpdatedBy sql.NullString `db:"updated_by"`
}

func (s *SQLite) ListMCPServers(ctx context.Context, q *query.Query) (*service.ListResult[service.MCPServer], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableMCPServers, q, "id", "name", "config", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list mcp servers query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
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

func (s *SQLite) GetMCPServer(ctx context.Context, id string) (*service.MCPServer, error) {
	query, _, err := s.goqu.From(s.tableMCPServers).
		Select("id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp server query: %w", err)
	}

	var row mcpServerRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp server %q: %w", id, err)
	}

	return mcpServerRowToRecord(row)
}

func (s *SQLite) GetMCPServerByName(ctx context.Context, name string) (*service.MCPServer, error) {
	query, _, err := s.goqu.From(s.tableMCPServers).
		Select("id", "name", "config", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp server by name query: %w", err)
	}

	var row mcpServerRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Config, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp server by name %q: %w", name, err)
	}

	return mcpServerRowToRecord(row)
}

func (s *SQLite) CreateMCPServer(ctx context.Context, srv service.MCPServer) (*service.MCPServer, error) {
	configJSON, err := json.Marshal(srv.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableMCPServers).Rows(
		goqu.Record{
			"id":         id,
			"name":       srv.Name,
			"config":     string(configJSON),
			"created_at": now.Format(time.RFC3339),
			"updated_at": now.Format(time.RFC3339),
			"created_by": srv.CreatedBy,
			"updated_by": srv.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert mcp server query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create mcp server %q: %w", srv.Name, err)
	}

	return &service.MCPServer{
		ID:        id,
		Name:      srv.Name,
		Config:    srv.Config,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
		CreatedBy: srv.CreatedBy,
		UpdatedBy: srv.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateMCPServer(ctx context.Context, id string, srv service.MCPServer) (*service.MCPServer, error) {
	configJSON, err := json.Marshal(srv.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp server config: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableMCPServers).Set(
		goqu.Record{
			"name":       srv.Name,
			"config":     string(configJSON),
			"updated_at": now.Format(time.RFC3339),
			"updated_by": srv.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update mcp server query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
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

	return s.GetMCPServer(ctx, id)
}

func (s *SQLite) DeleteMCPServer(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableMCPServers).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete mcp server query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete mcp server %q: %w", id, err)
	}

	return nil
}

func mcpServerRowToRecord(row mcpServerRow) (*service.MCPServer, error) {
	var cfg service.MCPServerConfig
	if row.Config.Valid && row.Config.String != "" {
		if err := json.Unmarshal([]byte(row.Config.String), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal mcp server config for %q: %w", row.ID, err)
		}
	}

	return &service.MCPServer{
		ID:        row.ID,
		Name:      row.Name,
		Config:    cfg,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		CreatedBy: row.CreatedBy.String,
		UpdatedBy: row.UpdatedBy.String,
	}, nil
}
