package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

// Boot maintenance reads ownership/status only, never prompts or credentials.
func (p *Postgres) GetExecutionCleanupTask(ctx context.Context, workspace, id string) (*service.Task, error) {
	if !service.HasExecutionMaintenance(ctx) || workspace == "" || id == "" {
		return nil, service.ErrExecutionDenied
	}
	var row struct {
		Status    string       `db:"status"`
		Completed sql.NullTime `db:"completed_at"`
		Cancelled sql.NullTime `db:"cancelled_at"`
		Updated   time.Time    `db:"updated_at"`
	}
	found, err := p.goqu.From(p.tableTasks).Select("status", "completed_at", "cancelled_at", "updated_at").Where(goqu.Ex{"workspace_id": workspace, "id": id}).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("load scoped cleanup metadata: %w", err)
	}
	if !found {
		return nil, nil
	}
	task := &service.Task{ID: id, Status: row.Status, UpdatedAt: row.Updated.UTC().Format(time.RFC3339)}
	if row.Completed.Valid {
		task.CompletedAt = row.Completed.Time.UTC().Format(time.RFC3339)
	}
	if row.Cancelled.Valid {
		task.CancelledAt = row.Cancelled.Time.UTC().Format(time.RFC3339)
	}
	return task, nil
}
