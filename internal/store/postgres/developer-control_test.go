package postgres

import (
	"errors"
	"sync"
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestDeveloperSpaceControlSuspendsAndPreservesReceipts(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	space, err := p.EnsureDeveloperSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, err := p.CreateDeveloperSession(ctx, service.DeveloperSession{SpaceID: space.ID, Mode: service.DeveloperModeBuild, Provider: "openai", Model: "gpt"})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "remote-run"); err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "stop-owner", "stop"); err != nil {
		t.Fatal(err)
	}
	if err := p.HeartbeatDeveloperRun(ctx, session.ID, "remote-run"); err == nil {
		t.Fatal("remote owner missed persisted cancellation")
	}
	if err := p.DeleteDeveloperSession(ctx, session.ID); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("deleted receipt: %v", err)
	}
	if err := p.DeleteDeveloperSpace(ctx, space.ID); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("foreign reset erased control: %v", err)
	}
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "other-control", "start"); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("second controller admitted: %v", err)
	}
	if err := p.ReleaseDeveloperSpaceControl(ctx, space.ID, "foreign", false); err != nil {
		t.Fatal(err)
	}
	stored, _ := p.GetDeveloperSpace(ctx, space.ID)
	if stored.ActiveControlID != "stop-owner" || !stored.ExecutionSuspended {
		t.Fatal("foreign release reopened space")
	}
	if err := p.ReleaseDeveloperSpaceControl(ctx, space.ID, "stop-owner", true); err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "start-owner", "start"); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("start ignored unresolved run: %v", err)
	}
	if err := p.ReleaseDeveloperRun(ctx, session.ID, "remote-run"); err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "new-run"); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("stopped space admitted work: %v", err)
	}
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "start-owner", "start"); err != nil {
		t.Fatal(err)
	}
	if err := p.ReleaseDeveloperSpaceControl(ctx, space.ID, "start-owner", false); err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperRun(ctx, session.ID, "new-run"); err != nil {
		t.Fatalf("explicit Start did not reopen admission: %v", err)
	}
	if _, err := p.UpdateDeveloperSpace(ctx, *space); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("reconfiguration during run: %v", err)
	}
	_, err = p.goqu.Update(p.tableDeveloperSessions).Set(goqu.Record{"active_run_heartbeat_at": goqu.L("clock_timestamp() - interval '61 seconds'")}).Where(goqu.Ex{"id": session.ID}).Executor().ExecContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteDeveloperSpace(ctx, space.ID); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("expired receipt erased: %v", err)
	}
}

func TestDeveloperSpaceControlsHaveOneConcurrentOwner(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	space, err := p.EnsureDeveloperSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for range 12 {
		wg.Go(func() { results <- p.AcquireDeveloperSpaceControl(ctx, space.ID, "same-id", "stop") })
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
		t.Fatalf("%d control owners", winners)
	}
	// A restart does not drop the receipt; same ID cannot reacquire (ABA).
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "same-id", "reset"); !errors.Is(err, service.ErrDeveloperSessionBusy) {
		t.Fatalf("control stolen: %v", err)
	}
}
