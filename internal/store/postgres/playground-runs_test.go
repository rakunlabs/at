package postgres

import (
	"errors"
	"testing"

	"github.com/doug-martin/goqu/v9"

	"github.com/rakunlabs/at/internal/service"
)

func TestPlaygroundRunReceipt(t *testing.T) {
	p := newTestStore(t, nil)
	ctx := t.Context()
	c := playgroundConversation(t, p, "user-a")
	browser := []service.PlaygroundMessage{{Role: "user", Data: map[string]any{"content": "hi"}}}

	if run, err := p.GetPlaygroundRun(ctx, "user-a", c.ID); err != nil || run != nil {
		t.Fatalf("idle conversation: %+v %v", run, err)
	}
	if err := p.AcquirePlaygroundRun(ctx, "user-b", c.ID, "run-x", "ws"); !errors.Is(err, service.ErrPlaygroundNotFound) {
		t.Fatalf("foreign owner acquired: %v", err)
	}
	if err := p.AcquirePlaygroundRun(ctx, "user-a", c.ID, "run-1", "ws"); err != nil {
		t.Fatal(err)
	}
	if err := p.AcquirePlaygroundRun(ctx, "user-a", c.ID, "run-2", "ws"); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("second live run acquired: %v", err)
	}

	// While run-1 is live, only run-1 writes the transcript.
	if _, err := p.AppendPlaygroundMessages(ctx, "user-a", c.ID, browser); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("browser append during run: %v", err)
	}
	if err := p.TruncatePlaygroundMessages(ctx, "user-a", c.ID, 1); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("truncate during run: %v", err)
	}
	if err := p.DeletePlaygroundConversation(ctx, "user-a", c.ID); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("delete during run: %v", err)
	}
	runCtx := service.ContextWithPlaygroundRun(ctx, "run-1")
	if _, err := p.AppendPlaygroundMessages(runCtx, "user-a", c.ID, browser); err != nil {
		t.Fatalf("owner append: %v", err)
	}
	if _, err := p.AppendPlaygroundMessages(service.ContextWithPlaygroundRun(ctx, "run-2"), "user-a", c.ID, browser); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("non-owner run append: %v", err)
	}

	// Cancellation stops renewal; the owner learns it from the heartbeat.
	if err := p.HeartbeatPlaygroundRun(ctx, "user-a", c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	// A Stop aimed at an earlier run does not reach this one.
	if err := p.CancelPlaygroundRun(ctx, "user-a", c.ID, "run-0"); err != nil {
		t.Fatal(err)
	}
	if run, _ := p.GetPlaygroundRun(ctx, "user-a", c.ID); run == nil || run.CancelRequested {
		t.Fatalf("stale stop cancelled the live run: %+v", run)
	}
	if err := p.CancelPlaygroundRun(ctx, "user-a", c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if run, err := p.GetPlaygroundRun(ctx, "user-a", c.ID); err != nil || run == nil || !run.CancelRequested || run.ID != "run-1" || run.WorkspaceID != "ws" {
		t.Fatalf("cancelled run: %+v %v", run, err)
	}
	if err := p.HeartbeatPlaygroundRun(ctx, "user-a", c.ID, "run-1"); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("heartbeat after cancel: %v", err)
	}

	// Releasing with a stale ID is a no-op; the owner's release frees it.
	if err := p.ReleasePlaygroundRun(ctx, "user-a", c.ID, "run-2"); err != nil {
		t.Fatal(err)
	}
	if run, _ := p.GetPlaygroundRun(ctx, "user-a", c.ID); run == nil {
		t.Fatal("foreign release cleared the receipt")
	}
	if err := p.ReleasePlaygroundRun(ctx, "user-a", c.ID, "run-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AppendPlaygroundMessages(ctx, "user-a", c.ID, browser); err != nil {
		t.Fatalf("browser append after release: %v", err)
	}

	// A stale receipt is taken over, and the superseded run is fenced out.
	if err := p.AcquirePlaygroundRun(ctx, "user-a", c.ID, "run-3", "ws"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.goqu.Update(p.tablePlaygroundChats).Set(goqu.Record{"active_run_heartbeat_at": goqu.L("clock_timestamp() - interval '5 minutes'")}).
		Where(goqu.Ex{"id": c.ID}).Executor().ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if run, _ := p.GetPlaygroundRun(ctx, "user-a", c.ID); run == nil || !run.Stale {
		t.Fatalf("stale receipt not reported: %+v", run)
	}
	if _, err := p.AppendPlaygroundMessages(ctx, "user-a", c.ID, browser); err != nil {
		t.Fatalf("browser append over stale receipt: %v", err)
	}
	if err := p.AcquirePlaygroundRun(ctx, "user-a", c.ID, "run-4", "ws"); err != nil {
		t.Fatalf("stale takeover: %v", err)
	}
	if _, err := p.AppendPlaygroundMessages(service.ContextWithPlaygroundRun(ctx, "run-3"), "user-a", c.ID, browser); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("superseded run still writes: %v", err)
	}
	if err := p.HeartbeatPlaygroundRun(ctx, "user-a", c.ID, "run-3"); !errors.Is(err, service.ErrPlaygroundRunBusy) {
		t.Fatalf("superseded heartbeat: %v", err)
	}
}
