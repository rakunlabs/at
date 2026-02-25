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

// ─── Workflow CRUD ───

type workflowRow struct {
	ID          string `db:"id"`
	Name        string `db:"name"`
	Description string `db:"description"`
	Graph       string `db:"graph"`
	CreatedAt   string `db:"created_at"`
	UpdatedAt   string `db:"updated_at"`
}

func (s *SQLite) ListWorkflows(ctx context.Context) ([]service.Workflow, error) {
	query, _, err := s.goqu.From(s.tableWorkflows).
		Select("id", "name", "description", "graph", "created_at", "updated_at").
		Order(goqu.I("name").Asc()).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list workflows query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list workflows: %w", err)
	}
	defer rows.Close()

	var result []service.Workflow
	for rows.Next() {
		var row workflowRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.Graph, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan workflow row: %w", err)
		}

		w, err := workflowRowToRecord(row)
		if err != nil {
			return nil, err
		}
		result = append(result, *w)
	}

	return result, rows.Err()
}

func (s *SQLite) GetWorkflow(ctx context.Context, id string) (*service.Workflow, error) {
	query, _, err := s.goqu.From(s.tableWorkflows).
		Select("id", "name", "description", "graph", "created_at", "updated_at").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get workflow query: %w", err)
	}

	var row workflowRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.Graph, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get workflow %q: %w", id, err)
	}

	return workflowRowToRecord(row)
}

func (s *SQLite) CreateWorkflow(ctx context.Context, w service.Workflow) (*service.Workflow, error) {
	graphJSON, err := json.Marshal(w.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow graph: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	query, _, err := s.goqu.Insert(s.tableWorkflows).Rows(
		goqu.Record{
			"id":          id,
			"name":        w.Name,
			"description": w.Description,
			"graph":       string(graphJSON),
			"created_at":  now.Format(time.RFC3339),
			"updated_at":  now.Format(time.RFC3339),
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert workflow query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create workflow %q: %w", w.Name, err)
	}

	return &service.Workflow{
		ID:          id,
		Name:        w.Name,
		Description: w.Description,
		Graph:       w.Graph,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
	}, nil
}

func (s *SQLite) UpdateWorkflow(ctx context.Context, id string, w service.Workflow) (*service.Workflow, error) {
	graphJSON, err := json.Marshal(w.Graph)
	if err != nil {
		return nil, fmt.Errorf("marshal workflow graph: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableWorkflows).Set(
		goqu.Record{
			"name":        w.Name,
			"description": w.Description,
			"graph":       string(graphJSON),
			"updated_at":  now.Format(time.RFC3339),
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update workflow query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
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

	return s.GetWorkflow(ctx, id)
}

func (s *SQLite) DeleteWorkflow(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableWorkflows).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete workflow query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete workflow %q: %w", id, err)
	}

	return nil
}

// workflowRowToRecord converts a database row to a Workflow.
func workflowRowToRecord(row workflowRow) (*service.Workflow, error) {
	var graph service.WorkflowGraph
	if err := json.Unmarshal([]byte(row.Graph), &graph); err != nil {
		return nil, fmt.Errorf("unmarshal workflow graph for %q: %w", row.ID, err)
	}

	return &service.Workflow{
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Graph:       graph,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}, nil
}
