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
	"github.com/rakunlabs/query"
)

// ─── Goal CRUD ───

type goalRow struct {
	ID             string         `db:"id"`
	OrganizationID sql.NullString `db:"organization_id"`
	ParentGoalID   sql.NullString `db:"parent_goal_id"`
	Name           string         `db:"name"`
	Description    string         `db:"description"`
	Status         string         `db:"status"`
	Priority       int            `db:"priority"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
	CreatedBy      string         `db:"created_by"`
	UpdatedBy      string         `db:"updated_by"`
}

func (p *Postgres) ListGoals(ctx context.Context, q *query.Query) (*service.ListResult[service.Goal], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableGoals, q,
		"id", "organization_id", "parent_goal_id", "name", "description", "status", "priority",
		"created_at", "updated_at", "created_by", "updated_by",
	)
	if err != nil {
		return nil, fmt.Errorf("build list goals query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list goals: %w", err)
	}
	defer rows.Close()

	var items []service.Goal
	for rows.Next() {
		var row goalRow
		if err := rows.Scan(
			&row.ID, &row.OrganizationID, &row.ParentGoalID, &row.Name, &row.Description,
			&row.Status, &row.Priority, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("scan goal row: %w", err)
		}

		items = append(items, *goalRowToRecord(row))
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

func (p *Postgres) GetGoal(ctx context.Context, id string) (*service.Goal, error) {
	query, _, err := p.goqu.From(p.tableGoals).
		Select("id", "organization_id", "parent_goal_id", "name", "description", "status", "priority",
			"created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get goal query: %w", err)
	}

	var row goalRow
	err = p.db.QueryRowContext(ctx, query).Scan(
		&row.ID, &row.OrganizationID, &row.ParentGoalID, &row.Name, &row.Description,
		&row.Status, &row.Priority, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get goal %q: %w", id, err)
	}

	return goalRowToRecord(row), nil
}

func (p *Postgres) CreateGoal(ctx context.Context, goal service.Goal) (*service.Goal, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableGoals).Rows(
		goqu.Record{
			"id":              id,
			"organization_id": nullString(goal.OrganizationID),
			"parent_goal_id":  nullString(goal.ParentGoalID),
			"name":            goal.Name,
			"description":     goal.Description,
			"status":          goal.Status,
			"priority":        goal.Priority,
			"created_at":      now,
			"updated_at":      now,
			"created_by":      goal.CreatedBy,
			"updated_by":      goal.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert goal query: %w", err)
	}

	if _, err := p.db.ExecContext(ctx, query); err != nil {
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

func (p *Postgres) UpdateGoal(ctx context.Context, id string, goal service.Goal) (*service.Goal, error) {
	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableGoals).Set(
		goqu.Record{
			"organization_id": nullString(goal.OrganizationID),
			"parent_goal_id":  nullString(goal.ParentGoalID),
			"name":            goal.Name,
			"description":     goal.Description,
			"status":          goal.Status,
			"priority":        goal.Priority,
			"updated_at":      now,
			"updated_by":      goal.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update goal query: %w", err)
	}

	res, err := p.db.ExecContext(ctx, query)
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

	return p.GetGoal(ctx, id)
}

func (p *Postgres) DeleteGoal(ctx context.Context, id string) error {
	query, _, err := p.goqu.Delete(p.tableGoals).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete goal query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete goal %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) ListGoalsByParent(ctx context.Context, parentID string) ([]service.Goal, error) {
	query, _, err := p.goqu.From(p.tableGoals).
		Select("id", "organization_id", "parent_goal_id", "name", "description", "status", "priority",
			"created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("parent_goal_id").Eq(parentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list goals by parent query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list goals by parent %q: %w", parentID, err)
	}
	defer rows.Close()

	var items []service.Goal
	for rows.Next() {
		var row goalRow
		if err := rows.Scan(
			&row.ID, &row.OrganizationID, &row.ParentGoalID, &row.Name, &row.Description,
			&row.Status, &row.Priority, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("scan goal row: %w", err)
		}

		items = append(items, *goalRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) GetGoalAncestry(ctx context.Context, id string) ([]service.Goal, error) {
	var ancestry []service.Goal
	currentID := id

	for currentID != "" {
		goal, err := p.GetGoal(ctx, currentID)
		if err != nil {
			return nil, fmt.Errorf("get goal ancestry for %q: %w", currentID, err)
		}
		if goal == nil {
			break
		}

		ancestry = append(ancestry, *goal)
		currentID = goal.ParentGoalID
	}

	return ancestry, nil
}

func goalRowToRecord(row goalRow) *service.Goal {
	return &service.Goal{
		ID:             row.ID,
		OrganizationID: row.OrganizationID.String,
		ParentGoalID:   row.ParentGoalID.String,
		Name:           row.Name,
		Description:    row.Description,
		Status:         row.Status,
		Priority:       row.Priority,
		CreatedAt:      row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:      row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
	}
}

// nullString converts an empty string to nil for nullable DB columns.
func nullString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
