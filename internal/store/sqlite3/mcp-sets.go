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

type mcpSetRow struct {
	ID          string         `db:"id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Config      sql.NullString `db:"config"`
	Servers     sql.NullString `db:"servers"`
	URLs        sql.NullString `db:"urls"`
	CreatedAt   string         `db:"created_at"`
	UpdatedAt   string         `db:"updated_at"`
	CreatedBy   sql.NullString `db:"created_by"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

func (s *SQLite) ListMCPSets(ctx context.Context, q *query.Query) (*service.ListResult[service.MCPSet], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableMCPSets, q, "id", "name", "description", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list mcp sets query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list mcp sets: %w", err)
	}
	defer rows.Close()

	var items []service.MCPSet
	for rows.Next() {
		var row mcpSetRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan mcp set row: %w", err)
		}

		rec, err := mcpSetRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.MCPSet]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetMCPSet(ctx context.Context, id string) (*service.MCPSet, error) {
	query, _, err := s.goqu.From(s.tableMCPSets).
		Select("id", "name", "description", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp set query: %w", err)
	}

	var row mcpSetRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp set %q: %w", id, err)
	}

	return mcpSetRowToRecord(row)
}

func (s *SQLite) GetMCPSetByName(ctx context.Context, name string) (*service.MCPSet, error) {
	query, _, err := s.goqu.From(s.tableMCPSets).
		Select("id", "name", "description", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp set by name query: %w", err)
	}

	var row mcpSetRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp set by name %q: %w", name, err)
	}

	return mcpSetRowToRecord(row)
}

func (s *SQLite) CreateMCPSet(ctx context.Context, set service.MCPSet) (*service.MCPSet, error) {
	configJSON, err := json.Marshal(set.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set config: %w", err)
	}
	serversJSON, err := json.Marshal(set.Servers)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set servers: %w", err)
	}
	urlsJSON, err := json.Marshal(set.URLs)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set urls: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableMCPSets).Rows(
		goqu.Record{
			"id":          id,
			"name":        set.Name,
			"description": set.Description,
			"config":      string(configJSON),
			"servers":     string(serversJSON),
			"urls":        string(urlsJSON),
			"created_at":  now.Format(time.RFC3339),
			"updated_at":  now.Format(time.RFC3339),
			"created_by":  set.CreatedBy,
			"updated_by":  set.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert mcp set query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create mcp set %q: %w", set.Name, err)
	}

	return &service.MCPSet{
		ID:          id,
		Name:        set.Name,
		Description: set.Description,
		Config:      set.Config,
		Servers:     set.Servers,
		URLs:        set.URLs,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   set.CreatedBy,
		UpdatedBy:   set.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateMCPSet(ctx context.Context, id string, set service.MCPSet) (*service.MCPSet, error) {
	configJSON, err := json.Marshal(set.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set config: %w", err)
	}
	serversJSON, err := json.Marshal(set.Servers)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set servers: %w", err)
	}
	urlsJSON, err := json.Marshal(set.URLs)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set urls: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableMCPSets).Set(
		goqu.Record{
			"name":        set.Name,
			"description": set.Description,
			"config":      string(configJSON),
			"servers":     string(serversJSON),
			"urls":        string(urlsJSON),
			"updated_at":  now.Format(time.RFC3339),
			"updated_by":  set.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update mcp set query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update mcp set %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetMCPSet(ctx, id)
}

func (s *SQLite) DeleteMCPSet(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableMCPSets).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete mcp set query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete mcp set %q: %w", id, err)
	}

	return nil
}

func mcpSetRowToRecord(row mcpSetRow) (*service.MCPSet, error) {
	var cfg service.MCPServerConfig
	if row.Config.Valid && row.Config.String != "" && row.Config.String != "{}" {
		if err := json.Unmarshal([]byte(row.Config.String), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set config for %q: %w", row.ID, err)
		}
	}

	var servers []string
	if row.Servers.Valid && row.Servers.String != "" {
		if err := json.Unmarshal([]byte(row.Servers.String), &servers); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set servers for %q: %w", row.ID, err)
		}
	}

	var urls []string
	if row.URLs.Valid && row.URLs.String != "" {
		if err := json.Unmarshal([]byte(row.URLs.String), &urls); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set urls for %q: %w", row.ID, err)
		}
	}

	return &service.MCPSet{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Config:      cfg,
		Servers:     servers,
		URLs:        urls,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CreatedBy:   row.CreatedBy.String,
		UpdatedBy:   row.UpdatedBy.String,
	}, nil
}
