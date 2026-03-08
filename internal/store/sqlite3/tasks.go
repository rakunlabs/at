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

type taskRow struct {
	ID              string         `db:"id"`
	OrganizationID  sql.NullString `db:"organization_id"`
	ProjectID       sql.NullString `db:"project_id"`
	GoalID          sql.NullString `db:"goal_id"`
	ParentID        sql.NullString `db:"parent_id"`
	AssignedAgentID sql.NullString `db:"assigned_agent_id"`
	Identifier      sql.NullString `db:"identifier"`
	Title           string         `db:"title"`
	Description     sql.NullString `db:"description"`
	Status          string         `db:"status"`
	PriorityLevel   sql.NullString `db:"priority_level"`
	Priority        int            `db:"priority"`
	Result          sql.NullString `db:"result"`
	BillingCode     sql.NullString `db:"billing_code"`
	RequestDepth    int            `db:"request_depth"`
	CheckedOutBy    sql.NullString `db:"checked_out_by"`
	CheckedOutAt    sql.NullString `db:"checked_out_at"`
	StartedAt       sql.NullString `db:"started_at"`
	CompletedAt     sql.NullString `db:"completed_at"`
	CancelledAt     sql.NullString `db:"cancelled_at"`
	HiddenAt        sql.NullString `db:"hidden_at"`
	CreatedAt       string         `db:"created_at"`
	UpdatedAt       string         `db:"updated_at"`
	CreatedBy       sql.NullString `db:"created_by"`
	UpdatedBy       sql.NullString `db:"updated_by"`
}

var taskColumns = []interface{}{
	"id", "organization_id", "project_id", "goal_id", "parent_id", "assigned_agent_id",
	"identifier", "title", "description", "status", "priority_level", "priority", "result",
	"billing_code", "request_depth", "checked_out_by", "checked_out_at",
	"started_at", "completed_at", "cancelled_at", "hidden_at",
	"created_at", "updated_at", "created_by", "updated_by",
}

func scanTaskRow(scanner interface{ Scan(dest ...any) error }) (taskRow, error) {
	var row taskRow
	err := scanner.Scan(
		&row.ID, &row.OrganizationID, &row.ProjectID, &row.GoalID, &row.ParentID,
		&row.AssignedAgentID, &row.Identifier, &row.Title, &row.Description,
		&row.Status, &row.PriorityLevel, &row.Priority, &row.Result,
		&row.BillingCode, &row.RequestDepth, &row.CheckedOutBy, &row.CheckedOutAt,
		&row.StartedAt, &row.CompletedAt, &row.CancelledAt, &row.HiddenAt,
		&row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
	)

	return row, err
}

func (s *SQLite) ListTasks(ctx context.Context, q *query.Query) (*service.ListResult[service.Task], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableTasks, q, taskColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list tasks query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		row, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, taskRowToRecord(row))
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Task]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetTask(ctx context.Context, id string) (*service.Task, error) {
	query, _, err := s.goqu.From(s.tableTasks).
		Select(taskColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get task query: %w", err)
	}

	row, err := scanTaskRow(s.db.QueryRowContext(ctx, query))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task %q: %w", id, err)
	}

	task := taskRowToRecord(row)

	return &task, nil
}

func (s *SQLite) CreateTask(ctx context.Context, task service.Task) (*service.Task, error) {
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableTasks).Rows(
		goqu.Record{
			"id":                id,
			"organization_id":   task.OrganizationID,
			"project_id":        task.ProjectID,
			"goal_id":           task.GoalID,
			"parent_id":         task.ParentID,
			"assigned_agent_id": task.AssignedAgentID,
			"identifier":        task.Identifier,
			"title":             task.Title,
			"description":       task.Description,
			"status":            task.Status,
			"priority_level":    task.PriorityLevel,
			"priority":          task.Priority,
			"result":            task.Result,
			"billing_code":      task.BillingCode,
			"request_depth":     task.RequestDepth,
			"checked_out_by":    "",
			"checked_out_at":    nil,
			"started_at":        nil,
			"completed_at":      nil,
			"cancelled_at":      nil,
			"hidden_at":         nil,
			"created_at":        now.Format(time.RFC3339),
			"updated_at":        now.Format(time.RFC3339),
			"created_by":        task.CreatedBy,
			"updated_by":        task.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert task query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create task %q: %w", task.Title, err)
	}

	return &service.Task{
		ID:              id,
		OrganizationID:  task.OrganizationID,
		ProjectID:       task.ProjectID,
		GoalID:          task.GoalID,
		ParentID:        task.ParentID,
		AssignedAgentID: task.AssignedAgentID,
		Identifier:      task.Identifier,
		Title:           task.Title,
		Description:     task.Description,
		Status:          task.Status,
		PriorityLevel:   task.PriorityLevel,
		Priority:        task.Priority,
		Result:          task.Result,
		BillingCode:     task.BillingCode,
		RequestDepth:    task.RequestDepth,
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
		CreatedBy:       task.CreatedBy,
		UpdatedBy:       task.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateTask(ctx context.Context, id string, task service.Task) (*service.Task, error) {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableTasks).Set(
		goqu.Record{
			"organization_id":   task.OrganizationID,
			"project_id":        task.ProjectID,
			"goal_id":           task.GoalID,
			"parent_id":         task.ParentID,
			"assigned_agent_id": task.AssignedAgentID,
			"identifier":        task.Identifier,
			"title":             task.Title,
			"description":       task.Description,
			"status":            task.Status,
			"priority_level":    task.PriorityLevel,
			"priority":          task.Priority,
			"result":            task.Result,
			"billing_code":      task.BillingCode,
			"request_depth":     task.RequestDepth,
			"started_at":        task.StartedAt,
			"completed_at":      task.CompletedAt,
			"cancelled_at":      task.CancelledAt,
			"hidden_at":         task.HiddenAt,
			"updated_at":        now.Format(time.RFC3339),
			"updated_by":        task.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update task query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update task %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetTask(ctx, id)
}

func (s *SQLite) DeleteTask(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableTasks).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete task query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete task %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) ListTasksByAgent(ctx context.Context, agentID string) ([]service.Task, error) {
	query, _, err := s.goqu.From(s.tableTasks).
		Select(taskColumns...).
		Where(goqu.I("assigned_agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list tasks by agent query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks by agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		row, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, taskRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) ListTasksByGoal(ctx context.Context, goalID string) ([]service.Task, error) {
	query, _, err := s.goqu.From(s.tableTasks).
		Select(taskColumns...).
		Where(goqu.I("goal_id").Eq(goalID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list tasks by goal query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks by goal %q: %w", goalID, err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		row, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, taskRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) CheckoutTask(ctx context.Context, taskID, agentID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Read current checkout state within the transaction.
	selectQuery, _, err := s.goqu.From(s.tableTasks).
		Select("checked_out_by").
		Where(goqu.I("id").Eq(taskID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build select task query: %w", err)
	}

	var checkedOutBy sql.NullString
	err = tx.QueryRowContext(ctx, selectQuery).Scan(&checkedOutBy)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("task %q not found", taskID)
	}
	if err != nil {
		return fmt.Errorf("read task checkout state: %w", err)
	}

	if checkedOutBy.Valid && checkedOutBy.String != "" && checkedOutBy.String != agentID {
		return fmt.Errorf("task %q already checked out by %q", taskID, checkedOutBy.String)
	}

	now := time.Now().UTC()

	updateQuery, _, err := s.goqu.Update(s.tableTasks).Set(
		goqu.Record{
			"checked_out_by": agentID,
			"checked_out_at": now.Format(time.RFC3339),
			"updated_at":     now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(taskID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update task checkout query: %w", err)
	}

	if _, err := tx.ExecContext(ctx, updateQuery); err != nil {
		return fmt.Errorf("checkout task %q: %w", taskID, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}

func (s *SQLite) ReleaseTask(ctx context.Context, taskID string) error {
	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableTasks).Set(
		goqu.Record{
			"checked_out_by": "",
			"checked_out_at": nil,
			"updated_at":     now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(taskID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build release task query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("release task %q: %w", taskID, err)
	}

	return nil
}

func (s *SQLite) ListChildTasks(ctx context.Context, parentID string) ([]service.Task, error) {
	query, _, err := s.goqu.From(s.tableTasks).
		Select(taskColumns...).
		Where(goqu.I("parent_id").Eq(parentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list child tasks query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list child tasks for parent %q: %w", parentID, err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		row, err := scanTaskRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, taskRowToRecord(row))
	}

	return items, rows.Err()
}

func (s *SQLite) UpdateTaskStatus(ctx context.Context, id string, status string, result string) error {
	now := time.Now().UTC().Format(time.RFC3339)

	record := goqu.Record{
		"status":     status,
		"updated_at": now,
	}
	if result != "" {
		record["result"] = result
	}

	query, _, err := s.goqu.Update(s.tableTasks).Set(record).
		Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update task status query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("update task status %q: %w", id, err)
	}

	return nil
}

func taskRowToRecord(row taskRow) service.Task {
	return service.Task{
		ID:              row.ID,
		OrganizationID:  row.OrganizationID.String,
		ProjectID:       row.ProjectID.String,
		GoalID:          row.GoalID.String,
		ParentID:        row.ParentID.String,
		AssignedAgentID: row.AssignedAgentID.String,
		Identifier:      row.Identifier.String,
		Title:           row.Title,
		Description:     row.Description.String,
		Status:          row.Status,
		PriorityLevel:   row.PriorityLevel.String,
		Priority:        row.Priority,
		Result:          row.Result.String,
		BillingCode:     row.BillingCode.String,
		RequestDepth:    row.RequestDepth,
		CheckedOutBy:    row.CheckedOutBy.String,
		CheckedOutAt:    row.CheckedOutAt.String,
		StartedAt:       row.StartedAt.String,
		CompletedAt:     row.CompletedAt.String,
		CancelledAt:     row.CancelledAt.String,
		HiddenAt:        row.HiddenAt.String,
		CreatedAt:       row.CreatedAt,
		UpdatedAt:       row.UpdatedAt,
		CreatedBy:       row.CreatedBy.String,
		UpdatedBy:       row.UpdatedBy.String,
	}
}
