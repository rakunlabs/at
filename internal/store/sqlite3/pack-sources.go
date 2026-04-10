package sqlite3

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Pack Source CRUD ───

type packSourceRow struct {
	ID        string         `db:"id"`
	Name      string         `db:"name"`
	URL       string         `db:"url"`
	Branch    string         `db:"branch"`
	Status    string         `db:"status"`
	LastSync  sql.NullString `db:"last_sync"`
	Error     sql.NullString `db:"error"`
	CreatedAt string         `db:"created_at"`
	UpdatedAt string         `db:"updated_at"`
}

func packSourceRowToRecord(row packSourceRow) *service.PackSource {
	rec := &service.PackSource{
		ID:        row.ID,
		Name:      row.Name,
		URL:       row.URL,
		Branch:    row.Branch,
		Status:    row.Status,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if row.LastSync.Valid {
		rec.LastSync = row.LastSync.String
	}
	if row.Error.Valid {
		rec.Error = row.Error.String
	}

	return rec
}

var packSourceCols = []any{"id", "name", "url", "branch", "status", "last_sync", "error", "created_at", "updated_at"}

func (s *SQLite) ListPackSources(ctx context.Context, q *query.Query) (*service.ListResult[service.PackSource], error) {
	sql, total, err := s.buildListQuery(ctx, s.tablePackSources, q, packSourceCols...)
	if err != nil {
		return nil, fmt.Errorf("build list pack sources query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list pack sources: %w", err)
	}
	defer rows.Close()

	var items []service.PackSource
	for rows.Next() {
		var row packSourceRow
		if err := rows.Scan(&row.ID, &row.Name, &row.URL, &row.Branch, &row.Status, &row.LastSync, &row.Error, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan pack source row: %w", err)
		}
		items = append(items, *packSourceRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.PackSource]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetPackSource(ctx context.Context, id string) (*service.PackSource, error) {
	query, _, err := s.goqu.From(s.tablePackSources).
		Select(packSourceCols...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get pack source query: %w", err)
	}

	var row packSourceRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.URL, &row.Branch, &row.Status, &row.LastSync, &row.Error, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get pack source %q: %w", id, err)
	}

	return packSourceRowToRecord(row), nil
}

func (s *SQLite) CreatePackSource(ctx context.Context, ps service.PackSource) (*service.PackSource, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	lastSync := sql.NullString{}
	if ps.LastSync != "" {
		lastSync = sql.NullString{String: ps.LastSync, Valid: true}
	}

	query, _, err := s.goqu.Insert(s.tablePackSources).Rows(
		goqu.Record{
			"id":         id,
			"name":       ps.Name,
			"url":        ps.URL,
			"branch":     ps.Branch,
			"status":     ps.Status,
			"last_sync":  lastSync,
			"error":      ps.Error,
			"created_at": now,
			"updated_at": now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert pack source query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create pack source %q: %w", ps.Name, err)
	}

	return &service.PackSource{
		ID:        id,
		Name:      ps.Name,
		URL:       ps.URL,
		Branch:    ps.Branch,
		Status:    ps.Status,
		LastSync:  ps.LastSync,
		Error:     ps.Error,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

func (s *SQLite) UpdatePackSource(ctx context.Context, id string, ps service.PackSource) (*service.PackSource, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	lastSync := sql.NullString{}
	if ps.LastSync != "" {
		lastSync = sql.NullString{String: ps.LastSync, Valid: true}
	}
	query, _, err := s.goqu.Update(s.tablePackSources).Set(
		goqu.Record{
			"name":       ps.Name,
			"url":        ps.URL,
			"branch":     ps.Branch,
			"status":     ps.Status,
			"last_sync":  lastSync,
			"error":      ps.Error,
			"updated_at": now,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update pack source query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update pack source %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetPackSource(ctx, id)
}

func (s *SQLite) DeletePackSource(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tablePackSources).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete pack source query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete pack source %q: %w", id, err)
	}

	return nil
}
