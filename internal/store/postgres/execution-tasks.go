package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/worldline-go/types"

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
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin task execution: %w", err)
	}
	defer tx.Rollback()
	created, err := p.createExecutionTaskTx(ctx, tx, task)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit task execution: %w", err)
	}
	return created, nil
}

func (p *Postgres) createExecutionTaskTx(ctx context.Context, tx *goqu.TxDatabase, task service.Task) (*service.Task, error) {
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
	return &task, nil
}

func (p *Postgres) CreateOrganizationChatTask(ctx context.Context, sessionID string, task service.Task, newTask bool) (*service.Task, bool, error) {
	initiator, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return nil, false, service.ErrExecutionDenied
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin organization chat task: %w", err)
	}
	defer tx.Rollback()
	var session chatSessionRow
	found, err := tx.From(p.tableChatSessions).Where(goqu.Ex{"id": sessionID, "workspace_id": initiator.WorkspaceID}).ForUpdate(goqu.Wait).ScanStructContext(ctx, &session)
	if err != nil {
		return nil, false, fmt.Errorf("lock organization chat: %w", err)
	}
	if !found || (session.OwnerUserID != "" && session.OwnerUserID != initiator.UserID) || session.OrganizationID != task.OrganizationID {
		return nil, false, service.ErrExecutionDenied
	}
	var cfg service.ChatSessionConfig
	if err := json.Unmarshal(session.Config, &cfg); err != nil || !cfg.OrganizationChat {
		return nil, false, service.ErrExecutionDenied
	}
	if cfg.ActiveTaskID != "" {
		var current taskRow
		found, err := tx.From(p.tableTasks).Where(goqu.Ex{"id": cfg.ActiveTaskID, "workspace_id": initiator.WorkspaceID, "organization_id": task.OrganizationID}).ScanStructContext(ctx, &current)
		if err != nil || !found {
			return nil, false, fmt.Errorf("selected organization task is unavailable")
		}
		if !newTask {
			return taskRowToRecord(current), false, nil
		}
		if current.Status != service.TaskStatusDone && current.Status != service.TaskStatusCancelled && current.Status != service.TaskStatusBlocked {
			return nil, false, fmt.Errorf("a task is already pending in this conversation")
		}
	}
	created, err := p.createExecutionTaskTx(ctx, tx, task)
	if err != nil {
		return nil, false, err
	}
	cfg.ActiveTaskID = created.ID
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("encode organization chat: %w", err)
	}
	if _, err := tx.Update(p.tableChatSessions).Set(goqu.Record{"config": types.RawJSON(data), "updated_at": time.Now().UTC()}).Where(goqu.Ex{"id": sessionID}).Executor().ExecContext(ctx); err != nil {
		return nil, false, fmt.Errorf("link organization chat task: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit organization chat task: %w", err)
	}
	return created, true, nil
}
