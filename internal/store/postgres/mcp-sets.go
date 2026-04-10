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
	"github.com/worldline-go/types"
)

type mcpSetRow struct {
	ID          string         `db:"id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Category    string         `db:"category"`
	Tags        types.RawJSON  `db:"tags"`
	Config      types.RawJSON  `db:"config"`
	Servers     types.RawJSON  `db:"servers"`
	URLs        types.RawJSON  `db:"urls"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	CreatedBy   sql.NullString `db:"created_by"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

func (p *Postgres) ListMCPSets(ctx context.Context, q *query.Query) (*service.ListResult[service.MCPSet], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableMCPSets, q, "id", "name", "description", "category", "tags", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list mcp sets query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list mcp sets: %w", err)
	}
	defer rows.Close()

	var items []service.MCPSet
	for rows.Next() {
		var row mcpSetRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
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

func (p *Postgres) GetMCPSet(ctx context.Context, id string) (*service.MCPSet, error) {
	query, _, err := p.goqu.From(p.tableMCPSets).
		Select("id", "name", "description", "category", "tags", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp set query: %w", err)
	}

	var row mcpSetRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp set %q: %w", id, err)
	}

	return mcpSetRowToRecord(row)
}

func (p *Postgres) GetMCPSetByName(ctx context.Context, name string) (*service.MCPSet, error) {
	query, _, err := p.goqu.From(p.tableMCPSets).
		Select("id", "name", "description", "category", "tags", "config", "servers", "urls", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get mcp set by name query: %w", err)
	}

	var row mcpSetRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Category, &row.Tags, &row.Config, &row.Servers, &row.URLs, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get mcp set by name %q: %w", name, err)
	}

	return mcpSetRowToRecord(row)
}

func (p *Postgres) CreateMCPSet(ctx context.Context, s service.MCPSet) (*service.MCPSet, error) {
	configJSON, err := json.Marshal(s.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set config: %w", err)
	}
	serversJSON, err := json.Marshal(s.Servers)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set servers: %w", err)
	}
	urlsJSON, err := json.Marshal(s.URLs)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set urls: %w", err)
	}
	tagsJSON, err := json.Marshal(s.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set tags: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableMCPSets).Rows(
		goqu.Record{
			"id":          id,
			"name":        s.Name,
			"description": s.Description,
			"category":    s.Category,
			"tags":        types.RawJSON(tagsJSON),
			"config":      types.RawJSON(configJSON),
			"servers":     types.RawJSON(serversJSON),
			"urls":        types.RawJSON(urlsJSON),
			"created_at":  now,
			"updated_at":  now,
			"created_by":  s.CreatedBy,
			"updated_by":  s.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert mcp set query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create mcp set %q: %w", s.Name, err)
	}

	return &service.MCPSet{
		ID:          id,
		Name:        s.Name,
		Description: s.Description,
		Category:    s.Category,
		Tags:        s.Tags,
		Config:      s.Config,
		Servers:     s.Servers,
		URLs:        s.URLs,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   s.CreatedBy,
		UpdatedBy:   s.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateMCPSet(ctx context.Context, id string, s service.MCPSet) (*service.MCPSet, error) {
	configJSON, err := json.Marshal(s.Config)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set config: %w", err)
	}
	serversJSON, err := json.Marshal(s.Servers)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set servers: %w", err)
	}
	urlsJSON, err := json.Marshal(s.URLs)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set urls: %w", err)
	}
	tagsJSON, err := json.Marshal(s.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal mcp set tags: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableMCPSets).Set(
		goqu.Record{
			"name":        s.Name,
			"description": s.Description,
			"category":    s.Category,
			"tags":        types.RawJSON(tagsJSON),
			"config":      types.RawJSON(configJSON),
			"servers":     types.RawJSON(serversJSON),
			"urls":        types.RawJSON(urlsJSON),
			"updated_at":  now,
			"updated_by":  s.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update mcp set query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetMCPSet(ctx, id)
}

func (p *Postgres) DeleteMCPSet(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableMCPSets).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete mcp set query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete mcp set %q: %w", id, err)
	}

	return nil
}

func mcpSetRowToRecord(row mcpSetRow) (*service.MCPSet, error) {
	var cfg service.MCPServerConfig
	if len(row.Config) > 0 && string(row.Config) != "{}" {
		if err := json.Unmarshal(row.Config, &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set config for %q: %w", row.ID, err)
		}
	}

	var servers []string
	if len(row.Servers) > 0 {
		if err := json.Unmarshal(row.Servers, &servers); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set servers for %q: %w", row.ID, err)
		}
	}

	var urls []string
	if len(row.URLs) > 0 {
		if err := json.Unmarshal(row.URLs, &urls); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set urls for %q: %w", row.ID, err)
		}
	}

	var tags []string
	if len(row.Tags) > 0 {
		if err := json.Unmarshal(row.Tags, &tags); err != nil {
			return nil, fmt.Errorf("unmarshal mcp set tags for %q: %w", row.ID, err)
		}
	}

	return &service.MCPSet{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Category:    row.Category,
		Tags:        tags,
		Config:      cfg,
		Servers:     servers,
		URLs:        urls,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:   row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy.String,
		UpdatedBy:   row.UpdatedBy.String,
	}, nil
}
