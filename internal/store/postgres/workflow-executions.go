package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.WorkflowExecutionStorer = (*Postgres)(nil)

type workflowExecutionRow struct {
	ID              string     `db:"id"`
	WorkspaceID     string     `db:"workspace_id"`
	WorkflowID      string     `db:"workflow_id"`
	OwnerUserID     string     `db:"owner_user_id"`
	Source          string     `db:"source"`
	Status          string     `db:"status"`
	Revision        int64      `db:"revision"`
	LeaseOwner      string     `db:"lease_owner"`
	LeaseUntil      *time.Time `db:"lease_until"`
	ResumeRequested bool       `db:"resume_requested"`
	WaitNodeID      string     `db:"wait_node_id"`
	WaitMode        string     `db:"wait_mode"`
	WaitPrompt      string     `db:"wait_prompt"`
	WakeAt          *time.Time `db:"wake_at"`
	ExpiresAt       *time.Time `db:"expires_at"`
	DecisionBy      string     `db:"decision_by"`
	Error           string     `db:"error"`
	Payload         []byte     `db:"payload"`
	Checkpoint      []byte     `db:"checkpoint"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
}

func executionRecord(row workflowExecutionRow) (*service.WorkflowExecution, error) {
	v := &service.WorkflowExecution{ID: row.ID, WorkspaceID: row.WorkspaceID, WorkflowID: row.WorkflowID, OwnerUserID: row.OwnerUserID, Status: row.Status, Revision: row.Revision, LeaseOwner: row.LeaseOwner, LeaseUntil: row.LeaseUntil, ResumeRequested: row.ResumeRequested, WaitNodeID: row.WaitNodeID, WaitMode: row.WaitMode, WaitPrompt: row.WaitPrompt, WakeAt: row.WakeAt, ExpiresAt: row.ExpiresAt, DecisionBy: row.DecisionBy, Error: row.Error, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if len(row.Payload) > 0 {
		if err := json.Unmarshal(row.Payload, &v.Payload); err != nil {
			return nil, fmt.Errorf("decode execution payload: %w", err)
		}
	}
	if len(row.Checkpoint) > 0 {
		if err := json.Unmarshal(row.Checkpoint, &v.Checkpoint); err != nil {
			return nil, fmt.Errorf("decode execution checkpoint: %w", err)
		}
	}
	v.Payload.Source = row.Source
	return v, nil
}

func (p *Postgres) workflowExecutionScope(ctx context.Context, workflowID, capability string) (exp.Expression, error) {
	wf, err := p.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if wf == nil || a.WorkspaceID != wf.WorkspaceID || !a.Allows(capability, service.AccessResource{Kind: "workflows", ID: workflowID, WorkspaceID: wf.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	scope := goqu.And(goqu.C("workflow_id").Eq(workflowID), goqu.C("workspace_id").Eq(a.WorkspaceID))
	if !a.PlatformAdmin && service.WorkspaceRoleRank(a.Role) < 3 {
		scope = goqu.And(scope, goqu.C("owner_user_id").Eq(a.UserID))
	}
	return scope, nil
}

func (p *Postgres) CreateWorkflowExecution(ctx context.Context, value service.WorkflowExecution) (*service.WorkflowExecution, error) {
	if value.Payload.Source != "api" && value.Payload.Source != "cron" && value.Payload.Source != "webhook" {
		return nil, fmt.Errorf("invalid durable workflow source")
	}
	provenance, _, bound := service.ExecutionFromContext(ctx)
	if !bound || provenance.RunID != value.ID || provenance.WorkspaceID != value.WorkspaceID || provenance.UserID != value.OwnerUserID {
		return nil, service.ErrExecutionDenied
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: value.WorkflowID}); err != nil {
		return nil, err
	}
	bw, err := p.beginBusinessWrite(ctx, p.tableWorkflows, "workflows.execute", value.WorkflowID)
	if err != nil {
		return nil, err
	}
	defer bw.tx.Rollback()
	if bw.actor.WorkspaceID != value.WorkspaceID {
		return nil, service.ErrAccessDenied
	}
	if err := p.businessReference(ctx, bw, p.tableWorkflows, "id", value.WorkflowID); err != nil {
		return nil, err
	}
	data, err := json.Marshal(value.Payload)
	if err != nil || len(data) > 8<<20 {
		return nil, fmt.Errorf("durable payload must be JSON of at most 8 MiB")
	}
	var row workflowExecutionRow
	_, err = bw.tx.Insert(p.executionTable("workflow_executions")).Rows(goqu.Record{
		"id": value.ID, "workspace_id": value.WorkspaceID, "workflow_id": value.WorkflowID, "owner_user_id": value.OwnerUserID,
		"status": "queued", "source": value.Payload.Source, "payload": goqu.L("?::jsonb", string(data)),
	}).Returning(goqu.Star()).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("create durable workflow: %w", err)
	}
	if err := bw.tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit durable workflow: %w", err)
	}
	return executionRecord(row)
}

func (p *Postgres) GetWorkflowExecution(ctx context.Context, workflowID, id string) (*service.WorkflowExecution, error) {
	scope, err := p.workflowExecutionScope(ctx, workflowID, "workflows.read")
	if err != nil {
		return nil, err
	}
	var row workflowExecutionRow
	found, err := p.goqu.From(p.executionTable("workflow_executions")).Where(scope, goqu.C("id").Eq(id)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get durable workflow: %w", err)
	}
	if !found {
		return nil, nil
	}
	return executionRecord(row)
}

func (p *Postgres) ListWorkflowExecutions(ctx context.Context, workflowID string) ([]service.WorkflowExecution, error) {
	scope, err := p.workflowExecutionScope(ctx, workflowID, "workflows.read")
	if err != nil {
		return nil, err
	}
	var rows []workflowExecutionRow
	err = p.goqu.From(p.executionTable("workflow_executions")).Select("id", "workspace_id", "workflow_id", "owner_user_id", "source", "status", "revision", "wait_node_id", "wait_mode", "wait_prompt", "wake_at", "expires_at", "decision_by", "error", "created_at", "updated_at").Where(scope).Order(goqu.L("CASE WHEN status IN ('queued','running','waiting') THEN 0 ELSE 1 END").Asc(), goqu.C("created_at").Desc()).Limit(50).ScanStructsContext(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list durable workflows: %w", err)
	}
	items := make([]service.WorkflowExecution, 0, len(rows))
	for _, row := range rows {
		value, err := executionRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *value)
	}
	return items, nil
}

func (p *Postgres) DecideWorkflowExecution(ctx context.Context, workflowID, id string, revision int64, action string) error {
	bw, err := p.beginBusinessWrite(ctx, p.tableWorkflows, "workflows.execute", workflowID)
	if err != nil {
		return err
	}
	defer bw.tx.Rollback()
	if err := p.businessReference(ctx, bw, p.tableWorkflows, "id", workflowID); err != nil {
		return err
	}
	a := bw.actor
	scope := goqu.And(goqu.C("workflow_id").Eq(workflowID), goqu.C("workspace_id").Eq(a.WorkspaceID))
	if !a.PlatformAdmin && service.WorkspaceRoleRank(a.Role) < 3 {
		scope = goqu.And(scope, goqu.C("owner_user_id").Eq(a.UserID))
	}
	where := goqu.And(scope, goqu.Ex{"id": id, "revision": revision})
	values := goqu.Record{"revision": goqu.L("revision + 1"), "updated_at": goqu.L("clock_timestamp()"), "decision_by": a.UserID}
	switch action {
	case "approve", "reject":
		where = goqu.And(where, goqu.Ex{"status": "waiting", "wait_mode": "approval"}, goqu.L("expires_at > clock_timestamp()"))
		if action == "approve" {
			values["status"], values["resume_requested"] = "queued", true
		} else {
			values["status"], values["error"] = "cancelled", "Approval rejected"
		}
	case "cancel":
		where = goqu.And(where, goqu.C("status").In("queued", "running", "waiting"))
		values["status"], values["error"] = "cancelled", "Cancelled by user"
	default:
		return service.ErrWorkflowExecutionConflict
	}
	result, err := bw.tx.Update(p.executionTable("workflow_executions")).Set(values).Where(where).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("decide durable workflow: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrWorkflowExecutionConflict
	}
	return bw.tx.Commit()
}

// Claim does not replay an expired running step. A crash can happen between an
// external side effect and its checkpoint, so uncertain work becomes blocked.
func (p *Postgres) ClaimWorkflowExecution(ctx context.Context, worker string, sources ...string) (*service.WorkflowExecution, error) {
	if !service.HasExecutionMaintenance(ctx) || worker == "" {
		return nil, service.ErrExecutionDenied
	}
	table := p.executionTable("workflow_executions")
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	old := tx.From(table).Select("id").Where(goqu.C("status").In("completed", "blocked", "cancelled", "expired"), goqu.L("updated_at < clock_timestamp() - interval '30 days'")).Limit(50).ForUpdate(goqu.SkipLocked)
	if _, err := tx.Delete(table).Where(goqu.C("id").In(old)).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("expire durable history: %w", err)
	}
	// Bounded maintenance avoids full-table writes on the worker polling path.
	for _, sweep := range []struct {
		predicate exp.Expression
		values    goqu.Record
	}{
		{goqu.And(goqu.Ex{"status": "running"}, goqu.L("lease_until < clock_timestamp()"), goqu.L("COALESCE(checkpoint->>'in_flight', '') <> ''")), goqu.Record{"status": "blocked", "error": "Worker interrupted; in-flight side effects are uncertain. Start a new run only after inspection."}},
		{goqu.And(goqu.Ex{"status": "running"}, goqu.L("lease_until < clock_timestamp()"), goqu.L("COALESCE(checkpoint->>'in_flight', '') = ''")), goqu.Record{"status": "queued", "lease_owner": "", "lease_until": nil}},
		{goqu.And(goqu.Ex{"status": "waiting", "wait_mode": "approval"}, goqu.L("expires_at <= clock_timestamp()")), goqu.Record{"status": "expired", "error": "Approval expired"}},
		{goqu.And(goqu.Ex{"status": "waiting", "wait_mode": "duration"}, goqu.L("wake_at <= clock_timestamp()")), goqu.Record{"status": "queued", "resume_requested": true}},
	} {
		ids := tx.From(table).Select("id").Where(sweep.predicate).Limit(50).ForUpdate(goqu.SkipLocked)
		sweep.values["revision"], sweep.values["updated_at"] = goqu.L("revision + 1"), goqu.L("clock_timestamp()")
		if _, err := tx.Update(table).Set(sweep.values).Where(goqu.C("id").In(ids)).Executor().ExecContext(ctx); err != nil {
			return nil, fmt.Errorf("sweep durable workflows: %w", err)
		}
	}
	var row workflowExecutionRow
	query := tx.From(table).Where(goqu.Ex{"status": "queued"})
	if len(sources) > 0 {
		query = query.Where(goqu.C("source").In(sources))
	}
	found, err := query.Order(goqu.C("created_at").Asc()).Limit(1).ForUpdate(goqu.SkipLocked).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("claim durable workflow: %w", err)
	}
	if !found {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, nil
	}
	_, err = tx.Update(table).Set(goqu.Record{"status": "running", "lease_owner": worker, "lease_until": goqu.L("clock_timestamp() + interval '120 seconds'"), "revision": goqu.L("revision + 1"), "updated_at": goqu.L("clock_timestamp()")}).Where(goqu.Ex{"id": row.ID}).Returning(goqu.Star()).Executor().ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("lease durable workflow: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return executionRecord(row)
}

func (p *Postgres) SaveWorkflowExecutionCheckpoint(ctx context.Context, id, worker string, revision int64, status string, checkpoint service.WorkflowCheckpoint, message string) (int64, error) {
	if !service.HasExecutionMaintenance(ctx) {
		return 0, service.ErrExecutionDenied
	}
	switch status {
	case "running", "waiting", "completed", "blocked":
	default:
		return 0, service.ErrWorkflowExecutionConflict
	}
	if status == "waiting" && (checkpoint.Waiting == nil || checkpoint.InFlight != "") {
		return 0, fmt.Errorf("invalid wait checkpoint")
	}
	encoded, err := json.Marshal(checkpoint)
	if err != nil || len(encoded) > 8<<20 {
		return 0, fmt.Errorf("workflow checkpoint must be JSON of at most 8 MiB")
	}
	if len(message) > 4096 {
		message = string([]rune(message)[:min(len([]rune(message)), 1000)])
	}
	values := goqu.Record{"status": status, "checkpoint": goqu.L("?::jsonb", string(encoded)), "revision": goqu.L("revision + 1"), "updated_at": goqu.L("clock_timestamp()"), "error": message, "resume_requested": false}
	if status != "running" {
		values["lease_owner"], values["lease_until"] = "", nil
	}
	if checkpoint.Waiting != nil {
		wait := checkpoint.Waiting
		values["wait_node_id"], values["wait_mode"], values["wait_prompt"], values["wake_at"], values["expires_at"] = wait.NodeID, wait.Mode, wait.Prompt, wait.WakeAt, wait.ExpiresAt
	} else {
		values["wait_node_id"], values["wait_mode"], values["wait_prompt"], values["wake_at"], values["expires_at"] = "", "", "", nil, nil
	}
	var next int64
	found, err := p.goqu.Update(p.executionTable("workflow_executions")).Set(values).Where(goqu.Ex{"id": id, "lease_owner": worker, "revision": revision, "status": "running"}, goqu.L("lease_until > clock_timestamp()")).Returning("revision").Executor().ScanValContext(ctx, &next)
	if err != nil {
		return 0, fmt.Errorf("save workflow checkpoint: %w", err)
	}
	if !found {
		return 0, service.ErrWorkflowExecutionConflict
	}
	return next, nil
}

func (p *Postgres) RenewWorkflowExecutionLease(ctx context.Context, id, worker string) error {
	if !service.HasExecutionMaintenance(ctx) {
		return service.ErrExecutionDenied
	}
	result, err := p.goqu.Update(p.executionTable("workflow_executions")).Set(goqu.Record{"lease_until": goqu.L("clock_timestamp() + interval '120 seconds'")}).Where(goqu.Ex{"id": id, "lease_owner": worker, "status": "running"}, goqu.L("lease_until > clock_timestamp()")).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("renew workflow lease: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrWorkflowExecutionConflict
	}
	return nil
}
