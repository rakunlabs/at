package sqlite3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/rakunlabs/at/internal/service"
)

type featureSettingRow struct {
	Key       string `db:"key"`
	Enabled   bool   `db:"enabled"`
	CreatedAt string `db:"created_at"`
	UpdatedAt string `db:"updated_at"`
	CreatedBy string `db:"created_by"`
	UpdatedBy string `db:"updated_by"`
}

var featureSettingColumns = []any{"key", "enabled", "created_at", "updated_at", "created_by", "updated_by"}

func (s *SQLite) ListFeatureSettings(ctx context.Context) ([]service.FeatureSetting, error) {
	query, _, err := s.goqu.From(s.tableFeatureSettings).
		Select(featureSettingColumns...).
		Order(goqu.I("key").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list feature settings query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list feature settings: %w", err)
	}
	defer rows.Close()

	var items []service.FeatureSetting
	for rows.Next() {
		var row featureSettingRow
		if err := scanFeatureSettingRow(rows, &row); err != nil {
			return nil, err
		}
		items = append(items, featureSettingRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) GetFeatureSetting(ctx context.Context, key string) (*service.FeatureSetting, error) {
	query, _, err := s.goqu.From(s.tableFeatureSettings).
		Select(featureSettingColumns...).
		Where(goqu.I("key").Eq(key)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get feature setting query: %w", err)
	}

	var row featureSettingRow
	err = scanFeatureSettingRow(s.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get feature setting %q: %w", key, err)
	}

	rec := featureSettingRowToRecord(row)
	return &rec, nil
}

func (s *SQLite) UpsertFeatureSetting(ctx context.Context, key string, enabled bool, updatedBy string) (*service.FeatureSetting, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	query, _, err := s.goqu.Insert(s.tableFeatureSettings).Rows(
		goqu.Record{
			"key":        key,
			"enabled":    enabled,
			"created_at": now,
			"updated_at": now,
			"created_by": updatedBy,
			"updated_by": updatedBy,
		},
	).OnConflict(goqu.DoUpdate("key", goqu.Record{
		"enabled":    enabled,
		"updated_at": now,
		"updated_by": updatedBy,
	})).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build upsert feature setting query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("upsert feature setting %q: %w", key, err)
	}

	return s.GetFeatureSetting(ctx, key)
}

func scanFeatureSettingRow(sc rowScanner, row *featureSettingRow) error {
	return sc.Scan(&row.Key, &row.Enabled, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
}

func featureSettingRowToRecord(row featureSettingRow) service.FeatureSetting {
	return service.FeatureSetting{
		Key:       row.Key,
		Enabled:   row.Enabled,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
		CreatedBy: row.CreatedBy,
		UpdatedBy: row.UpdatedBy,
	}
}
