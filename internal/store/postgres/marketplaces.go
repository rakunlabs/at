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

type marketplaceRow struct {
	ID               string        `db:"id"`
	Name             string        `db:"name"`
	Description      string        `db:"description"`
	Skills           types.RawJSON `db:"skills"`
	SkillServers     types.RawJSON `db:"skill_servers"`
	MCPServers       types.RawJSON `db:"mcp_servers"`
	DirectMCPServers types.RawJSON `db:"direct_mcp_servers"`
	CreatedAt        time.Time     `db:"created_at"`
	UpdatedAt        time.Time     `db:"updated_at"`
	CreatedBy        string        `db:"created_by"`
	UpdatedBy        string        `db:"updated_by"`
}

var marketplaceCols = []any{"id", "name", "description", "skills", "skill_servers", "mcp_servers", "direct_mcp_servers", "created_at", "updated_at", "created_by", "updated_by"}

func (p *Postgres) ListMarketplaces(ctx context.Context, q *query.Query) (*service.ListResult[service.Marketplace], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableMarketplaces, q, marketplaceCols...)
	if err != nil {
		return nil, fmt.Errorf("build list marketplaces query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list marketplaces: %w", err)
	}
	defer rows.Close()

	var items []service.Marketplace
	for rows.Next() {
		var row marketplaceRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Skills, &row.SkillServers, &row.MCPServers, &row.DirectMCPServers, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan marketplace row: %w", err)
		}

		rec, err := marketplaceRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Marketplace]{
		Data: items,
		Meta: service.ListMeta{Total: total, Offset: offset, Limit: limit},
	}, rows.Err()
}

func (p *Postgres) GetMarketplace(ctx context.Context, id string) (*service.Marketplace, error) {
	query, _, err := p.goqu.From(p.tableMarketplaces).
		Select(marketplaceCols...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get marketplace query: %w", err)
	}

	return p.getMarketplaceByQuery(ctx, query, id)
}

func (p *Postgres) GetMarketplaceByName(ctx context.Context, name string) (*service.Marketplace, error) {
	query, _, err := p.goqu.From(p.tableMarketplaces).
		Select(marketplaceCols...).
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get marketplace by name query: %w", err)
	}

	return p.getMarketplaceByQuery(ctx, query, name)
}

func (p *Postgres) CreateMarketplace(ctx context.Context, m service.Marketplace) (*service.Marketplace, error) {
	skillsJSON, skillServersJSON, mcpServersJSON, directMCPServersJSON, err := marshalMarketplaceRefs(m)
	if err != nil {
		return nil, err
	}

	id := ulid.Make().String()
	now := time.Now().UTC()
	query, _, err := p.goqu.Insert(p.tableMarketplaces).Rows(
		goqu.Record{
			"id":                 id,
			"name":               m.Name,
			"description":        m.Description,
			"skills":             types.RawJSON(skillsJSON),
			"skill_servers":      types.RawJSON(skillServersJSON),
			"mcp_servers":        types.RawJSON(mcpServersJSON),
			"direct_mcp_servers": types.RawJSON(directMCPServersJSON),
			"created_at":         now,
			"updated_at":         now,
			"created_by":         m.CreatedBy,
			"updated_by":         m.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert marketplace query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create marketplace %q: %w", m.Name, err)
	}

	m.ID = id
	m.CreatedAt = now.Format(time.RFC3339)
	m.UpdatedAt = now.Format(time.RFC3339)
	return &m, nil
}

func (p *Postgres) UpdateMarketplace(ctx context.Context, id string, m service.Marketplace) (*service.Marketplace, error) {
	skillsJSON, skillServersJSON, mcpServersJSON, directMCPServersJSON, err := marshalMarketplaceRefs(m)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	query, _, err := p.goqu.Update(p.tableMarketplaces).Set(
		goqu.Record{
			"name":               m.Name,
			"description":        m.Description,
			"skills":             types.RawJSON(skillsJSON),
			"skill_servers":      types.RawJSON(skillServersJSON),
			"mcp_servers":        types.RawJSON(mcpServersJSON),
			"direct_mcp_servers": types.RawJSON(directMCPServersJSON),
			"updated_at":         now,
			"updated_by":         m.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update marketplace query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update marketplace %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return p.GetMarketplace(ctx, id)
}

func (p *Postgres) DeleteMarketplace(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableMarketplaces).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete marketplace query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("delete marketplace %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) getMarketplaceByQuery(ctx context.Context, query, key string) (*service.Marketplace, error) {
	var row marketplaceRow
	err := p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Skills, &row.SkillServers, &row.MCPServers, &row.DirectMCPServers, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get marketplace %q: %w", key, err)
	}

	return marketplaceRowToRecord(row)
}

func marketplaceRowToRecord(row marketplaceRow) (*service.Marketplace, error) {
	var skills []string
	if len(row.Skills) > 0 {
		if err := json.Unmarshal(row.Skills, &skills); err != nil {
			return nil, fmt.Errorf("unmarshal marketplace skills for %q: %w", row.ID, err)
		}
	}

	var skillServers []string
	if len(row.SkillServers) > 0 {
		if err := json.Unmarshal(row.SkillServers, &skillServers); err != nil {
			return nil, fmt.Errorf("unmarshal marketplace skill servers for %q: %w", row.ID, err)
		}
	}

	var mcpServers []string
	if len(row.MCPServers) > 0 {
		if err := json.Unmarshal(row.MCPServers, &mcpServers); err != nil {
			return nil, fmt.Errorf("unmarshal marketplace mcp servers for %q: %w", row.ID, err)
		}
	}

	var directMCPServers []service.MarketplaceMCPServer
	if len(row.DirectMCPServers) > 0 {
		if err := json.Unmarshal(row.DirectMCPServers, &directMCPServers); err != nil {
			return nil, fmt.Errorf("unmarshal marketplace direct mcp servers for %q: %w", row.ID, err)
		}
	}

	return &service.Marketplace{
		ID:               row.ID,
		Name:             row.Name,
		Description:      row.Description,
		Skills:           skills,
		SkillServers:     skillServers,
		MCPServers:       mcpServers,
		DirectMCPServers: directMCPServers,
		CreatedAt:        row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:        row.CreatedBy,
		UpdatedBy:        row.UpdatedBy,
	}, nil
}

func marshalMarketplaceRefs(m service.Marketplace) ([]byte, []byte, []byte, []byte, error) {
	skillsJSON, err := json.Marshal(m.Skills)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("marshal marketplace skills: %w", err)
	}
	skillServersJSON, err := json.Marshal(m.SkillServers)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("marshal marketplace skill servers: %w", err)
	}
	mcpServersJSON, err := json.Marshal(m.MCPServers)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("marshal marketplace mcp servers: %w", err)
	}
	directMCPServersJSON, err := json.Marshal(m.DirectMCPServers)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("marshal marketplace direct mcp servers: %w", err)
	}
	return skillsJSON, skillServersJSON, mcpServersJSON, directMCPServersJSON, nil
}
