package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// CreateExecutionChildTask is the runtime-only writer. It sets workspace_id
// explicitly and creates immutable initiating provenance in the same transaction.
// Legacy business CreateTask remains owned by the workspace rollout.
func (p *Postgres) CreateExecutionChildTask(ctx context.Context, task service.Task) (*service.Task, error) {
	// Same vocabulary guard as the business writer: a runtime caller should get
	// a named error, not a check-constraint violation.
	if task.Status == "" {
		task.Status = service.TaskStatusTodo
	}
	status, statusErr := service.ParseTaskStatus(task.Status)
	if statusErr != nil {
		return nil, statusErr
	}
	task.Status = status

	if task.ParentID == "" {
		return nil, service.ErrExecutionDenied
	}
	return p.CreateExecutionTask(ctx, task)
}

func (p *Postgres) CreateExecutionTask(ctx context.Context, task service.Task) (*service.Task, error) {
	for name, id := range map[string]string{"tasks.run": task.ParentID, "agents.run": task.AssignedAgentID, "organizations.run": task.OrganizationID} {
		if name == "tasks.run" && id == "" {
			continue
		}
		if id == "" {
			return nil, service.ErrExecutionDenied
		}
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: name, ResourceID: id}); err != nil {
			return nil, err
		}
	}
	initiator, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	task.ID = ulid.Make().String()
	now := time.Now().UTC()
	task.CreatedAt, task.UpdatedAt = now.Format(time.RFC3339), now.Format(time.RFC3339)
	task.CreatedBy, task.UpdatedBy = initiator.UserID, initiator.UserID
	initiator.RunID, initiator.Source = "task/"+task.ID, "task"
	data, err := json.Marshal(initiator)
	if err != nil {
		return nil, fmt.Errorf("encode child execution provenance: %w", err)
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin child execution: %w", err)
	}
	defer tx.Rollback()
	// Repeat ownership checks on the transaction used for insertion. Resource
	// references are never accepted on an agent's/model's assertion alone.
	for table, id := range map[string]string{"tasks": task.ParentID, "agents": task.AssignedAgentID, "organizations": task.OrganizationID} {
		if table == "tasks" && id == "" {
			continue
		}
		count, err := tx.From(p.executionTable(table)).Where(goqu.Ex{"id": id, "workspace_id": initiator.WorkspaceID}).CountContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("verify child reference: %w", err)
		}
		if count != 1 {
			return nil, service.ErrExecutionDenied
		}
	}
	_, err = tx.Insert(p.tableTasks).Rows(goqu.Record{
		"id": task.ID, "workspace_id": initiator.WorkspaceID, "organization_id": task.OrganizationID,
		"parent_id": nullString(task.ParentID), "assigned_agent_id": task.AssignedAgentID, "identifier": nullString(task.Identifier),
		"title": task.Title, "description": task.Description, "status": task.Status, "priority": task.Priority,
		"request_depth": task.RequestDepth, "max_iterations": task.MaxIterations,
		"created_at": now, "updated_at": now, "created_by": task.CreatedBy, "updated_by": task.UpdatedBy,
	}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("insert scoped child task: %w", err)
	}
	_, err = tx.Insert(p.executionTable("execution_provenance")).Rows(goqu.Record{"run_id": initiator.RunID, "workspace_id": initiator.WorkspaceID, "data": goqu.L("?::jsonb", string(data))}).Executor().ExecContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("insert child provenance: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit child execution: %w", err)
	}
	return &task, nil
}
