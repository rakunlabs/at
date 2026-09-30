package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestWorkflowRunHistoryPostgres(t *testing.T) {
	p := newTestStore(t, nil)
	admin, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "runs-admin", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	adminPrincipal, _, err := p.ResolveWorkspaceAccess(t.Context(), service.DefaultWorkspaceID, admin.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	adminCtx := service.WithAccessPrincipal(t.Context(), adminPrincipal)
	wf, err := p.CreateWorkflow(adminCtx, service.Workflow{Name: "history", Graph: service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}}}})
	if err != nil {
		t.Fatal(err)
	}
	memberUser, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "runs-member"}, false)
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := workspaceMember(t, p, adminCtx, adminPrincipal.WorkspaceID, memberUser, "member")
	memberPrincipal, _ := service.AccessPrincipalFromContext(memberCtx)

	bind := func(principal service.AccessPrincipal, runID string) context.Context {
		t.Helper()
		prov := service.ExecutionProvenance{RunID: runID, UserID: principal.UserID, WorkspaceID: principal.WorkspaceID, Source: "trigger", MembershipVersion: principal.MembershipVersion}
		ctx, err := service.BindExecution(service.WithAccessPrincipal(t.Context(), principal), prov, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
			return service.ExecutionValidation{Allowed: true, MembershipVersion: principal.MembershipVersion, Policy: service.ExecutionPolicy{WorkspaceID: principal.WorkspaceID, Mode: service.ExecutionRestricted}}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}
	start := func(ctx context.Context, principal service.AccessPrincipal, id, source string) {
		t.Helper()
		if err := p.StartWorkflowRun(ctx, service.WorkflowRun{ID: id, WorkspaceID: principal.WorkspaceID, WorkflowID: wf.ID, OwnerUserID: principal.UserID, Source: source, TriggerID: "trg"}); err != nil {
			t.Fatal(err)
		}
	}

	// Trigger runs share one execution provenance; each still gets its own row.
	adminExec := bind(adminPrincipal, "trigger/trg")
	start(adminExec, adminPrincipal, "run_ok", "cron")
	start(adminExec, adminPrincipal, "run_bad", "cron")
	start(adminExec, adminPrincipal, "run_live", "webhook")
	memberExec := bind(memberPrincipal, "run_member")
	start(memberExec, memberPrincipal, "run_member", "api")

	if err := p.FinishWorkflowRun(adminExec, "run_ok", service.WorkflowRunFinish{
		Status:        service.WorkflowRunCompleted,
		HandledErrors: []service.WorkflowRunHandledError{{NodeID: "mail", NodeType: "email", Error: "smtp down"}},
	}); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("ş", 5000) // multi-byte, over the 4 KiB bound
	if err := p.FinishWorkflowRun(adminExec, "run_bad", service.WorkflowRunFinish{Status: service.WorkflowRunFailed, Error: long, FailedNodeID: "http", FailedNodeType: "http_request"}); err != nil {
		t.Fatal(err)
	}
	if err := p.FinishWorkflowRun(memberExec, "run_member", service.WorkflowRunFinish{Status: service.WorkflowRunCancelled, Error: "context canceled"}); err != nil {
		t.Fatal(err)
	}

	t.Run("administrator sees every run, newest first", func(t *testing.T) {
		runs, err := p.ListWorkflowRuns(adminCtx, service.WorkflowRunQuery{WorkflowID: wf.ID})
		if err != nil {
			t.Fatal(err)
		}
		byID := map[string]service.WorkflowRun{}
		for _, r := range runs {
			byID[r.ID] = r
		}
		if len(runs) != 4 {
			t.Fatalf("runs = %d, want 4", len(runs))
		}
		if byID["run_live"].Status != service.WorkflowRunRunning || byID["run_live"].FinishedAt != nil {
			t.Fatalf("live run = %+v", byID["run_live"])
		}
		ok := byID["run_ok"]
		if ok.Status != service.WorkflowRunCompleted || len(ok.HandledErrors) != 1 || ok.HandledErrors[0].NodeID != "mail" || ok.FinishedAt == nil {
			t.Fatalf("completed run = %+v", ok)
		}
		bad := byID["run_bad"]
		if bad.FailedNodeID != "http" || bad.FailedNodeType != "http_request" || len(bad.Error) > 4100 || !strings.HasSuffix(bad.Error, "…") {
			t.Fatalf("failed run = node %q/%q, error %d bytes", bad.FailedNodeID, bad.FailedNodeType, len(bad.Error))
		}
		if !strings.HasPrefix(bad.Error, "ş") || strings.ContainsRune(bad.Error, '\uFFFD') {
			t.Fatal("error truncated inside a rune")
		}
	})

	t.Run("status filter; failed includes interrupted", func(t *testing.T) {
		runs, err := p.ListWorkflowRuns(adminCtx, service.WorkflowRunQuery{WorkflowID: wf.ID, Status: "failed"})
		if err != nil || len(runs) != 1 || runs[0].ID != "run_bad" {
			t.Fatalf("failed filter = %+v %v", runs, err)
		}
		if _, err := p.ListWorkflowRuns(adminCtx, service.WorkflowRunQuery{WorkflowID: wf.ID, Status: "exploded"}); !errors.Is(err, service.ErrInvalidWorkflowRunQuery) {
			t.Fatalf("unknown status error = %v", err)
		}
	})

	t.Run("member sees only own runs", func(t *testing.T) {
		runs, err := p.ListWorkflowRuns(memberCtx, service.WorkflowRunQuery{WorkflowID: wf.ID})
		if err != nil || len(runs) != 1 || runs[0].ID != "run_member" || runs[0].Status != service.WorkflowRunCancelled {
			t.Fatalf("member runs = %+v %v", runs, err)
		}
	})

	t.Run("identity and ownership are enforced on write", func(t *testing.T) {
		// Another user cannot be recorded as the owner.
		err := p.StartWorkflowRun(memberExec, service.WorkflowRun{ID: "forged", WorkspaceID: memberPrincipal.WorkspaceID, WorkflowID: wf.ID, OwnerUserID: admin.ID, Source: "api"})
		if !errors.Is(err, service.ErrExecutionDenied) {
			t.Fatalf("forged owner = %v", err)
		}
		// A workflow outside the workspace cannot be recorded.
		err = p.StartWorkflowRun(adminExec, service.WorkflowRun{ID: "foreign", WorkspaceID: adminPrincipal.WorkspaceID, WorkflowID: "no-such-workflow", OwnerUserID: admin.ID, Source: "api"})
		if !errors.Is(err, service.ErrAccessDenied) {
			t.Fatalf("foreign workflow = %v", err)
		}
		// Unbound callers write nothing.
		if err := p.StartWorkflowRun(adminCtx, service.WorkflowRun{ID: "x", WorkspaceID: adminPrincipal.WorkspaceID, WorkflowID: wf.ID, OwnerUserID: admin.ID, Source: "api"}); !errors.Is(err, service.ErrExecutionDenied) {
			t.Fatalf("unbound start = %v", err)
		}
		// A closed run is not reopened or rewritten.
		if err := p.FinishWorkflowRun(adminExec, "run_ok", service.WorkflowRunFinish{Status: service.WorkflowRunFailed, Error: "rewrite"}); err != nil {
			t.Fatal(err)
		}
		runs, _ := p.ListWorkflowRuns(adminCtx, service.WorkflowRunQuery{WorkflowID: wf.ID, Status: "completed"})
		if len(runs) != 1 || runs[0].Error != "" {
			t.Fatalf("closed run rewritten: %+v", runs)
		}
	})

	t.Run("sweep interrupts abandoned runs and applies retention", func(t *testing.T) {
		maintenance := service.WithExecutionMaintenance(t.Context())
		if _, _, err := p.SweepWorkflowRuns(adminCtx, time.Minute, time.Hour, time.Hour); !errors.Is(err, service.ErrExecutionDenied) {
			t.Fatalf("sweep without maintenance = %v", err)
		}
		// A heartbeat keeps a live run alive.
		if err := p.HeartbeatWorkflowRuns(maintenance, []string{"run_live"}); err != nil {
			t.Fatal(err)
		}
		if interrupted, _, err := p.SweepWorkflowRuns(maintenance, time.Minute, time.Hour, time.Hour); err != nil || interrupted != 0 {
			t.Fatalf("fresh run interrupted: %d %v", interrupted, err)
		}
		// Its process stops heartbeating.
		table := p.executionTable("workflow_runs")
		if _, err := p.goqu.Update(table).Set(goqu.Record{"heartbeat_at": goqu.L("clock_timestamp() - interval '10 minutes'")}).Where(goqu.Ex{"id": "run_live"}).Executor().ExecContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		// Age the completed run past its retention, keep the failed one inside.
		if _, err := p.goqu.Update(table).Set(goqu.Record{"finished_at": goqu.L("clock_timestamp() - interval '2 hours'")}).Where(goqu.Ex{"id": "run_ok"}).Executor().ExecContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		interrupted, deleted, err := p.SweepWorkflowRuns(maintenance, time.Minute, time.Hour, 24*time.Hour)
		if err != nil || interrupted != 1 || deleted != 1 {
			t.Fatalf("sweep = interrupted %d, deleted %d, %v", interrupted, deleted, err)
		}
		runs, err := p.ListWorkflowRuns(adminCtx, service.WorkflowRunQuery{WorkflowID: wf.ID, Status: "failed"})
		if err != nil || len(runs) != 2 {
			t.Fatalf("failed after sweep = %+v %v", runs, err)
		}
		for _, r := range runs {
			if r.ID == "run_live" && (r.Status != service.WorkflowRunInterrupted || r.FinishedAt == nil || r.Error == "") {
				t.Fatalf("abandoned run = %+v", r)
			}
		}
		// The interrupted run's late finish must not overwrite the sweep.
		if err := p.FinishWorkflowRun(adminExec, "run_live", service.WorkflowRunFinish{Status: service.WorkflowRunCompleted}); err != nil {
			t.Fatal(err)
		}
		runs, _ = p.ListWorkflowRuns(adminCtx, service.WorkflowRunQuery{WorkflowID: wf.ID, Status: "interrupted"})
		if len(runs) != 1 {
			t.Fatalf("interrupted run reopened: %+v", runs)
		}
	})
}
