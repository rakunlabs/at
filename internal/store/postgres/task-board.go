package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.TaskBoardStorer = (*Postgres)(nil)

type taskBoardRow struct {
	WorkspaceID string         `db:"workspace_id"`
	Version     int64          `db:"version"`
	Columns     []byte         `db:"columns"`
	UpdatedAt   time.Time      `db:"updated_at"`
	UpdatedBy   sql.NullString `db:"updated_by"`
}

func taskBoardRowToRecord(row taskBoardRow) (*service.TaskBoardSettings, error) {
	var columns []service.TaskBoardColumn
	if err := json.Unmarshal(row.Columns, &columns); err != nil {
		return nil, fmt.Errorf("decode task board columns: %w", err)
	}
	return &service.TaskBoardSettings{
		WorkspaceID: row.WorkspaceID,
		Version:     row.Version,
		Columns:     columns,
		UpdatedAt:   row.UpdatedAt.UTC().Format(time.RFC3339),
		UpdatedBy:   row.UpdatedBy.String,
	}, nil
}

// GetTaskBoard reads the workspace's layout, falling back to the shipped
// default. A workspace that never customised its board has no row, which is the
// common case, so absence is not an error.
func (p *Postgres) GetTaskBoard(ctx context.Context) (*service.TaskBoardSettings, error) {
	scope, err := p.businessReadScope(ctx, p.tableTaskBoard)
	if err != nil {
		return nil, err
	}
	principal, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}

	query, _, err := p.goqu.From(p.tableTaskBoard).
		Select("workspace_id", "version", "columns", "updated_at", "updated_by").
		Where(scope).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get task board query: %w", err)
	}

	var row taskBoardRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.WorkspaceID, &row.Version, &row.Columns, &row.UpdatedAt, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return service.DefaultTaskBoardSettings(principal.WorkspaceID), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get task board: %w", err)
	}
	return taskBoardRowToRecord(row)
}

// SaveTaskBoard upserts the layout under optimistic concurrency. expectedVersion
// 0 means "no layout stored yet"; anything else must match the stored version.
func (p *Postgres) SaveTaskBoard(ctx context.Context, columns []service.TaskBoardColumn, expectedVersion int64, updatedBy string) (*service.TaskBoardSettings, error) {
	cleaned, err := service.ValidateTaskBoardColumns(columns)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(cleaned)
	if err != nil {
		return nil, fmt.Errorf("encode task board columns: %w", err)
	}

	w, err := p.beginBusinessWrite(ctx, p.tableTaskBoard, "tasks.write", "")
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()

	workspaceID := w.actor.WorkspaceID
	if workspaceID == "" {
		return nil, service.ErrWorkspaceRequired
	}
	now := time.Now().UTC()

	var current int64
	query, _, err := w.tx.From(p.tableTaskBoard).Select("version").
		Where(goqu.C("workspace_id").Eq(workspaceID)).ForUpdate(goqu.Wait).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build lock task board query: %w", err)
	}
	switch err := w.tx.QueryRowContext(ctx, query).Scan(&current); {
	case errors.Is(err, sql.ErrNoRows):
		current = 0
	case err != nil:
		return nil, fmt.Errorf("lock task board: %w", err)
	}
	if current != expectedVersion {
		return nil, service.ErrTaskBoardConflict
	}

	next := current + 1
	if current == 0 {
		query, _, err = w.tx.Insert(p.tableTaskBoard).Rows(goqu.Record{
			"workspace_id": workspaceID,
			"version":      next,
			"columns":      string(encoded),
			"updated_at":   now,
			"updated_by":   updatedBy,
		}).ToSQL()
	} else {
		query, _, err = w.tx.Update(p.tableTaskBoard).Set(goqu.Record{
			"version":    next,
			"columns":    string(encoded),
			"updated_at": now,
			"updated_by": updatedBy,
		}).Where(w.predicate, goqu.C("workspace_id").Eq(workspaceID)).ToSQL()
	}
	if err != nil {
		return nil, fmt.Errorf("build save task board query: %w", err)
	}
	res, err := w.tx.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("save task board: %w", err)
	}
	if affected, err := res.RowsAffected(); err == nil && affected != 1 {
		return nil, service.ErrTaskBoardConflict
	}
	if err := w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit task board: %w", err)
	}

	return &service.TaskBoardSettings{
		WorkspaceID: workspaceID,
		Version:     next,
		Columns:     cleaned,
		UpdatedAt:   now.Format(time.RFC3339),
		UpdatedBy:   updatedBy,
	}, nil
}

// ResetTaskBoard removes the stored layout. Deleting nothing is success: the
// workspace is already on the default.
func (p *Postgres) ResetTaskBoard(ctx context.Context) error {
	w, err := p.beginBusinessWrite(ctx, p.tableTaskBoard, "tasks.write", "")
	if err != nil {
		return err
	}
	defer w.tx.Rollback()

	query, _, err := w.tx.Delete(p.tableTaskBoard).Where(w.predicate).ToSQL()
	if err != nil {
		return fmt.Errorf("build reset task board query: %w", err)
	}
	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("reset task board: %w", err)
	}
	if err := w.tx.Commit(); err != nil {
		return fmt.Errorf("commit task board reset: %w", err)
	}
	return nil
}
