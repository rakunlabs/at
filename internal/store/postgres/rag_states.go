package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
	"github.com/worldline-go/types"
)

type ragStateRow struct {
	Key       string    `db:"key"`
	Value     string    `db:"value"`
	UpdatedAt time.Time `db:"updated_at"`
}

func (p *Postgres) GetRAGState(ctx context.Context, key string) (*service.RAGState, error) {
	query, _, err := p.goqu.From(p.tableRAGStates).
		Select("key", "value", "updated_at").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get query: %w", err)
	}

	var row ragStateRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.Key, &row.Value, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag state %q: %w", key, err)
	}

	return &service.RAGState{
		Key:       row.Key,
		Value:     row.Value,
		UpdatedAt: types.Time{Time: row.UpdatedAt},
	}, nil
}

func (p *Postgres) SetRAGState(ctx context.Context, key string, value string) error {
	now := time.Now().UTC()

	// Upsert: Try insert, on conflict update.
	query, _, err := p.goqu.Insert(p.tableRAGStates).Rows(
		goqu.Record{
			"key":        key,
			"value":      value,
			"updated_at": now,
		},
	).OnConflict(goqu.DoUpdate("key", goqu.Record{
		"value":      value,
		"updated_at": now,
	})).ToSQL()

	if err != nil {
		return fmt.Errorf("build upsert query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("set rag state %q: %w", key, err)
	}

	return nil
}
