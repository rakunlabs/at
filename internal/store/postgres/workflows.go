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
	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── Workflow CRUD ───

type workflowRow struct {
	WorkspaceID   string        `db:"workspace_id"`
	ID            string        `db:"id"`
	Name          string        `db:"name"`
	Description   string        `db:"description"`
	Graph         types.RawJSON `db:"graph"`
	ActiveVersion *int          `db:"active_version"`
	CreatedAt     time.Time     `db:"created_at"`
	UpdatedAt     time.Time     `db:"updated_at"`
	CreatedBy     string        `db:"created_by"`
	UpdatedBy     string        `db:"updated_by"`
}

func (p *Postgres) ListWorkflows(ctx context.Context, q *query.Query) (*service.ListResult[service.Workflow], error) {
	sql, total, err := p.buildListQuery(ctx, p.tableWorkflows, q, "id", "name", "description", "graph", "active_version", "created_at", "updated_at", "created_by", "updated_by", "workspace_id")
	if err != nil {
		return nil, fmt.Errorf("build list workflows query: %w", err)
	}

	rows, err := p.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	var items []service.Workflow
	for rows.Next() {
		var row workflowRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Graph, &row.ActiveVersion, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID); err != nil {
			return nil, fmt.Errorf("scan workflow row: %w", err)
		}

		w, err := workflowRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *w)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.Workflow]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (p *Postgres) GetWorkflow(ctx context.Context, id string) (*service.Workflow, error) {
	scope, err := p.businessReadScope(ctx, p.tableWorkflows)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableWorkflows).
		Select("id", "name", "description", "graph", "active_version", "created_at", "updated_at", "created_by", "updated_by", "workspace_id").
		Where(scope, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get workflow query: %w", err)
	}

	var row workflowRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Graph, &row.ActiveVersion, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow %q: %w", id, err)
	}

	return workflowRowToRecord(row)
}

func (p *Postgres) GetWorkflowByName(ctx context.Context, name string) (*service.Workflow, error) {
	scope, err := p.businessReadScope(ctx, p.tableWorkflows)
	if err != nil {
		return nil, err
	}
	query, _, err := p.goqu.From(p.tableWorkflows).
		Select("id", "name", "description", "graph", "active_version", "created_at", "updated_at", "created_by", "updated_by", "workspace_id").
		Where(scope, goqu.I("name").Eq(name)).
		Limit(1).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get workflow by name query: %w", err)
	}

	var row workflowRow
	err = p.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Graph, &row.ActiveVersion, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy, &row.WorkspaceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow by name %q: %w", name, err)
	}

	return workflowRowToRecord(row)
}

func (p *Postgres) CreateWorkflow(ctx context.Context, w service.Workflow) (*service.Workflow, error) {
	bw, err := p.beginBusinessWrite(ctx, p.tableWorkflows, "workflows.write", "")
	if err != nil {
		return nil, err
	}
	defer bw.tx.Rollback()
	if w.WorkspaceID != "" && w.WorkspaceID != bw.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if err = p.workflowReferences(ctx, bw, w.Graph); err != nil {
		return nil, err
	}
	graphJSON, err := json.Marshal(w.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow graph: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := p.goqu.Insert(p.tableWorkflows).Rows(
		goqu.Record{
			"workspace_id": bw.actor.WorkspaceID,
			"id":           id,
			"name":         w.Name,
			"description":  w.Description,
			"graph":        types.RawJSON(graphJSON),
			"created_at":   now,
			"updated_at":   now,
			"created_by":   w.CreatedBy,
			"updated_by":   w.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert workflow query: %w", err)
	}

	if _, err := bw.tx.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create workflow %q: %w", w.Name, err)
	}
	if err = bw.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workflow: %w", err)
	}

	return &service.Workflow{
		WorkspaceID: bw.actor.WorkspaceID,
		ID:          id,
		Name:        w.Name,
		Description: w.Description,
		Graph:       w.Graph,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
		CreatedBy:   w.CreatedBy,
		UpdatedBy:   w.UpdatedBy,
	}, nil
}

func (p *Postgres) UpdateWorkflow(ctx context.Context, id string, w service.Workflow) (*service.Workflow, error) {
	bw, err := p.beginBusinessWrite(ctx, p.tableWorkflows, "workflows.write", id)
	if err != nil {
		return nil, err
	}
	defer bw.tx.Rollback()
	if w.WorkspaceID != "" && w.WorkspaceID != bw.actor.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if err = p.workflowReferences(ctx, bw, w.Graph); err != nil {
		return nil, err
	}
	graphJSON, err := json.Marshal(w.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow graph: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := p.goqu.Update(p.tableWorkflows).Set(
		goqu.Record{
			"name":        w.Name,
			"description": w.Description,
			"graph":       types.RawJSON(graphJSON),
			"updated_at":  now,
			"updated_by":  w.UpdatedBy,
		},
	).Where(bw.predicate, goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update workflow query: %w", err)
	}

	res, err := bw.tx.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update workflow %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}
	if err = bw.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit workflow update: %w", err)
	}

	return p.GetWorkflow(ctx, id)
}

func (p *Postgres) DeleteWorkflow(ctx context.Context, id string) error {
	bw, err := p.beginBusinessWrite(ctx, p.tableWorkflows, "workflows.write", id)
	if err != nil {
		return err
	}
	defer bw.tx.Rollback()
	query, _, err := p.goqu.Delete(p.tableWorkflows).
		Where(bw.predicate, goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete workflow query: %w", err)
	}

	_, err = bw.tx.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete workflow %q: %w", id, err)
	}

	return bw.tx.Commit()
}

// workflowRowToRecord converts a database row to a Workflow.
func workflowRowToRecord(row workflowRow) (*service.Workflow, error) {
	var graph service.WorkflowGraph
	if err := json.Unmarshal(row.Graph, &graph); err != nil {
		return nil, fmt.Errorf("unmarshal workflow graph for %q: %w", row.ID, err)
	}

	return &service.Workflow{
		WorkspaceID:   row.WorkspaceID,
		ID:            row.ID,
		Name:          row.Name,
		Description:   row.Description,
		Graph:         graph,
		ActiveVersion: row.ActiveVersion,
		CreatedAt:     row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:     row.UpdatedAt.Format(time.RFC3339),
		CreatedBy:     row.CreatedBy,
		UpdatedBy:     row.UpdatedBy,
	}, nil
}
