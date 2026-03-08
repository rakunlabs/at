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

type goalRow struct {
	ID             string         `db:"id"`
	OrganizationID sql.NullString `db:"organization_id"`
	ParentGoalID   sql.NullString `db:"parent_goal_id"`
	Name           string         `db:"name"`
	Description    sql.NullString `db:"description"`
	Status         string         `db:"status"`
	Priority       int            `db:"priority"`
	CreatedAt      string         `db:"created_at"`
	UpdatedAt      string         `db:"updated_at"`
	CreatedBy      sql.NullString `db:"created_by"`
	UpdatedBy      sql.NullString `db:"updated_by"`
}

var goalColumns = []interface{}{"id", "organization_id", "parent_goal_id", "name", "description", "status", "priority", "created_at", "updated_at", "created_by", "updated_by"}

func scanGoalRow(scanner interface{ Scan(dest ...any) error }) (goalRow, error) {
	var row goalRow
	err := scanner.Scan(&row.ID, &row.OrganizationID, &row.ParentGoalID, &row.Name, &row.Description, &row.Status, &row.Priority, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)

	return row, err
}

func (s *SQLite) ListGoals(ctx context.Context, q *query.Query) (*service.ListResult[service.Goal], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableGoals, q, goalColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list goals query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()

	var items []service.Goal
	for rows.Next() {
		row, err := scanGoalRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan goal row: %w", err)
		}

		items = append(items, goalRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Goal]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetGoal(ctx context.Context, id string) (*service.Goal, error) {
	query, _, err := s.goqu.From(s.tableGoals).
		Select(goalColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get goal query: %w", err)
	}

	row, err := scanGoalRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get goal %q: %w", id, err)
	}

	goal := goalRowToRecord(row)

	return &goal, nil
}

func (s *SQLite) CreateGoal(ctx context.Context, goal service.Goal) (*service.Goal, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableGoals).Rows(
		goqu.Record{
			"id":              id,
			"organization_id": goal.OrganizationID,
			"parent_goal_id":  goal.ParentGoalID,
			"name":            goal.Name,
			"description":     goal.Description,
			"status":          goal.Status,
			"priority":        goal.Priority,
			"created_at":      now.Format(time.RFC3339),
			"updated_at":      now.Format(time.RFC3339),
			"created_by":      goal.CreatedBy,
			"updated_by":      goal.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert goal query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create goal %q: %w", goal.Name, err)
	}

	return &service.Goal{
		ID:             id,
		OrganizationID: goal.OrganizationID,
		ParentGoalID:   goal.ParentGoalID,
		Name:           goal.Name,
		Description:    goal.Description,
		Status:         goal.Status,
		Priority:       goal.Priority,
		CreatedAt:      now.Format(time.RFC3339),
		UpdatedAt:      now.Format(time.RFC3339),
		CreatedBy:      goal.CreatedBy,
		UpdatedBy:      goal.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateGoal(ctx context.Context, id string, goal service.Goal) (*service.Goal, error) {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableGoals).Set(
		goqu.Record{
			"organization_id": goal.OrganizationID,
			"parent_goal_id":  goal.ParentGoalID,
			"name":            goal.Name,
			"description":     goal.Description,
			"status":          goal.Status,
			"priority":        goal.Priority,
			"updated_at":      now.Format(time.RFC3339),
			"updated_by":      goal.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update goal query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update goal %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetGoal(ctx, id)
}

func (s *SQLite) DeleteGoal(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableGoals).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete goal query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete goal %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) ListGoalsByParent(ctx context.Context, parentID string) ([]service.Goal, error) {
	query, _, err := s.goqu.From(s.tableGoals).
		Select(goalColumns...).
		Where(goqu.I("parent_goal_id").Eq(parentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list goals by parent query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list goals by parent %q: %w", parentID, err)
	}
	defer rows.Close()

	var items []service.Goal
	for rows.Next() {
		row, err := scanGoalRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan goal row: %w", err)
		}

		items = append(items, goalRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) GetGoalAncestry(ctx context.Context, id string) ([]service.Goal, error) {
	var chain []service.Goal
	currentID := id

	for currentID != "" {
		goal, err := s.GetGoal(ctx, currentID)
		if err != nil {
			return nil, fmt.Errorf("get goal ancestry for %q: %w", currentID, err)
		}
		if goal == nil {
			break
		}

		chain = append(chain, *goal)
		currentID = goal.ParentGoalID
	}

	return chain, nil
}

func goalRowToRecord(row goalRow) service.Goal {
	return service.Goal{
		ID:             row.ID,
		OrganizationID: row.OrganizationID.String,
		ParentGoalID:   row.ParentGoalID.String,
		Name:           row.Name,
		Description:    row.Description.String,
		Status:         row.Status,
		Priority:       row.Priority,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		CreatedBy:      row.CreatedBy.String,
		UpdatedBy:      row.UpdatedBy.String,
	}
}
