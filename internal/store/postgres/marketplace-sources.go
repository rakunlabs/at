package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Marketplace Source CRUD ───

type marketplaceSourceRow struct {
	ID        string    `db:"id"`
	Name      string    `db:"name"`
	Type      string    `db:"type"`
	SearchURL string    `db:"search_url"`
	TopURL    string    `db:"top_url"`
	Enabled   bool      `db:"enabled"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

func marketplaceSourceRowToRecord(row marketplaceSourceRow) *service.MarketplaceSource {
	return &service.MarketplaceSource{
		ID:        row.ID,
		Name:      row.Name,
		Type:      row.Type,
		SearchURL: row.SearchURL,
		TopURL:    row.TopURL,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt.Format(time.RFC3339),
		UpdatedAt: row.UpdatedAt.Format(time.RFC3339),
	}
}

var marketplaceSourceCols = []any{"id", "name", "type", "search_url", "top_url", "enabled", "created_at", "updated_at"}

func (p *Postgres) ListMarketplaceSources(ctx context.Context) ([]service.MarketplaceSource, error) {
	query, _, err := p.goqu.From(p.tableMarketplaceSources).
		Select(marketplaceSourceCols...).
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list marketplace sources query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list marketplace sources: %w", err)
	}
	defer rows.Close()

	var items []service.MarketplaceSource
	for rows.Next() {
		var row marketplaceSourceRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Type, &row.SearchURL, &row.TopURL, &row.Enabled, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan marketplace source row: %w", err)
		}
		items = append(items, *marketplaceSourceRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) GetMarketplaceSource(ctx context.Context, id string) (*service.MarketplaceSource, error) {
	query, _, err := p.goqu.From(p.tableMarketplaceSources).
		Select(marketplaceSourceCols...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get marketplace source query: %w", err)
	}

	var row marketplaceSourceRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Type, &row.SearchURL, &row.TopURL, &row.Enabled, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get marketplace source %q: %w", id, err)
	}

	return marketplaceSourceRowToRecord(row), nil
}

func (p *Postgres) CreateMarketplaceSource(ctx context.Context, src service.MarketplaceSource) (*service.MarketplaceSource, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableMarketplaceSources).Rows(
		goqu.Record{
			"id":         id,
			"name":       src.Name,
			"type":       src.Type,
			"search_url": src.SearchURL,
			"top_url":    src.TopURL,
			"enabled":    src.Enabled,
			"created_at": now,
			"updated_at": now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert marketplace source query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create marketplace source: %w", err)
	}

	return &service.MarketplaceSource{
		ID:        id,
		Name:      src.Name,
		Type:      src.Type,
		SearchURL: src.SearchURL,
		TopURL:    src.TopURL,
		Enabled:   src.Enabled,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
	}, nil
}

func (p *Postgres) UpdateMarketplaceSource(ctx context.Context, id string, src service.MarketplaceSource) (*service.MarketplaceSource, error) {
	now := time.Now().UTC()

	record := goqu.Record{
		"name":       src.Name,
		"type":       src.Type,
		"search_url": src.SearchURL,
		"top_url":    src.TopURL,
		"enabled":    src.Enabled,
		"updated_at": now,
	}

	query, _, err := p.goqu.Update(p.tableMarketplaceSources).Set(record).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update marketplace source query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update marketplace source %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return p.GetMarketplaceSource(ctx, id)
}

func (p *Postgres) DeleteMarketplaceSource(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableMarketplaceSources).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete marketplace source query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete marketplace source %q: %w", id, err)
	}

	return nil
}
