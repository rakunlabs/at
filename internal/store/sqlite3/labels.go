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
)

type labelRow struct {
	ID             string         `db:"id"`
	OrganizationID sql.NullString `db:"organization_id"`
	Name           string         `db:"name"`
	Color          string         `db:"color"`
	CreatedAt      string         `db:"created_at"`
	UpdatedAt      string         `db:"updated_at"`
}

var labelColumns = []interface{}{"id", "organization_id", "name", "color", "created_at", "updated_at"}

func scanLabelRow(scanner interface{ Scan(dest ...any) error }) (labelRow, error) {
	var row labelRow
	err := scanner.Scan(&row.ID, &row.OrganizationID, &row.Name, &row.Color, &row.CreatedAt, &row.UpdatedAt)

	return row, err
}

func (s *SQLite) ListLabels(ctx context.Context, orgID string) ([]service.Label, error) {
	query, _, err := s.goqu.From(s.tableLabels).
		Select(labelColumns...).
		Where(goqu.I("organization_id").Eq(orgID)).
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list labels query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list labels for org %q: %w", orgID, err)
	}
	defer rows.Close()

	var items []service.Label
	for rows.Next() {
		row, err := scanLabelRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan label row: %w", err)
		}

		items = append(items, labelRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) GetLabel(ctx context.Context, id string) (*service.Label, error) {
	query, _, err := s.goqu.From(s.tableLabels).
		Select(labelColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get label query: %w", err)
	}

	row, err := scanLabelRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get label %q: %w", id, err)
	}

	label := labelRowToRecord(row)

	return &label, nil
}

func (s *SQLite) CreateLabel(ctx context.Context, label service.Label) (*service.Label, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableLabels).Rows(
		goqu.Record{
			"id":              id,
			"organization_id": label.OrganizationID,
			"name":            label.Name,
			"color":           label.Color,
			"created_at":      now.Format(time.RFC3339),
			"updated_at":      now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert label query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create label %q: %w", label.Name, err)
	}

	return &service.Label{
		ID:             id,
		OrganizationID: label.OrganizationID,
		Name:           label.Name,
		Color:          label.Color,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateLabel(ctx context.Context, id string, label service.Label) (*service.Label, error) {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableLabels).Set(
		goqu.Record{
			"name":       label.Name,
			"color":      label.Color,
			"updated_at": now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update label query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update label %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetLabel(ctx, id)
}

func (s *SQLite) DeleteLabel(ctx context.Context, id string) error {
	// Delete task-label associations first.
	delAssoc, _, err := s.goqu.Delete(s.tableTaskLabels).
		Where(goqu.I("label_id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete task-label associations query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, delAssoc); err != nil {
		return fmt.Errorf("delete task-label associations for label %q: %w", id, err)
	}

	query, _, err := s.goqu.Delete(s.tableLabels).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete label query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete label %q: %w", id, err)
	}

	return nil
}

// ─── Task-Label Associations ───

func (s *SQLite) AddLabelToTask(ctx context.Context, taskID, labelID string) error {
	query, _, err := s.goqu.Insert(s.tableTaskLabels).Rows(
		goqu.Record{
			"task_id":  taskID,
			"label_id": labelID,
		},
	).OnConflict(goqu.DoNothing()).ToSQL()
	if err != nil {
		return fmt.Errorf("build add label to task query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("add label %q to task %q: %w", labelID, taskID, err)
	}

	return nil
}

func (s *SQLite) RemoveLabelFromTask(ctx context.Context, taskID, labelID string) error {
	query, _, err := s.goqu.Delete(s.tableTaskLabels).
		Where(
			goqu.I("task_id").Eq(taskID),
			goqu.I("label_id").Eq(labelID),
		).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build remove label from task query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("remove label %q from task %q: %w", labelID, taskID, err)
	}

	return nil
}

func (s *SQLite) ListLabelsForTask(ctx context.Context, taskID string) ([]service.Label, error) {
	// Join task_labels with labels to get full label records.
	query, _, err := s.goqu.From(s.tableTaskLabels).
		Join(s.tableLabels, goqu.On(goqu.I(s.tableTaskLabels.GetTable()+".label_id").Eq(goqu.I(s.tableLabels.GetTable()+".id")))).
		Select(
			goqu.I(s.tableLabels.GetTable()+".id"),
			goqu.I(s.tableLabels.GetTable()+".organization_id"),
			goqu.I(s.tableLabels.GetTable()+".name"),
			goqu.I(s.tableLabels.GetTable()+".color"),
			goqu.I(s.tableLabels.GetTable()+".created_at"),
			goqu.I(s.tableLabels.GetTable()+".updated_at"),
		).
		Where(goqu.I(s.tableTaskLabels.GetTable() + ".task_id").Eq(taskID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list labels for task query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list labels for task %q: %w", taskID, err)
	}
	defer rows.Close()

	var items []service.Label
	for rows.Next() {
		row, err := scanLabelRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan label row: %w", err)
		}

		items = append(items, labelRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) ListTasksForLabel(ctx context.Context, labelID string) ([]string, error) {
	query, _, err := s.goqu.From(s.tableTaskLabels).
		Select("task_id").
		Where(goqu.I("label_id").Eq(labelID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list tasks for label query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks for label %q: %w", labelID, err)
	}
	defer rows.Close()

	var taskIDs []string
	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			return nil, fmt.Errorf("scan task ID: %w", err)
		}
		taskIDs = append(taskIDs, taskID)
	}

	return taskIDs, rows.Err()
}

func labelRowToRecord(row labelRow) service.Label {
	return service.Label{
		ID:             row.ID,
		OrganizationID: row.OrganizationID.String,
		Name:           row.Name,
		Color:          row.Color,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}
