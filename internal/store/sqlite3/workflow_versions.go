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
)

// ─── Workflow Version CRUD ───

type workflowVersionRow struct {
	ID          string `db:"id"`
	WorkflowID  string `db:"workflow_id"`
	Version     int    `db:"version"`
	Name        string `db:"name"`
	Description string `db:"description"`
	Graph       string `db:"graph"`
	CreatedAt   string `db:"created_at"`
	CreatedBy   string `db:"created_by"`
}

func (s *SQLite) ListWorkflowVersions(ctx context.Context, workflowID string) ([]service.WorkflowVersion, error) {
	query, _, err := s.goqu.From(s.tableWorkflowVersions).
		Select("id", "workflow_id", "version", "name", "description", "graph", "created_at", "created_by").
		Where(goqu.I("workflow_id").Eq(workflowID)).
		Order(goqu.I("version").Desc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list workflow versions query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list workflow versions: %w", err)
	}
	defer rows.Close()

	var result []service.WorkflowVersion
	for rows.Next() {
		var row workflowVersionRow
		if err := rows.Scan(&row.ID, &row.WorkflowID, &row.Version, &row.Name, &row.Description, &row.Graph, &row.CreatedAt, &row.CreatedBy); err != nil {
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

func (s *SQLite) GetWorkflowVersion(ctx context.Context, workflowID string, version int) (*service.WorkflowVersion, error) {
	query, _, err := s.goqu.From(s.tableWorkflowVersions).
		Select("id", "workflow_id", "version", "name", "description", "graph", "created_at", "created_by").
		Where(
			goqu.I("workflow_id").Eq(workflowID),
			goqu.I("version").Eq(version),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get workflow version query: %w", err)
	}

	var row workflowVersionRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.WorkflowID, &row.Version, &row.Name, &row.Description, &row.Graph, &row.CreatedAt, &row.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow version %d for %q: %w", version, workflowID, err)
	}

	return workflowVersionRowToRecord(row)
}

func (s *SQLite) CreateWorkflowVersion(ctx context.Context, v service.WorkflowVersion) (*service.WorkflowVersion, error) {
	graphJSON, err := json.Marshal(v.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow version graph: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	// Compute next version number: MAX(version) + 1 for this workflow.
	maxQuery, _, err := s.goqu.From(s.tableWorkflowVersions).
		Select(goqu.COALESCE(goqu.MAX("version"), 0)).
		Where(goqu.I("workflow_id").Eq(v.WorkflowID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build max version query: %w", err)
	}

	var maxVersion int
	if err := s.db.QueryRowContext(ctx, maxQuery).Scan(&maxVersion); err != nil {
		return nil, fmt.Errorf("get max version for workflow %q: %w", v.WorkflowID, err)
	}
	nextVersion := maxVersion + 1

	query, _, err := s.goqu.Insert(s.tableWorkflowVersions).Rows(
		goqu.Record{
			"id":          id,
			"workflow_id": v.WorkflowID,
			"version":     nextVersion,
			"name":        v.Name,
			"description": v.Description,
			"graph":       string(graphJSON),
			"created_at":  now.Format(time.RFC3339),
			"created_by":  v.CreatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert workflow version query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create workflow version for %q: %w", v.WorkflowID, err)
	}

	return &service.WorkflowVersion{
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

func (s *SQLite) SetActiveVersion(ctx context.Context, workflowID string, version int) error {
	query, _, err := s.goqu.Update(s.tableWorkflows).Set(
		goqu.Record{
			"active_version": version,
			"updated_at":     time.Now().UTC().Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(workflowID)).ToSQL()
	if err != nil {
		return fmt.Errorf("build set active version query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
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

	return nil
}

// workflowVersionRowToRecord converts a database row to a WorkflowVersion.
func workflowVersionRowToRecord(row workflowVersionRow) (*service.WorkflowVersion, error) {
	var graph service.WorkflowGraph
	if err := json.Unmarshal([]byte(row.Graph), &graph); err != nil {
		return nil, fmt.Errorf("unmarshal workflow version graph for %q v%d: %w", row.WorkflowID, row.Version, err)
	}

	return &service.WorkflowVersion{
		ID:          row.ID,
		WorkflowID:  row.WorkflowID,
		Version:     row.Version,
		Name:        row.Name,
		Description: row.Description,
		Graph:       graph,
		CreatedAt:   row.CreatedAt,
		CreatedBy:   row.CreatedBy,
	}, nil
}
