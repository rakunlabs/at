package postgres

import (
	"errors"
	"sync"
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestDeveloperRunOwnershipAndInterruption(t *testing.T) {
	p, ctx, workspace, _ := workspaceFixture(t)
	space, err := p.EnsureDeveloperSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.CreateDeveloperSession(ctx, service.DeveloperSession{SpaceID: space.ID, Mode: service.DeveloperModeBuild, Provider: "openai", Model: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "run-2"); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("second replica acquired running session: %v", err)
	}
	runCtx := service.ContextWithDeveloperRun(ctx, "run-1")
	if _, err := p.BeginDeveloperSessionRun(runCtx, session.ID); err != nil {
		t.Fatal(err)
	}
	other := workspaceUser(t, p, "foreign-run-reader")
	otherCtx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: other.ID, WorkspaceID: workspace.ID})
	if _, err := p.GetDeveloperRun(otherCtx, session.ID); !errors.Is(err, service.ErrDeveloperSpaceNotFound) {
		t.Fatalf("foreign receipt visible: %v", err)
	}
	if err := p.ReleaseDeveloperRun(ctx, session.ID, "foreign-run"); err != nil {
		t.Fatal(err)
	}
	run, err := p.GetDeveloperRun(ctx, session.ID)
	if err != nil || run == nil || run.ID != "run-1" {
		t.Fatalf("foreign release changed ownership: %+v %v", run, err)
	}
	_, err = p.goqu.Update(p.tableDeveloperSessions).Set(goqu.Record{"active_run_heartbeat_at": goqu.L("clock_timestamp() - interval '61 seconds'")}).Where(goqu.Ex{"id": session.ID}).Executor().ExecContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, err = p.GetDeveloperRun(ctx, session.ID)
	if err != nil || run == nil || !run.Interrupted {
		t.Fatalf("expired owner not reported: %+v %v", run, err)
	}
	stored, err := p.GetDeveloperSession(ctx, session.ID)
	if err != nil || stored.Status != service.DeveloperSessionFailed || stored.Error == "" {
		t.Fatalf("interrupted run still running: %+v %v", stored, err)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "run-2"); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("expired receipt was stolen: %v", err)
	}
	if err := p.HeartbeatDeveloperRun(runCtx, session.ID, "run-1"); err == nil {
		t.Fatal("expired owner resurrected")
	}
	if _, err := p.SetDeveloperSessionRuntime(runCtx, session.ID, service.DeveloperSessionCompleted, ""); err == nil {
		t.Fatal("stale owner overwrote interrupted status")
	}
	if _, err := p.AppendDeveloperSessionMessage(runCtx, service.DeveloperSessionMessage{SessionID: session.ID, Role: "assistant", Content: "late answer"}); err == nil {
		t.Fatal("stale owner appended history")
	}
}

func TestDeveloperRunCancellationAndWaitingApproval(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	space, _ := p.EnsureDeveloperSpace(ctx)
	session, err := p.CreateDeveloperSession(ctx, service.DeveloperSession{SpaceID: space.ID, Mode: service.DeveloperModeBuild, Provider: "openai", Model: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	runCtx := service.ContextWithDeveloperRun(ctx, "run")
	if _, err := p.BeginDeveloperSessionRun(runCtx, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.CancelDeveloperRun(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	if err := p.HeartbeatDeveloperRun(runCtx, session.ID, "run"); err == nil {
		t.Fatal("cancelled owner kept running")
	}
	if _, err := p.SetDeveloperSessionRuntime(runCtx, session.ID, service.DeveloperSessionCompleted, ""); err == nil {
		t.Fatal("late completion overwrote cancellation")
	}
	if err := p.ReleaseDeveloperRun(runCtx, session.ID, "run"); err != nil {
		t.Fatal(err)
	}
	stored, _ := p.GetDeveloperSession(ctx, session.ID)
	if stored.Status != service.DeveloperSessionCancelled {
		t.Fatalf("cancel outcome: %+v", stored)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "next"); err != nil {
		t.Fatal(err)
	}
	nextCtx := service.ContextWithDeveloperRun(ctx, "next")
	if _, err := p.BeginDeveloperSessionRun(nextCtx, session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SetDeveloperSessionRuntime(nextCtx, session.ID, service.DeveloperSessionWaitingPermission, ""); err != nil {
		t.Fatal(err)
	}
	if err := p.ReleaseDeveloperRun(nextCtx, session.ID, "next"); err != nil {
		t.Fatal(err)
	}
	stored, _ = p.GetDeveloperSession(ctx, session.ID)
	if stored.Status != service.DeveloperSessionWaitingPermission {
		t.Fatalf("release discarded waiting approval: %+v", stored)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "confirm"); err != nil {
		t.Fatalf("resume could not claim receipt: %v", err)
	}
}

func TestDeveloperRunConcurrentAcquisition(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	space, _ := p.EnsureDeveloperSpace(ctx)
	session, err := p.CreateDeveloperSession(ctx, service.DeveloperSession{SpaceID: space.ID, Mode: service.DeveloperModeBuild, Provider: "openai", Model: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() { results <- p.AcquireDeveloperRun(ctx, session.ID, "concurrent-run") })
	}
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, service.ErrDeveloperSessionBusy) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d concurrent winners", winners)
	}
}
