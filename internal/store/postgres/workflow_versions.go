package postgres

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
	"github.com/worldline-go/types"
)

// ─── Workflow Version CRUD ───

type workflowVersionRow struct {
	WorkspaceID string        `db:"workspace_id"`
	ID          string        `db:"id"`
	WorkflowID  string        `db:"workflow_id"`
	Version     int           `db:"version"`
	Name        string        `db:"name"`
	Description string        `db:"description"`
	Graph       types.RawJSON `db:"graph"`
	CreatedAt   time.Time     `db:"created_at"`
	CreatedBy   string        `db:"created_by"`
}

func (p *Postgres) ListWorkflowVersions(ctx context.Context, workflowID string) ([]service.WorkflowVersion, error) {
	scope, err := p.businessReadScope(ctx, p.tableWorkflowVersions)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableWorkflowVersions).
		Select("id", "workflow_id", "version", "name", "description", "graph", "created_at", "created_by", "workspace_id").
		Where(scope, goqu.I("workflow_id").Eq(workflowID)).
		Order(goqu.I("version").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list workflow versions query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list workflow versions: %w", err)
	}
	defer rows.Close()

	var result []service.WorkflowVersion
	for rows.Next() {
		var row workflowVersionRow
		if err := rows.Scan(&row.ID, &row.WorkflowID, &row.Version, &row.Name, &row.Description, &row.Graph, &row.CreatedAt, &row.CreatedBy, &row.WorkspaceID); err != nil {
			return nil, fmt.Errorf("scan workflow version row: %w", err)
		}

		v, err := workflowVersionRowToRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *v)
	}

	return result, rows.Err()
}

func (p *Postgres) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (*service.WorkflowVersion, error) {
	scope, err := p.businessReadScope(ctx, p.tableWorkflowVersions)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableWorkflowVersions).
		Select("id", "workflow_id", "version", "name", "description", "graph", "created_at", "created_by", "workspace_id").
		Where(
			scope,
			goqu.I("workflow_id").Eq(workflowID),
			goqu.I("version").Eq(version),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get workflow version query: %w", err)
	}

	var row workflowVersionRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.WorkflowID, &row.Version, &row.Name, &row.Description, &row.Graph, &row.CreatedAt, &row.CreatedBy, &row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow version %d for %q: %w", version, workflowID, err)
	}

	return workflowVersionRowToRecord(row)
}

func (p *Postgres) CreateWorkflowVersion(ctx context.Context, v service.WorkflowVersion) (*service.WorkflowVersion, error) {
	w, err := p.beginBusinessWrite(ctx, p.tableWorkflows, "workflows.write", v.WorkflowID)
	if err != nil {
		return nil, err
	}
	defer w.tx.Rollback()
	if v.WorkspaceID != "" && v.WorkspaceID != w.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if v.WorkflowID == "" {
		return nil, service.ErrAccessResourceNotFound
	}
	if err = p.businessReference(ctx, w, p.tableWorkflows, "id", v.WorkflowID); err != nil {
		return nil, err
	}
	if err = p.workflowReferences(ctx, w, v.Graph); err != nil {
		return nil, err
	}
	graphJSON, err := json.Marshal(v.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow version graph: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	// Compute next version number: MAX(version) + 1 for this workflow.
	maxQuery, _, err := p.goqu.From(p.tableWorkflowVersions).
		Select(goqu.COALESCE(goqu.MAX("version"), 0)).
		Where(w.predicate, goqu.I("workflow_id").Eq(v.WorkflowID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build max version query: %w", err)
	}

	var maxVersion int
	if err := w.tx.QueryRowContext(ctx, maxQuery).Scan(&maxVersion); err != nil {
		return nil, fmt.Errorf("get max version for workflow %q: %w", v.WorkflowID, err)
	}
	nextVersion := maxVersion + 1

	query, _, err := p.goqu.Insert(p.tableWorkflowVersions).Rows(
		goqu.Record{
			"workspace_id": w.actor.WorkspaceID,
			"id":           id,
			"workflow_id":  v.WorkflowID,
			"version":      nextVersion,
			"name":         v.Name,
			"description":  v.Description,
			"graph":        types.RawJSON(graphJSON),
			"created_at":   now,
			"created_by":   v.CreatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert workflow version query: %w", err)
	}

	if _, err := w.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create workflow version for %q: %w", v.WorkflowID, err)
	}
	if err = w.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workflow version: %w", err)
	}

	return &service.WorkflowVersion{
		WorkspaceID: w.actor.WorkspaceID,
		ID:          id,
		WorkflowID:  v.WorkflowID,
		Version:     nextVersion,
		Name:        v.Name,
		Description: v.Description,
		Graph:       v.Graph,
		CreatedAt:   now.Format(time.RFC3339),
		CreatedBy:   v.CreatedBy,
	}, nil
}

func (p *Postgres) SetActiveVersion(ctx context.Context, workflowID string, version int) error {
	w, err := p.beginBusinessWrite(ctx, p.tableWorkflows, "workflows.write", workflowID)
	if err != nil {
		return err
	}
	defer w.tx.Rollback()
	var versionID string
	found, err := w.tx.From(p.tableWorkflowVersions).Select("id").Where(w.predicate, goqu.Ex{"workflow_id": workflowID, "version": version}).ForKeyShare(goqu.Wait).ScanValContext(ctx, &versionID)
	if err != nil {
		return fmt.Errorf("validate active workflow version: %w", err)
	}
	if !found {
		return service.ErrAccessResourceNotFound
	}
	query, _, err := p.goqu.Update(p.tableWorkflows).Set(
		goqu.Record{
			"active_version": version,
			"updated_at":     time.Now().UTC(),
		},
	).Where(w.predicate, goqu.I("id").Eq(workflowID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build set active version query: %w", err)
	}

	res, err := w.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("set active version for workflow %q: %w", workflowID, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("workflow %q not found", workflowID)
	}

	return w.tx.Commit()
}

// workflowVersionRowToRecord converts a database row to a WorkflowVersion.
func workflowVersionRowToRecord(row workflowVersionRow) (*service.WorkflowVersion, error) {
	var graph service.WorkflowGraph
	if err := json.Unmarshal(row.Graph, &graph); err != nil {
		return nil, fmt.Errorf("unmarshal workflow version graph for %q v%d: %w", row.WorkflowID, row.Version, err)
	}

	return &service.WorkflowVersion{
		WorkspaceID: row.WorkspaceID,
		ID:          row.ID,
		WorkflowID:  row.WorkflowID,
		Version:     row.Version,
		Name:        row.Name,
		Description: row.Description,
		Graph:       graph,
		CreatedAt:   row.CreatedAt.Format(time.RFC3339),
		CreatedBy:   row.CreatedBy,
	}, nil
}
