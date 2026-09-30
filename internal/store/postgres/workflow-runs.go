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

var _ service.WorkflowRunStorer = (*Postgres)(nil)

const (
	workflowRunErrorMaxBytes    = 4096
	workflowRunHandledMaxErrors = 20
	workflowRunListMaxLimit     = 200
)

type workflowRunRow struct {
	ID             string     `db:"id"`
	WorkspaceID    string     `db:"workspace_id"`
	WorkflowID     string     `db:"workflow_id"`
	OwnerUserID    string     `db:"owner_user_id"`
	Source         string     `db:"source"`
	TriggerID      string     `db:"trigger_id"`
	Status         string     `db:"status"`
	Error          string     `db:"error"`
	FailedNodeID   string     `db:"failed_node_id"`
	FailedNodeType string     `db:"failed_node_type"`
	HandledErrors  []byte     `db:"handled_errors"`
	StartedAt      time.Time  `db:"started_at"`
	HeartbeatAt    time.Time  `db:"heartbeat_at"`
	FinishedAt     *time.Time `db:"finished_at"`
}

func workflowRunRecord(row workflowRunRow) (service.WorkflowRun, error) {
	run := service.WorkflowRun{
		ID: row.ID, WorkspaceID: row.WorkspaceID, WorkflowID: row.WorkflowID, OwnerUserID: row.OwnerUserID,
		Source: row.Source, TriggerID: row.TriggerID, Status: row.Status, Error: row.Error,
		FailedNodeID: row.FailedNodeID, FailedNodeType: row.FailedNodeType,
		StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
		HandledErrors: []service.WorkflowRunHandledError{},
	}
	if len(row.HandledErrors) > 0 {
		if err := json.Unmarshal(row.HandledErrors, &run.HandledErrors); err != nil {
			return run, fmt.Errorf("decode workflow run handled errors: %w", err)
		}
	}
	return run, nil
}

// truncateUTF8 bounds an error string without cutting a rune in half.
func truncateUTF8(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && cut < len(s) && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// StartWorkflowRun records the run under the execution identity it is bound
// to: the row can only describe a run of the caller's own workspace and user,
// for a workflow that belongs to that workspace. The run ID is minted by the
// server, not taken from the execution provenance: trigger runs share one
// provenance (and exec sandbox) per trigger, while history needs one row per run.
func (p *Postgres) StartWorkflowRun(ctx context.Context, run service.WorkflowRun) error {
	provenance, _, bound := service.ExecutionFromContext(ctx)
	if !bound || run.ID == "" || provenance.WorkspaceID != run.WorkspaceID || provenance.UserID != run.OwnerUserID {
		return service.ErrExecutionDenied
	}
	switch run.Source {
	case "api", "cron", "webhook", "tool":
	default:
		return fmt.Errorf("invalid workflow run source %q", run.Source)
	}
	owned := p.goqu.From(p.tableWorkflows).Select(
		goqu.V(run.ID), goqu.C("workspace_id"), goqu.C("id"), goqu.V(run.OwnerUserID),
		goqu.V(run.Source), goqu.V(run.TriggerID), goqu.V(service.WorkflowRunRunning),
	).Where(goqu.Ex{"id": run.WorkflowID, "workspace_id": run.WorkspaceID})
	res, err := p.goqu.Insert(p.executionTable("workflow_runs")).
		Cols("id", "workspace_id", "workflow_id", "owner_user_id", "source", "trigger_id", "status").
		FromQuery(owned).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("start workflow run: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrAccessDenied
	}
	return nil
}

// FinishWorkflowRun closes a running row of the caller's workspace. A row
// already closed (for example marked interrupted by a sweep) is left as it is.
func (p *Postgres) FinishWorkflowRun(ctx context.Context, id string, finish service.WorkflowRunFinish) error {
	provenance, _, bound := service.ExecutionFromContext(ctx)
	if !bound || id == "" {
		return service.ErrExecutionDenied
	}
	switch finish.Status {
	case service.WorkflowRunCompleted, service.WorkflowRunFailed, service.WorkflowRunCancelled:
	default:
		return fmt.Errorf("invalid terminal workflow run status %q", finish.Status)
	}
	handled := finish.HandledErrors
	if len(handled) > workflowRunHandledMaxErrors {
		handled = handled[:workflowRunHandledMaxErrors]
	}
	for i := range handled {
		handled[i].Error = truncateUTF8(handled[i].Error, workflowRunErrorMaxBytes)
	}
	if handled == nil {
		handled = []service.WorkflowRunHandledError{}
	}
	encoded, err := json.Marshal(handled)
	if err != nil {
		return fmt.Errorf("encode handled errors: %w", err)
	}
	_, err = p.goqu.Update(p.executionTable("workflow_runs")).Set(goqu.Record{
		"status":           finish.Status,
		"error":            truncateUTF8(finish.Error, workflowRunErrorMaxBytes),
		"failed_node_id":   finish.FailedNodeID,
		"failed_node_type": finish.FailedNodeType,
		"handled_errors":   goqu.L("?::jsonb", string(encoded)),
		"finished_at":      goqu.L("clock_timestamp()"),
	}).Where(goqu.Ex{"id": id, "workspace_id": provenance.WorkspaceID, "status": service.WorkflowRunRunning}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("finish workflow run: %w", err)
	}
	return nil
}

// HeartbeatWorkflowRuns refreshes the live runs of this process.
func (p *Postgres) HeartbeatWorkflowRuns(ctx context.Context, ids []string) error {
	if !service.HasExecutionMaintenance(ctx) {
		return service.ErrExecutionDenied
	}
	if len(ids) == 0 {
		return nil
	}
	_, err := p.goqu.Update(p.executionTable("workflow_runs")).
		Set(goqu.Record{"heartbeat_at": goqu.L("clock_timestamp()")}).
		Where(goqu.C("id").In(ids), goqu.C("status").Eq(service.WorkflowRunRunning)).
		Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("heartbeat workflow runs: %w", err)
	}
	return nil
}

// ListWorkflowRuns follows the saved-run visibility rule: workflow readers see
// their own runs, workspace administrators and platform administrators see
// every run of the workflow.
func (p *Postgres) ListWorkflowRuns(ctx context.Context, q service.WorkflowRunQuery) ([]service.WorkflowRun, error) {
	scope, err := p.workflowRunScope(ctx, q.WorkflowID)
	if err != nil {
		return nil, err
	}
	where := []exp.Expression{scope}
	switch q.Status {
	case "":
	case service.WorkflowRunFailed:
		where = append(where, goqu.C("status").In(service.WorkflowRunFailed, service.WorkflowRunInterrupted))
	case service.WorkflowRunRunning, service.WorkflowRunCompleted, service.WorkflowRunCancelled, service.WorkflowRunInterrupted:
		where = append(where, goqu.C("status").Eq(q.Status))
	default:
		return nil, fmt.Errorf("%w: unknown status %q", service.ErrInvalidWorkflowRunQuery, q.Status)
	}
	limit := q.Limit
	if limit <= 0 || limit > workflowRunListMaxLimit {
		limit = 50
	}
	var rows []workflowRunRow
	err = p.goqu.From(p.executionTable("workflow_runs")).Where(where...).
		Order(goqu.C("started_at").Desc(), goqu.C("id").Desc()).Limit(uint(limit)).
		ScanStructsContext(ctx, &rows)
	if err != nil {
		return nil, fmt.Errorf("list workflow runs: %w", err)
	}
	runs := make([]service.WorkflowRun, 0, len(rows))
	for _, row := range rows {
		run, err := workflowRunRecord(row)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func (p *Postgres) workflowRunScope(ctx context.Context, workflowID string) (exp.Expression, error) {
	wf, err := p.GetWorkflow(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	if wf == nil || a.WorkspaceID != wf.WorkspaceID || !a.Allows("workflows.read", service.AccessResource{Kind: "workflows", ID: workflowID, WorkspaceID: wf.WorkspaceID}) {
		return nil, service.ErrAccessDenied
	}
	scope := goqu.And(goqu.C("workflow_id").Eq(workflowID), goqu.C("workspace_id").Eq(a.WorkspaceID))
	if !a.PlatformAdmin && service.WorkspaceRoleRank(a.Role) < 3 {
		scope = goqu.And(scope, goqu.C("owner_user_id").Eq(a.UserID))
	}
	return scope, nil
}

// SweepWorkflowRuns closes abandoned rows and applies retention, in bounded
// batches so a large backlog never becomes one long transaction.
func (p *Postgres) SweepWorkflowRuns(ctx context.Context, staleAfter, keepCompleted, keepFailed time.Duration) (int64, int64, error) {
	if !service.HasExecutionMaintenance(ctx) {
		return 0, 0, service.ErrExecutionDenied
	}
	table := p.executionTable("workflow_runs")
	seconds := func(d time.Duration) float64 { return d.Seconds() }

	stale := p.goqu.From(table).Select("id").Where(
		goqu.C("status").Eq(service.WorkflowRunRunning),
		goqu.L("heartbeat_at < clock_timestamp() - make_interval(secs => ?)", seconds(staleAfter)),
	).Limit(500)
	res, err := p.goqu.Update(table).Set(goqu.Record{
		"status":      service.WorkflowRunInterrupted,
		"error":       "The server running this workflow stopped before it finished (restart or crash). Steps after the last completed one did not run.",
		"finished_at": goqu.L("clock_timestamp()"),
	}).Where(goqu.C("id").In(stale), goqu.C("status").Eq(service.WorkflowRunRunning)).Executor().ExecContext(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("interrupt stale workflow runs: %w", err)
	}
	interrupted, _ := res.RowsAffected()

	expired := p.goqu.From(table).Select("id").Where(goqu.Or(
		goqu.And(goqu.C("status").Eq(service.WorkflowRunCompleted), goqu.L("finished_at < clock_timestamp() - make_interval(secs => ?)", seconds(keepCompleted))),
		goqu.And(goqu.C("status").In(service.WorkflowRunFailed, service.WorkflowRunCancelled, service.WorkflowRunInterrupted), goqu.L("finished_at < clock_timestamp() - make_interval(secs => ?)", seconds(keepFailed))),
	)).Limit(5000)
	res, err = p.goqu.Delete(table).Where(goqu.C("id").In(expired)).Executor().ExecContext(ctx)
	if err != nil {
		return interrupted, 0, fmt.Errorf("expire workflow runs: %w", err)
	}
	deleted, _ := res.RowsAffected()
	return interrupted, deleted, nil
}
