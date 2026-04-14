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

// ─── Guide CRUD ───

type guideRow struct {
	ID          string `db:"id"`
	Title       string `db:"title"`
	Description string `db:"description"`
	Icon        string `db:"icon"`
	Content     string `db:"content"`
	CreatedAt   string `db:"created_at"`
	UpdatedAt   string `db:"updated_at"`
	CreatedBy   string `db:"created_by"`
	UpdatedBy   string `db:"updated_by"`
}

func guideRowToRecord(row guideRow) *service.Guide {
	return &service.Guide{
		ID:          row.ID,
		Title:       row.Title,
		Description: row.Description,
		Icon:        row.Icon,
		Content:     row.Content,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
	}
}

var guideCols = []any{"id", "title", "description", "icon", "content", "created_at", "updated_at", "created_by", "updated_by"}

func (s *SQLite) ListGuides(ctx context.Context, q *query.Query) (*service.ListResult[service.Guide], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableGuides, q, guideCols...)
	if err != nil {
		return nil, fmt.Errorf("build list guides query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list guides: %w", err)
	}
	defer rows.Close()

	var items []service.Guide
	for rows.Next() {
		var row guideRow
		if err := rows.Scan(&row.ID, &row.Title, &row.Description, &row.Icon, &row.Content, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan guide row: %w", err)
		}
		items = append(items, *guideRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Guide]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetGuide(ctx context.Context, id string) (*service.Guide, error) {
	query, _, err := s.goqu.From(s.tableGuides).
		Select(guideCols...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get guide query: %w", err)
	}

	var row guideRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Title, &row.Description, &row.Icon, &row.Content, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get guide %q: %w", id, err)
	}

	return guideRowToRecord(row), nil
}

func (s *SQLite) CreateGuide(ctx context.Context, g service.Guide) (*service.Guide, error) {
	id := ulid.Make().String()
	now := time.Now().UTC().Format(time.RFC3339)

	icon := g.Icon
	if icon == "" {
		icon = "BookOpen"
	}

	query, _, err := s.goqu.Insert(s.tableGuides).Rows(
		goqu.Record{
			"id":          id,
			"title":       g.Title,
			"description": g.Description,
			"icon":        icon,
			"content":     g.Content,
			"created_at":  now,
			"updated_at":  now,
			"created_by":  g.CreatedBy,
			"updated_by":  g.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert guide query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create guide %q: %w", g.Title, err)
	}

	return &service.Guide{
		ID:          id,
		Title:       g.Title,
		Description: g.Description,
		Icon:        icon,
		Content:     g.Content,
		CreatedAt:   now,
		UpdatedAt:   now,
		CreatedBy:   g.CreatedBy,
		UpdatedBy:   g.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateGuide(ctx context.Context, id string, g service.Guide) (*service.Guide, error) {
	now := time.Now().UTC().Format(time.RFC3339)

	icon := g.Icon
	if icon == "" {
		icon = "BookOpen"
	}

	query, _, err := s.goqu.Update(s.tableGuides).Set(
		goqu.Record{
			"title":       g.Title,
			"description": g.Description,
			"icon":        icon,
			"content":     g.Content,
			"updated_at":  now,
			"updated_by":  g.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update guide query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update guide %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetGuide(ctx, id)
}

func (s *SQLite) DeleteGuide(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableGuides).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete guide query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete guide %q: %w", id, err)
	}

	return nil
}
