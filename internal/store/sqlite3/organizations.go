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

type orgRow struct {
	ID           string         `db:"id"`
	Name         string         `db:"name"`
	Description  sql.NullString `db:"description"`
	CanvasLayout sql.NullString `db:"canvas_layout"`
	CreatedAt    string         `db:"created_at"`
	UpdatedAt    string         `db:"updated_at"`
	CreatedBy    sql.NullString `db:"created_by"`
	UpdatedBy    sql.NullString `db:"updated_by"`
}

func (s *SQLite) ListOrganizations(ctx context.Context, q *query.Query) (*service.ListResult[service.Organization], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableOrganizations, q, "id", "name", "description", "canvas_layout", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list organizations query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()

	var items []service.Organization
	for rows.Next() {
		var row orgRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.CanvasLayout, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan organization row: %w", err)
		}

		items = append(items, orgRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Organization]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetOrganization(ctx context.Context, id string) (*service.Organization, error) {
	query, _, err := s.goqu.From(s.tableOrganizations).
		Select("id", "name", "description", "canvas_layout", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get organization query: %w", err)
	}

	var row orgRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.CanvasLayout, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization %q: %w", id, err)
	}

	org := orgRowToRecord(row)

	return &org, nil
}

func (s *SQLite) CreateOrganization(ctx context.Context, org service.Organization) (*service.Organization, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	canvasLayout := string(org.CanvasLayout)
	if canvasLayout == "" {
		canvasLayout = "{}"
	}

	query, _, err := s.goqu.Insert(s.tableOrganizations).Rows(
		goqu.Record{
			"id":            id,
			"name":          org.Name,
			"description":   org.Description,
			"canvas_layout": canvasLayout,
			"created_at":    now.Format(time.RFC3339),
			"updated_at":    now.Format(time.RFC3339),
			"created_by":    org.CreatedBy,
			"updated_by":    org.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert organization query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create organization %q: %w", org.Name, err)
	}

	return &service.Organization{
		ID:           id,
		Name:         org.Name,
		Description:  org.Description,
		CanvasLayout: org.CanvasLayout,
		CreatedAt:    now.Format(time.RFC3339),
		UpdatedAt:    now.Format(time.RFC3339),
		CreatedBy:    org.CreatedBy,
		UpdatedBy:    org.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateOrganization(ctx context.Context, id string, org service.Organization) (*service.Organization, error) {
	now := time.Now().UTC()

	rec := goqu.Record{
		"name":        org.Name,
		"description": org.Description,
		"updated_at":  now.Format(time.RFC3339),
		"updated_by":  org.UpdatedBy,
	}
	if len(org.CanvasLayout) > 0 {
		rec["canvas_layout"] = string(org.CanvasLayout)
	}

	query, _, err := s.goqu.Update(s.tableOrganizations).Set(rec).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update organization query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update organization %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetOrganization(ctx, id)
}

func (s *SQLite) DeleteOrganization(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableOrganizations).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete organization query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete organization %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) IncrementIssueCounter(ctx context.Context, orgID string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	q := fmt.Sprintf(
		`UPDATE %s SET issue_counter = issue_counter + 1, updated_at = ? WHERE id = ?`,
		s.tableOrganizations.GetTable(),
	)
	if _, err := s.db.ExecContext(ctx, q, now, orgID); err != nil {
		return 0, fmt.Errorf("increment issue counter for org %q: %w", orgID, err)
	}
	// Read back the counter.
	selectQ := fmt.Sprintf(`SELECT issue_counter FROM %s WHERE id = ?`, s.tableOrganizations.GetTable())
	var counter int64
	if err := s.db.QueryRowContext(ctx, selectQ, orgID).Scan(&counter); err != nil {
		return 0, fmt.Errorf("read issue counter for org %q: %w", orgID, err)
	}
	return counter, nil
}

func orgRowToRecord(row orgRow) service.Organization {
	var canvasLayout json.RawMessage
	if row.CanvasLayout.Valid && row.CanvasLayout.String != "" && row.CanvasLayout.String != "{}" {
		canvasLayout = json.RawMessage(row.CanvasLayout.String)
	}

	return service.Organization{
		ID:           row.ID,
		Name:         row.Name,
		Description:  row.Description.String,
		CanvasLayout: canvasLayout,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
		CreatedBy:    row.CreatedBy.String,
		UpdatedBy:    row.UpdatedBy.String,
	}
}
