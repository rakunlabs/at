package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestCronRunHistoryPostgres(t *testing.T) {
	p := newTestStore(t, nil)
	admin, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "cron-admin", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	principal, _, err := p.ResolveWorkspaceAccess(t.Context(), service.DefaultWorkspaceID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := service.WithAccessPrincipal(t.Context(), principal)
	wf, err := p.CreateWorkflow(adminCtx, service.Workflow{Name: "cron", Graph: service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}}}})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := p.CreateTrigger(adminCtx, service.Trigger{Type: "cron", TargetType: service.TriggerTargetWorkflow, TargetID: wf.ID, WorkflowID: wf.ID, Enabled: true, Config: map[string]any{"schedule": "0 0 * * *"}})
	if err != nil {
		t.Fatal(err)
	}
	prov := service.ExecutionProvenance{RunID: "trigger/" + trigger.ID, UserID: principal.UserID, WorkspaceID: principal.WorkspaceID, Source: "trigger", MembershipVersion: principal.MembershipVersion}
	exec, err := service.BindExecution(adminCtx, prov, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: true, MembershipVersion: principal.MembershipVersion, Policy: service.ExecutionPolicy{WorkspaceID: principal.WorkspaceID, Mode: service.ExecutionRestricted}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.StartCronRun(adminCtx, service.CronRun{TriggerID: trigger.ID, Source: service.CronRunSourceManual}); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatalf("unbound start: %v", err)
	}
	if _, err := p.StartCronRun(exec, service.CronRun{TriggerID: "missing", Source: service.CronRunSourceManual}); !errors.Is(err, service.ErrAccessDenied) {
		t.Fatalf("foreign trigger: %v", err)
	}

	first, err := p.StartCronRun(exec, service.CronRun{TriggerID: trigger.ID, Source: service.CronRunSourceSchedule})
	if err != nil {
		t.Fatal(err)
	}
	run, err := p.StartCronRun(exec, service.CronRun{TriggerID: trigger.ID, Source: service.CronRunSourceManual, TriggeredBy: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != service.CronRunRunning || run.Source != service.CronRunSourceManual {
		t.Fatalf("started: %+v", run)
	}
	if err := p.LinkCronRun(exec, run.ID, "task-1", "ORG-1", ""); err != nil {
		t.Fatal(err)
	}
	if id, err := p.CronRunForTask(exec, "task-1"); err != nil || id != run.ID {
		t.Fatalf("run for task: %q %v", id, err)
	}
	if err := p.AppendCronRunLog(exec, run.ID, service.CronRunLogMilestone, "Topic selected"); err != nil {
		t.Fatal(err)
	}
	if err := p.AppendCronRunLog(exec, run.ID, "bogus", "x"); err == nil {
		t.Fatal("invalid level accepted")
	}
	if err := p.ReportCronRun(exec, run.ID, "partial", "uploaded, no thumbnail"); err != nil {
		t.Fatal(err)
	}
	if err := p.FinishCronRun(exec, run.ID, service.CronRunCompleted, "", "done"); err != nil {
		t.Fatal(err)
	}
	// A closed run is not reopened or rewritten.
	if err := p.FinishCronRun(exec, run.ID, service.CronRunFailed, "late", ""); err != nil {
		t.Fatal(err)
	}

	runs, err := p.ListCronRuns(adminCtx, service.CronRunQuery{TriggerID: trigger.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != run.ID || runs[1].ID != first.ID {
		t.Fatalf("list order: %+v", runs)
	}
	got := runs[0]
	if got.Status != service.CronRunCompleted || got.ReportedStatus != "partial" || got.TaskIdentifier != "ORG-1" || got.LogCount != 1 || got.FinishedAt == nil {
		t.Fatalf("run: %+v", got)
	}
	detail, err := p.GetCronRun(adminCtx, run.ID)
	if err != nil || detail == nil || len(detail.Logs) != 1 || detail.Logs[0].Message != "Topic selected" {
		t.Fatalf("detail: %+v %v", detail, err)
	}

	if err := p.DeleteTrigger(adminCtx, trigger.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListCronRuns(adminCtx, service.CronRunQuery{TriggerID: trigger.ID}); !errors.Is(err, service.ErrAccessResourceNotFound) {
		t.Fatalf("deleted trigger runs: %v", err)
	}
}
