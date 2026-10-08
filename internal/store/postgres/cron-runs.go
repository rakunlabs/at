package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.CronRunStorer = (*Postgres)(nil)

const (
	cronRunTextMax    = 4000
	cronRunMessageMax = 2000
	cronRunListMax    = 100
)

type cronRunRow struct {
	ID              string     `db:"id"`
	WorkspaceID     string     `db:"workspace_id"`
	TriggerID       string     `db:"trigger_id"`
	TargetType      string     `db:"target_type"`
	TargetID        string     `db:"target_id"`
	Source          string     `db:"source"`
	Status          string     `db:"status"`
	TaskID          string     `db:"task_id"`
	TaskIdentifier  string     `db:"task_identifier"`
	WorkflowRunID   string     `db:"workflow_run_id"`
	TriggeredBy     string     `db:"triggered_by"`
	Error           string     `db:"error"`
	Result          string     `db:"result"`
	ReportedStatus  string     `db:"reported_status"`
	ReportedSummary string     `db:"reported_summary"`
	StartedAt       time.Time  `db:"started_at"`
	FinishedAt      *time.Time `db:"finished_at"`
	LogCount        int        `db:"log_count"`
}

func (r cronRunRow) record() service.CronRun {
	return service.CronRun{
		ID: r.ID, WorkspaceID: r.WorkspaceID, TriggerID: r.TriggerID, TargetType: r.TargetType, TargetID: r.TargetID,
		Source: r.Source, Status: r.Status, TaskID: r.TaskID, TaskIdentifier: r.TaskIdentifier,
		WorkflowRunID: r.WorkflowRunID, TriggeredBy: r.TriggeredBy, Error: r.Error, Result: r.Result,
		ReportedStatus: r.ReportedStatus, ReportedSummary: r.ReportedSummary,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, LogCount: r.LogCount,
	}
}

// cleanCronText bounds a string on a rune boundary and drops invalid UTF-8,
// which postgres refuses.
func cleanCronText(s string, limit int) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// cronRunWorkspace is the workspace of the execution identity a write runs
// under. Run rows are only ever written from inside the run's execution.
func cronRunWorkspace(ctx context.Context) (string, error) {
	p, _, bound := service.ExecutionFromContext(ctx)
	if !bound || p.WorkspaceID == "" {
		return "", service.ErrExecutionDenied
	}
	return p.WorkspaceID, nil
}

func (p *Postgres) StartCronRun(ctx context.Context, run service.CronRun) (*service.CronRun, error) {
	ws, err := cronRunWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	if run.TriggerID == "" {
		return nil, fmt.Errorf("cron run requires a trigger")
	}
	if run.Source != service.CronRunSourceSchedule && run.Source != service.CronRunSourceManual {
		return nil, fmt.Errorf("invalid cron run source %q", run.Source)
	}
	// The trigger must belong to the run's workspace.
	owned := p.goqu.From(p.tableTriggers).Select(
		goqu.V(ulid.Make().String()), goqu.C("workspace_id"), goqu.C("id"), goqu.V(run.TargetType), goqu.V(run.TargetID),
		goqu.V(run.Source), goqu.V(service.CronRunRunning), goqu.V(cleanCronText(run.TriggeredBy, 200)),
	).Where(goqu.Ex{"id": run.TriggerID, "workspace_id": ws})
	id := ""
	query := p.goqu.Insert(p.workspaceTable("cron_runs")).
		Cols("id", "workspace_id", "trigger_id", "target_type", "target_id", "source", "status", "triggered_by").
		FromQuery(owned).Returning("id")
	found, err := query.Executor().ScanValContext(ctx, &id)
	if err != nil {
		return nil, fmt.Errorf("start cron run: %w", err)
	}
	if !found {
		return nil, service.ErrAccessDenied
	}
	table := p.workspaceTable("cron_runs")
	keep := p.goqu.From(table).Select("id").Where(goqu.Ex{"workspace_id": ws, "trigger_id": run.TriggerID}).
		Order(goqu.I("id").Desc()).Limit(service.CronRunRetention)
	if _, err := p.goqu.Delete(table).Where(goqu.Ex{"workspace_id": ws, "trigger_id": run.TriggerID}, goqu.C("id").NotIn(keep)).Executor().ExecContext(ctx); err != nil {
		return nil, fmt.Errorf("trim cron runs: %w", err)
	}
	return p.cronRunByID(ctx, ws, id)
}

func (p *Postgres) LinkCronRun(ctx context.Context, id, taskID, taskIdentifier, workflowRunID string) error {
	ws, err := cronRunWorkspace(ctx)
	if err != nil {
		return err
	}
	rec := goqu.Record{}
	if taskID != "" {
		rec["task_id"] = taskID
		rec["task_identifier"] = cleanCronText(taskIdentifier, 200)
	}
	if workflowRunID != "" {
		rec["workflow_run_id"] = workflowRunID
	}
	if len(rec) == 0 {
		return nil
	}
	if _, err := p.goqu.Update(p.workspaceTable("cron_runs")).Set(rec).
		Where(goqu.Ex{"id": id, "workspace_id": ws}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("link cron run: %w", err)
	}
	return nil
}

// FinishCronRun closes a running row; a row already closed is left as it is.
func (p *Postgres) FinishCronRun(ctx context.Context, id, status, errMsg, result string) error {
	ws, err := cronRunWorkspace(ctx)
	if err != nil {
		return err
	}
	switch status {
	case service.CronRunCompleted, service.CronRunFailed, service.CronRunBlocked, service.CronRunCancelled:
	default:
		return fmt.Errorf("invalid terminal cron run status %q", status)
	}
	if _, err := p.goqu.Update(p.workspaceTable("cron_runs")).Set(goqu.Record{
		"status":      status,
		"error":       cleanCronText(errMsg, cronRunTextMax),
		"result":      cleanCronText(result, cronRunTextMax),
		"finished_at": goqu.L("clock_timestamp()"),
	}).Where(goqu.Ex{"id": id, "workspace_id": ws, "status": service.CronRunRunning}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("finish cron run: %w", err)
	}
	return nil
}

func (p *Postgres) ReportCronRun(ctx context.Context, id, status, summary string) error {
	ws, err := cronRunWorkspace(ctx)
	if err != nil {
		return err
	}
	if !service.ValidCronRunReport(status) {
		return fmt.Errorf("invalid reported status %q", status)
	}
	if _, err := p.goqu.Update(p.workspaceTable("cron_runs")).Set(goqu.Record{
		"reported_status":  status,
		"reported_summary": cleanCronText(summary, cronRunTextMax),
	}).Where(goqu.Ex{"id": id, "workspace_id": ws}).Executor().ExecContext(ctx); err != nil {
		return fmt.Errorf("report cron run: %w", err)
	}
	return nil
}

func (p *Postgres) AppendCronRunLog(ctx context.Context, runID, level, message string) error {
	ws, err := cronRunWorkspace(ctx)
	if err != nil {
		return err
	}
	switch level {
	case service.CronRunLogSystem, service.CronRunLogMilestone, service.CronRunLogReport, service.CronRunLogError:
	default:
		return fmt.Errorf("invalid cron run log level %q", level)
	}
	message = cleanCronText(message, cronRunMessageMax)
	if message == "" {
		return nil
	}
	count, err := p.goqu.From(p.workspaceTable("cron_run_logs")).Where(goqu.Ex{"run_id": runID, "workspace_id": ws}).CountContext(ctx)
	if err != nil {
		return fmt.Errorf("count cron run logs: %w", err)
	}
	if count >= service.CronRunLogMax {
		return service.ErrCronRunLogFull
	}
	owned := p.goqu.From(p.workspaceTable("cron_runs")).Select(
		goqu.V(ulid.Make().String()), goqu.C("workspace_id"), goqu.C("id"), goqu.V(level), goqu.V(message),
	).Where(goqu.Ex{"id": runID, "workspace_id": ws})
	res, err := p.goqu.Insert(p.workspaceTable("cron_run_logs")).
		Cols("id", "workspace_id", "run_id", "level", "message").FromQuery(owned).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("append cron run log: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return service.ErrAccessResourceNotFound
	}
	return nil
}

// CronRunForTask returns the run that launched rootTaskID, or "".
func (p *Postgres) CronRunForTask(ctx context.Context, rootTaskID string) (string, error) {
	ws, err := cronRunWorkspace(ctx)
	if err != nil {
		return "", err
	}
	if rootTaskID == "" {
		return "", nil
	}
	var id string
	if _, err := p.goqu.From(p.workspaceTable("cron_runs")).Select("id").
		Where(goqu.Ex{"task_id": rootTaskID, "workspace_id": ws}).
		Order(goqu.I("id").Desc()).Limit(1).ScanValContext(ctx, &id); err != nil {
		return "", fmt.Errorf("find cron run for task: %w", err)
	}
	return id, nil
}

func (p *Postgres) cronRunSelect() *goqu.SelectDataset {
	logs := p.goqu.From(p.workspaceTable("cron_run_logs").As("l")).
		Select(goqu.COUNT("*")).Where(goqu.I("l.run_id").Eq(goqu.I("r.id")))
	return p.goqu.From(p.workspaceTable("cron_runs").As("r")).Select(
		goqu.I("r.id"), goqu.I("r.workspace_id"), goqu.I("r.trigger_id"), goqu.I("r.target_type"), goqu.I("r.target_id"),
		goqu.I("r.source"), goqu.I("r.status"), goqu.I("r.task_id"), goqu.I("r.task_identifier"), goqu.I("r.workflow_run_id"),
		goqu.I("r.triggered_by"), goqu.I("r.error"), goqu.I("r.result"), goqu.I("r.reported_status"), goqu.I("r.reported_summary"),
		goqu.I("r.started_at"), goqu.I("r.finished_at"), logs.As("log_count"),
	)
}

func (p *Postgres) cronRunByID(ctx context.Context, workspaceID, id string) (*service.CronRun, error) {
	var row cronRunRow
	found, err := p.cronRunSelect().Where(goqu.I("r.id").Eq(id), goqu.I("r.workspace_id").Eq(workspaceID)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("get cron run: %w", err)
	}
	if !found {
		return nil, nil
	}
	rec := row.record()
	return &rec, nil
}

// ListCronRuns returns a trigger's latest runs to a caller that can read the
// trigger.
func (p *Postgres) ListCronRuns(ctx context.Context, q service.CronRunQuery) ([]service.CronRun, error) {
	t, err := p.GetTrigger(ctx, q.TriggerID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, service.ErrAccessResourceNotFound
	}
	limit := q.Limit
	if limit <= 0 || limit > cronRunListMax {
		limit = 50
	}
	ds := p.cronRunSelect().Where(goqu.I("r.workspace_id").Eq(t.WorkspaceID), goqu.I("r.trigger_id").Eq(t.ID))
	if q.Before != "" {
		ds = ds.Where(goqu.I("r.id").Lt(q.Before))
	}
	var rows []cronRunRow
	if err := ds.Order(goqu.I("r.id").Desc()).Limit(uint(limit)).ScanStructsContext(ctx, &rows); err != nil {
		return nil, fmt.Errorf("list cron runs: %w", err)
	}
	out := make([]service.CronRun, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.record())
	}
	return out, nil
}

// GetCronRun returns one run with its log to a caller that can read its
// trigger.
func (p *Postgres) GetCronRun(ctx context.Context, id string) (*service.CronRun, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	run, err := p.cronRunByID(ctx, a.WorkspaceID, id)
	if err != nil || run == nil {
		return nil, err
	}
	t, err := p.GetTrigger(ctx, run.TriggerID)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, nil
	}
	var logs []struct {
		ID        string    `db:"id"`
		RunID     string    `db:"run_id"`
		Level     string    `db:"level"`
		Message   string    `db:"message"`
		CreatedAt time.Time `db:"created_at"`
	}
	if err := p.goqu.From(p.workspaceTable("cron_run_logs")).Select("id", "run_id", "level", "message", "created_at").
		Where(goqu.Ex{"run_id": run.ID, "workspace_id": run.WorkspaceID}).Order(goqu.I("id").Asc()).
		ScanStructsContext(ctx, &logs); err != nil {
		return nil, fmt.Errorf("list cron run logs: %w", err)
	}
	run.Logs = make([]service.CronRunLog, 0, len(logs))
	for _, l := range logs {
		run.Logs = append(run.Logs, service.CronRunLog{ID: l.ID, RunID: l.RunID, Level: l.Level, Message: l.Message, CreatedAt: l.CreatedAt})
	}
	return run, nil
}
