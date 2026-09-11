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

// ─── Task CRUD ───

type taskRow struct {
	WorkspaceID     string         `db:"workspace_id"`
	ID              string         `db:"id"`
	OrganizationID  sql.NullString `db:"organization_id"`
	ProjectID       sql.NullString `db:"project_id"`
	GoalID          sql.NullString `db:"goal_id"`
	ParentID        sql.NullString `db:"parent_id"`
	AssignedAgentID sql.NullString `db:"assigned_agent_id"`
	Identifier      sql.NullString `db:"identifier"`
	Title           string         `db:"title"`
	Description     string         `db:"description"`
	Status          string         `db:"status"`
	PriorityLevel   sql.NullString `db:"priority_level"`
	Priority        int            `db:"priority"`
	Result          sql.NullString `db:"result"`
	BillingCode     sql.NullString `db:"billing_code"`
	RequestDepth    int            `db:"request_depth"`
	MaxIterations   int            `db:"max_iterations"`
	CheckedOutBy    sql.NullString `db:"checked_out_by"`
	CheckedOutAt    sql.NullTime   `db:"checked_out_at"`
	StartedAt       sql.NullTime   `db:"started_at"`
	CompletedAt     sql.NullTime   `db:"completed_at"`
	CancelledAt     sql.NullTime   `db:"cancelled_at"`
	HiddenAt        sql.NullTime   `db:"hidden_at"`
	CreatedAt       time.Time      `db:"created_at"`
	UpdatedAt       time.Time      `db:"updated_at"`
	CreatedBy       string         `db:"created_by"`
	UpdatedBy       string         `db:"updated_by"`
}

var taskColumns = []interface{}{
	"id", "organization_id", "project_id", "goal_id", "parent_id", "assigned_agent_id",
	"identifier", "title", "description", "status", "priority_level", "priority", "result",
	"billing_code", "request_depth", "max_iterations", "checked_out_by", "checked_out_at",
	"started_at", "completed_at", "cancelled_at", "hidden_at",
	"created_at", "updated_at", "created_by", "updated_by", "workspace_id",
}

func scanTaskRow(scanner interface {
	Scan(dest ...interface{}) error
}, row *taskRow) error {
	return scanner.Scan(
		&row.ID, &row.OrganizationID, &row.ProjectID, &row.GoalID, &row.ParentID,
		&row.AssignedAgentID, &row.Identifier, &row.Title, &row.Description,
		&row.Status, &row.PriorityLevel, &row.Priority, &row.Result,
		&row.BillingCode, &row.RequestDepth, &row.MaxIterations,
		&row.CheckedOutBy, &row.CheckedOutAt,
		&row.StartedAt, &row.CompletedAt, &row.CancelledAt, &row.HiddenAt,
		&row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy,
		&row.WorkspaceID,
	)
}

func (p *Postgres) ListTasks(ctx context.Context, q *query.Query) (*service.ListResult[service.Task], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableTasks, q, taskColumns...)
	if err != nil {
		return nil, fmt.Errorf("build list tasks query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		var row taskRow
		if err := scanTaskRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, *taskRowToRecord(row))
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

func (p *Postgres) GetTask(ctx context.Context, id string) (*service.Task, error) {
	scope, err := p.businessReadScope(ctx, p.tableTasks)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTasks).
		Select(taskColumns...).
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get task query: %w", err)
	}

	var row taskRow
	err = scanTaskRow(p.db.QueryRowContext(ctx, query), &row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task %q: %w", id, err)
	}

	return taskRowToRecord(row), nil
}

func (p *Postgres) CreateTask(ctx context.Context, task service.Task) (*service.Task, error) {
	// Fold retired synonyms and reject anything outside the vocabulary here, so
	// an internal caller gets a named error instead of a raw check-constraint
	// violation from Postgres.
	if task.Status == "" {
		task.Status = service.TaskStatusTodo
	}
	status, err := service.ParseTaskStatus(task.Status)
	if err != nil {
		return nil, err
	}
	task.Status = status

	w, err := p.beginBusinessWrite(ctx, p.tableTasks, "tasks.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if task.WorkspaceID != "" && task.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if err = p.taskReferences(ctx, w, task); err != nil {
		return nil, err
	}
	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := w.tx.Insert(p.tableTasks).Rows(
		goqu.Record{
			"workspace_id":      w.actor.WorkspaceID,
			"id":                id,
			"organization_id":   nullString(task.OrganizationID),
			"project_id":        nullString(task.ProjectID),
			"goal_id":           nullString(task.GoalID),
			"parent_id":         nullString(task.ParentID),
			"assigned_agent_id": nullString(task.AssignedAgentID),
			"identifier":        nullString(task.Identifier),
			"title":             task.Title,
			"description":       task.Description,
			"status":            task.Status,
			"priority_level":    nullString(task.PriorityLevel),
			"priority":          task.Priority,
			"result":            nullString(task.Result),
			"billing_code":      nullString(task.BillingCode),
			"request_depth":     task.RequestDepth,
			"max_iterations":    task.MaxIterations,
			"checked_out_by":    nil,
			"checked_out_at":    nil,
			"started_at":        nil,
			"completed_at":      nil,
			"cancelled_at":      nil,
			"hidden_at":         nil,
			"created_at":        now,
			"updated_at":        now,
			"created_by":        task.CreatedBy,
			"updated_by":        task.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert task query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create task %q: %w", task.Title, err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit task: %w", err)
	}

	return &service.Task{
		WorkspaceID:     w.actor.WorkspaceID,
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
		MaxIterations:   task.MaxIterations,
		CreatedAt:       now.Format(time.RFC3339),
		UpdatedAt:       now.Format(time.RFC3339),
		CreatedBy:       task.CreatedBy,
		UpdatedBy:       task.UpdatedBy,
	}, nil
}

// taskStatusStamps returns the lifecycle timestamp columns a status change
// should set, given what is already stamped. Entering a state stamps it once;
// leaving and re-entering keeps the original moment.
//
// Both write paths use this. UpdateTask used to skip it entirely, so a task
// moved to `done` through the generic update — which is what the agent tool and
// the REST handler both call — ended up terminal with no completed_at. That
// silently broke duration reporting and made the workspace janitor skip the
// task forever, because it sweeps on the terminal timestamp.
func taskStatusStamps(status, startedAt, completedAt, cancelledAt string, now time.Time) map[string]any {
	stamps := map[string]any{}
	if status == service.TaskStatusInProgress && startedAt == "" {
		stamps["started_at"] = now
	}
	if status == service.TaskStatusDone && completedAt == "" {
		stamps["completed_at"] = now
	}
	if status == service.TaskStatusCancelled && cancelledAt == "" {
		stamps["cancelled_at"] = now
	}
	return stamps
}

func (p *Postgres) UpdateTask(ctx context.Context, id string, task service.Task) (*service.Task, error) {
	// Fold retired synonyms and reject anything outside the vocabulary here, so
	// an internal caller gets a named error instead of a raw check-constraint
	// violation from Postgres.
	if task.Status == "" {
		task.Status = service.TaskStatusTodo
	}
	status, err := service.ParseTaskStatus(task.Status)
	if err != nil {
		return nil, err
	}
	task.Status = status

	w, err := p.beginBusinessWrite(ctx, p.tableTasks, "tasks.write", id)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if task.WorkspaceID != "" && task.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if task.ParentID == id {
		return nil, service.ErrWorkspaceConflict
	}
	if err = p.taskReferences(ctx, w, task); err != nil {
		return nil, err
	}
	now := time.Now().UTC()

	// Stamp the lifecycle timestamps this status change implies before writing.
	stamped := task
	for column, value := range taskStatusStamps(task.Status, task.StartedAt, task.CompletedAt, task.CancelledAt, now) {
		stamp, _ := value.(time.Time)
		switch column {
		case "started_at":
			stamped.StartedAt = stamp.Format(time.RFC3339)
		case "completed_at":
			stamped.CompletedAt = stamp.Format(time.RFC3339)
		case "cancelled_at":
			stamped.CancelledAt = stamp.Format(time.RFC3339)
		}
	}

	query, _, err := w.tx.Update(p.tableTasks).Set(
		goqu.Record{
			"organization_id":   nullString(task.OrganizationID),
			"project_id":        nullString(task.ProjectID),
			"goal_id":           nullString(task.GoalID),
			"parent_id":         nullString(task.ParentID),
			"assigned_agent_id": nullString(task.AssignedAgentID),
			"identifier":        nullString(task.Identifier),
			"title":             task.Title,
			"description":       task.Description,
			"status":            task.Status,
			"priority_level":    nullString(task.PriorityLevel),
			"priority":          task.Priority,
			"result":            nullString(task.Result),
			"billing_code":      nullString(task.BillingCode),
			"request_depth":     task.RequestDepth,
			"max_iterations":    task.MaxIterations,
			"started_at":        nullTimeString(stamped.StartedAt),
			"completed_at":      nullTimeString(stamped.CompletedAt),
			"cancelled_at":      nullTimeString(stamped.CancelledAt),
			"hidden_at":         nullTimeString(task.HiddenAt),
			"updated_at":        now,
			"updated_by":        task.UpdatedBy,
		},
	).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update task query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
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
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit task update: %w", err)
	}

	return p.GetTask(ctx, id)
}

func (p *Postgres) DeleteTask(ctx context.Context, id string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableTasks, "tasks.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	query, _, err := w.tx.Delete(p.tableTasks).
		Where(w.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete task query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete task %q: %w", id, err)
	}

	return w.tx.Commit()
}

func (p *Postgres) ListTasksByAgent(ctx context.Context, agentID string) ([]service.Task, error) {
	scope, err := p.businessReadScope(ctx, p.tableTasks)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTasks).
		Select(taskColumns...).
		Where(scope, goqu.I("assigned_agent_id").Eq(agentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list tasks by agent query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks by agent %q: %w", agentID, err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		var row taskRow
		if err := scanTaskRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, *taskRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) ListTasksByGoal(ctx context.Context, goalID string) ([]service.Task, error) {
	scope, err := p.businessReadScope(ctx, p.tableTasks)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTasks).
		Select(taskColumns...).
		Where(scope, goqu.I("goal_id").Eq(goalID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list tasks by goal query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks by goal %q: %w", goalID, err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		var row taskRow
		if err := scanTaskRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, *taskRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) CheckoutTask(ctx context.Context, taskID, agentID string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableTasks, "tasks.write", taskID)
	if err != nil {
		return err
	}
	tx := w.tx
	defer tx.Rollback() //nolint:errcheck
	if agentID == "" {
		return service.ErrAccessResourceNotFound
	}
	if err = p.businessReference(ctx, w, p.tableAgents, "id", agentID); err != nil {
		return err
	}

	// SELECT the task to check current checkout status.
	selectQuery, _, err := p.goqu.From(p.tableTasks).
		Select("checked_out_by").
		Where(w.predicate, goqu.I("id").Eq(taskID)).ForUpdate(goqu.Wait).
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
		return fmt.Errorf("select task %q: %w", taskID, err)
	}

	// Verify the task is not already checked out by another agent.
	if checkedOutBy.Valid && checkedOutBy.String != "" && checkedOutBy.String != agentID {
		return fmt.Errorf("task %q is already checked out by agent %q", taskID, checkedOutBy.String)
	}

	// UPDATE the checkout fields.
	now := time.Now().UTC()
	updateQuery, _, err := p.goqu.Update(p.tableTasks).Set(
		goqu.Record{
			"checked_out_by": agentID,
			"checked_out_at": now,
			"updated_at":     now,
		},
	).Where(w.predicate, goqu.I("id").Eq(taskID)).ToSQL()
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

func (p *Postgres) ReleaseTask(ctx context.Context, taskID string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableTasks, "tasks.write", taskID)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableTasks).Set(
		goqu.Record{
			"checked_out_by": "",
			"checked_out_at": nil,
			"updated_at":     now,
		},
	).Where(w.predicate, goqu.I("id").Eq(taskID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build release task query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("release task %q: %w", taskID, err)
	}

	return w.tx.Commit()
}

func (p *Postgres) ListChildTasks(ctx context.Context, parentID string) ([]service.Task, error) {
	scope, err := p.businessReadScope(ctx, p.tableTasks)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableTasks).
		Select(taskColumns...).
		Where(scope, goqu.I("parent_id").Eq(parentID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list child tasks query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list child tasks for parent %q: %w", parentID, err)
	}
	defer rows.Close()

	var items []service.Task
	for rows.Next() {
		var row taskRow
		if err := scanTaskRow(rows, &row); err != nil {
			return nil, fmt.Errorf("scan task row: %w", err)
		}

		items = append(items, *taskRowToRecord(row))
	}

	return items, rows.Err()
}

func (p *Postgres) UpdateTaskStatus(ctx context.Context, id string, status string, result string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableTasks, "tasks.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	now := time.Now().UTC()
	var row taskRow
	found, err := w.tx.From(p.tableTasks).Select(taskColumns...).Where(w.predicate, goqu.C("id").Eq(id)).ForUpdate(goqu.Wait).ScanStructContext(ctx, &row)
	if err != nil {
		return fmt.Errorf("get task for status update %q: %w", id, err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	current := taskRowToRecord(row)

	status, err = service.ParseTaskStatus(status)
	if err != nil {
		return err
	}

	record := goqu.Record{
		"status":     status,
		"updated_at": now,
	}
	if result != "" {
		record["result"] = result
	}
	var started, completed, cancelled string
	if current != nil {
		started, completed, cancelled = current.StartedAt, current.CompletedAt, current.CancelledAt
	}
	for column, value := range taskStatusStamps(status, started, completed, cancelled, now) {
		record[column] = value
	}

	query, _, err := p.goqu.Update(p.tableTasks).Set(record).
		Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update task status query: %w", err)
	}

	_, err = w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("update task status %q: %w", id, err)
	}

	return w.tx.Commit()
}

func (p *Postgres) UpdateTaskResult(ctx context.Context, id string, result string) error {
	w, err := p.beginBusinessWrite(ctx, p.tableTasks, "tasks.write", id)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	query, _, err := p.goqu.Update(p.tableTasks).Set(goqu.Record{
		"result":     nullString(result),
		"updated_at": time.Now().UTC(),
	}).Where(w.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return fmt.Errorf("build update task result query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("update task result %q: %w", id, err)
	}

	return w.tx.Commit()
}

func taskRowToRecord(row taskRow) *service.Task {
	var checkedOutAt string
	if row.CheckedOutAt.Valid {
		checkedOutAt = row.CheckedOutAt.Time.Format(time.RFC3339)
	}

	var startedAt string
	if row.StartedAt.Valid {
		startedAt = row.StartedAt.Time.Format(time.RFC3339)
	}

	var completedAt string
	if row.CompletedAt.Valid {
		completedAt = row.CompletedAt.Time.Format(time.RFC3339)
	}

	var cancelledAt string
	if row.CancelledAt.Valid {
		cancelledAt = row.CancelledAt.Time.Format(time.RFC3339)
	}

	var hiddenAt string
	if row.HiddenAt.Valid {
		hiddenAt = row.HiddenAt.Time.Format(time.RFC3339)
	}

	return &service.Task{
		WorkspaceID:     row.WorkspaceID,
		ID:              row.ID,
		OrganizationID:  row.OrganizationID.String,
		ProjectID:       row.ProjectID.String,
		GoalID:          row.GoalID.String,
		ParentID:        row.ParentID.String,
		AssignedAgentID: row.AssignedAgentID.String,
		Identifier:      row.Identifier.String,
		Title:           row.Title,
		Description:     row.Description,
		Status:          row.Status,
		PriorityLevel:   row.PriorityLevel.String,
		Priority:        row.Priority,
		Result:          row.Result.String,
		BillingCode:     row.BillingCode.String,
		RequestDepth:    row.RequestDepth,
		MaxIterations:   row.MaxIterations,
		CheckedOutBy:    row.CheckedOutBy.String,
		CheckedOutAt:    checkedOutAt,
		StartedAt:       startedAt,
		CompletedAt:     completedAt,
		CancelledAt:     cancelledAt,
		HiddenAt:        hiddenAt,
		CreatedAt:       row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
	}
}
