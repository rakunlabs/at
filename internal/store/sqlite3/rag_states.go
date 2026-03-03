package sqlite3

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
	Key       string `db:"key"`
	Value     string `db:"value"`
	UpdatedAt string `db:"updated_at"`
}

func (s *SQLite) GetRAGState(ctx context.Context, key string) (*service.RAGState, error) {
	query, _, err := s.goqu.From(s.tableRAGStates).
		Select("key", "value", "updated_at").
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get query: %w", err)
	}

	var row ragStateRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.Key, &row.Value, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag state %q: %w", key, err)
	}

	updatedAt, err := time.Parse(time.RFC3339, row.UpdatedAt)
	if err != nil {
		// Try fallback parsing if RFC3339 fails (e.g. SQLite default datetime('now') format)
		updatedAt, err = time.Parse("2006-01-02 15:04:05", row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse updated_at %q: %w", row.UpdatedAt, err)
		}
	}

	return &service.RAGState{
		Key:       row.Key,
		Value:     row.Value,
		UpdatedAt: types.Time{Time: updatedAt},
	}, nil
}

func (s *SQLite) SetRAGState(ctx context.Context, key string, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	// Upsert: Try insert, on conflict update.
	query, _, err := s.goqu.Insert(s.tableRAGStates).Rows(
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

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("set rag state %q: %w", key, err)
	}

	return nil
}
