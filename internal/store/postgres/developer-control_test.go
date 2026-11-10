package postgres

import (
	"errors"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestDeveloperSpaceControlSuspendsAndBlocksWrites(t *testing.T) {
	p, ctx, _, _ := workspaceFixture(t)
	space, err := p.EnsureDeveloperSpace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "stop-owner", "stop"); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteDeveloperSpace(ctx, space.ID); !errors.Is(err, service.ErrDeveloperSpaceBusy) {
		t.Fatalf("foreign reset erased control: %v", err)
	}
	if _, err := p.UpdateDeveloperSpace(ctx, *space); !errors.Is(err, service.ErrDeveloperSpaceBusy) {
		t.Fatalf("reconfiguration during control: %v", err)
	}
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "other-control", "start"); !errors.Is(err, service.ErrDeveloperSpaceBusy) {
		t.Fatalf("second controller admitted: %v", err)
	}
	if err := p.ReleaseDeveloperSpaceControl(ctx, space.ID, "foreign", false); err != nil {
		t.Fatal(err)
	}
	stored, _ := p.GetDeveloperSpace(ctx, space.ID)
	if stored.ActiveControlID != "stop-owner" || !stored.ExecutionSuspended {
		t.Fatal("foreign release reopened space")
	}
	// The owner of a control may write through it (Reset deletes the row).
	owned := service.ContextWithDeveloperSpaceControl(ctx, "stop-owner")
	if _, err := p.UpdateDeveloperSpace(owned, *space); err != nil {
		t.Fatalf("control owner refused: %v", err)
	}
	if err := p.ReleaseDeveloperSpaceControl(ctx, space.ID, "stop-owner", true); err != nil {
		t.Fatal(err)
	}
	stored, _ = p.GetDeveloperSpace(ctx, space.ID)
	if stored.ActiveControlID != "" || !stored.ExecutionSuspended {
		t.Fatalf("stop must leave the space suspended: %+v", stored)
	}
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "start-owner", "start"); err != nil {
		t.Fatal(err)
	}
	if err := p.ReleaseDeveloperSpaceControl(ctx, space.ID, "start-owner", false); err != nil {
		t.Fatal(err)
	}
	if stored, _ = p.GetDeveloperSpace(ctx, space.ID); stored.ExecutionSuspended {
		t.Fatal("explicit Start did not reopen admission")
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
		} else if !errors.Is(err, service.ErrDeveloperSpaceBusy) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("%d control owners", winners)
	}
	// A restart does not drop the receipt; same ID cannot reacquire (ABA).
	if err := p.AcquireDeveloperSpaceControl(ctx, space.ID, "same-id", "reset"); !errors.Is(err, service.ErrDeveloperSpaceBusy) {
		t.Fatalf("control stolen: %v", err)
	}
}
