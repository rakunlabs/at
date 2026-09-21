package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestWorkflowExecutionPostgresClaimsDecisionsAndRecovery(t *testing.T) {
	p := newTestStore(t, nil)
	user, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "durable-admin", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	principal, _, err := p.ResolveWorkspaceAccess(t.Context(), service.DefaultWorkspaceID, user.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	actor := service.WithAccessPrincipal(t.Context(), principal)
	wf, err := p.CreateWorkflow(actor, service.Workflow{Name: "durable", Graph: service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}}}})
	if err != nil {
		t.Fatal(err)
	}
	maintenance := service.WithExecutionMaintenance(t.Context())
	create := func(id string) *service.WorkflowExecution {
		t.Helper()
		prov := service.ExecutionProvenance{RunID: id, UserID: user.ID, WorkspaceID: principal.WorkspaceID, Source: "durable_workflow", MembershipVersion: principal.MembershipVersion}
		ctx, err := service.BindExecution(actor, prov, t.TempDir(), func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
			return service.ExecutionValidation{Allowed: true, MembershipVersion: principal.MembershipVersion, Policy: service.ExecutionPolicy{WorkspaceID: principal.WorkspaceID, Mode: service.ExecutionRestricted}}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		value, err := p.CreateWorkflowExecution(ctx, service.WorkflowExecution{ID: id, WorkspaceID: principal.WorkspaceID, WorkflowID: wf.ID, OwnerUserID: user.ID, Payload: service.WorkflowExecutionPayload{Source: "api", Graph: wf.Graph}})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	create("one")
	if unavailable, err := p.ClaimWorkflowExecution(maintenance, "disabled-api", "cron"); err != nil || unavailable != nil {
		t.Fatalf("disabled source was claimed: %+v %v", unavailable, err)
	}
	var won atomic.Int32
	var claimed *service.WorkflowExecution
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			value, err := p.ClaimWorkflowExecution(maintenance, fmt.Sprintf("worker-%d", index))
			if err != nil {
				t.Error(err)
				return
			}
			if value != nil {
				won.Add(1)
				mu.Lock()
				claimed = value
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if won.Load() != 1 || claimed == nil {
		t.Fatalf("double claim: %d", won.Load())
	}
	expires := time.Now().Add(time.Hour)
	cp := service.WorkflowCheckpoint{Version: 1, Nodes: map[string]service.WorkflowNodeCheckpoint{}, Waiting: &service.WorkflowWaitState{NodeID: "w", Mode: "approval", ExpiresAt: &expires, Data: map[string]any{"data": "frozen"}}}
	revision, err := p.SaveWorkflowExecutionCheckpoint(maintenance, claimed.ID, claimed.LeaseOwner, claimed.Revision, "waiting", cp, "")
	if err != nil {
		t.Fatal(err)
	}
	var approvals atomic.Int32
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := p.DecideWorkflowExecution(actor, wf.ID, claimed.ID, revision, "approve")
			if err == nil {
				approvals.Add(1)
			} else if !errors.Is(err, service.ErrWorkflowExecutionConflict) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if approvals.Load() != 1 {
		t.Fatalf("accepted %d approvals", approvals.Load())
	}
	restarted, err := p.ClaimWorkflowExecution(maintenance, "new-process")
	if err != nil || restarted == nil || !restarted.ResumeRequested || restarted.Checkpoint.Waiting.Data["data"] != "frozen" {
		t.Fatalf("resume did not preserve state: %+v %v", restarted, err)
	}
	if err := p.DecideWorkflowExecution(actor, wf.ID, claimed.ID, revision, "approve"); !errors.Is(err, service.ErrWorkflowExecutionConflict) {
		t.Fatal("old approval reused")
	}
	if _, err := p.SaveWorkflowExecutionCheckpoint(maintenance, claimed.ID, claimed.LeaseOwner, claimed.Revision, "completed", cp, ""); !errors.Is(err, service.ErrWorkflowExecutionConflict) {
		t.Fatal("stale worker wrote a checkpoint")
	}
	if err := p.DecideWorkflowExecution(actor, wf.ID, restarted.ID, restarted.Revision, "cancel"); err != nil {
		t.Fatal(err)
	}
	if err := p.RenewWorkflowExecutionLease(maintenance, restarted.ID, "new-process"); !errors.Is(err, service.ErrWorkflowExecutionConflict) {
		t.Fatal("cancel did not fence worker")
	}
	create("crashed")
	crashed, err := p.ClaimWorkflowExecution(maintenance, "dead-worker")
	if err != nil || crashed == nil {
		t.Fatal(err)
	}
	if _, err := p.SaveWorkflowExecutionCheckpoint(maintenance, crashed.ID, crashed.LeaseOwner, crashed.Revision, "running", service.WorkflowCheckpoint{Version: 1, InFlight: "external-step"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.executionTable("workflow_executions")).Set(goqu.Record{"lease_until": goqu.L("clock_timestamp() - interval '1 second'")}).Where(goqu.Ex{"id": crashed.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if next, err := p.ClaimWorkflowExecution(maintenance, "replacement"); err != nil || next != nil {
		t.Fatalf("expired worker was replayed: %+v %v", next, err)
	}
	blocked, err := p.GetWorkflowExecution(actor, wf.ID, crashed.ID)
	if err != nil || blocked.Status != "blocked" {
		t.Fatalf("missing blocked status: %+v %v", blocked, err)
	}
	create("safe-checkpoint")
	safe, err := p.ClaimWorkflowExecution(maintenance, "interrupted-before-step")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.executionTable("workflow_executions")).Set(goqu.Record{"lease_until": goqu.L("clock_timestamp() - interval '1 second'")}).Where(goqu.Ex{"id": safe.ID}).Executor().ExecContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	recovered, err := p.ClaimWorkflowExecution(maintenance, "safe-replacement")
	if err != nil || recovered == nil || recovered.ID != safe.ID {
		t.Fatalf("safe checkpoint did not recover: %+v %v", recovered, err)
	}
	if err := p.DecideWorkflowExecution(actor, wf.ID, recovered.ID, recovered.Revision, "cancel"); err != nil {
		t.Fatal(err)
	}
	create("timed")
	timed, err := p.ClaimWorkflowExecution(maintenance, "timer")
	if err != nil {
		t.Fatal(err)
	}
	var due time.Time
	if _, err := p.goqu.Select(goqu.L("clock_timestamp() - interval '1 second'")).ScanValContext(t.Context(), &due); err != nil {
		t.Fatal(err)
	}
	cp.Waiting.Mode, cp.Waiting.WakeAt, cp.Waiting.ExpiresAt = "duration", &due, nil
	if _, err := p.SaveWorkflowExecutionCheckpoint(maintenance, timed.ID, timed.LeaseOwner, timed.Revision, "waiting", cp, ""); err != nil {
		t.Fatal(err)
	}
	ready, err := p.ClaimWorkflowExecution(maintenance, "timer-resume")
	if err != nil || ready == nil || ready.ID != timed.ID || !ready.ResumeRequested {
		t.Fatalf("timer did not enqueue: %+v %v", ready, err)
	}
	if _, err := p.ClaimWorkflowExecution(t.Context(), "untrusted"); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("worker API admitted without maintenance identity")
	}
	if err := p.DecideWorkflowExecution(actor, wf.ID, ready.ID, ready.Revision, "cancel"); err != nil {
		t.Fatal(err)
	}
	create("expired")
	expiring, err := p.ClaimWorkflowExecution(maintenance, "expiry")
	if err != nil {
		t.Fatal(err)
	}
	cp.Waiting.Mode, cp.Waiting.WakeAt, cp.Waiting.ExpiresAt = "approval", nil, &due
	if _, err := p.SaveWorkflowExecutionCheckpoint(maintenance, expiring.ID, expiring.LeaseOwner, expiring.Revision, "waiting", cp, ""); err != nil {
		t.Fatal(err)
	}
	if next, err := p.ClaimWorkflowExecution(maintenance, "after-expiry"); err != nil || next != nil {
		t.Fatalf("expired approval resumed: %+v %v", next, err)
	}
	expired, err := p.GetWorkflowExecution(actor, wf.ID, expiring.ID)
	if err != nil || expired.Status != "expired" {
		t.Fatalf("approval not expired: %+v %v", expired, err)
	}
	private := create("private")
	member, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "durable-member"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SetWorkspaceMember(actor, service.WorkspaceMembership{WorkspaceID: principal.WorkspaceID, UserID: member.ID, Role: "member", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	reader, _, err := p.ResolveWorkspaceAccess(t.Context(), principal.WorkspaceID, member.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	// Even forged/stale role metadata cannot widen store-side ownership scope.
	reader.PlatformAdmin, reader.Role = true, "owner"
	readerCtx := service.WithAccessPrincipal(t.Context(), reader)
	if hidden, err := p.GetWorkflowExecution(readerCtx, wf.ID, private.ID); err != nil || hidden != nil {
		t.Fatalf("foreign execution visible: %+v %v", hidden, err)
	}
	if listed, err := p.ListWorkflowExecutions(readerCtx, wf.ID); err != nil || len(listed) != 0 {
		t.Fatalf("foreign execution listed: %d %v", len(listed), err)
	}
	if err := p.DecideWorkflowExecution(readerCtx, wf.ID, private.ID, private.Revision, "cancel"); err == nil {
		t.Fatal("foreign execution cancelled")
	}
}
