package postgres

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

func TestTerminalPersistenceAndOwnership(t *testing.T) {
	p := newTestStore(t, nil)
	owner, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "terminal-owner", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "terminal-other", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	item := service.TerminalSession{ID: ulid.Make().String(), OwnerID: owner.ID, TargetID: "host-one", TargetName: "host", Username: "root", Title: "Build", Position: 1}
	if err := p.CreateTerminal(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	if got, err := p.GetTerminal(t.Context(), other.ID, item.ID); err != nil || got != nil {
		t.Fatalf("cross-owner read: %+v %v", got, err)
	}
	if err := p.UpdateTerminal(t.Context(), other.ID, item.ID, "hijacked", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteTerminal(t.Context(), other.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	got, err := p.GetTerminal(t.Context(), owner.ID, item.ID)
	if err != nil || got == nil || *got != item {
		t.Fatalf("record changed: %+v %v", got, err)
	}
	prefs := service.TerminalPreferences{ActiveID: item.ID, DefaultUsers: map[string]string{"host-one": "root"}}
	if err := p.SetTerminalPreferences(t.Context(), owner.ID, prefs); err != nil {
		t.Fatal(err)
	}
	// A second store facade has no process-local terminal state.
	second := &Postgres{db: p.db, goqu: p.goqu, tableAuthUsers: p.tableAuthUsers}
	loaded, err := second.GetTerminalPreferences(t.Context(), owner.ID)
	if err != nil || loaded.ActiveID != item.ID || loaded.DefaultUsers["host-one"] != "root" {
		t.Fatalf("preferences not durable: %+v %v", loaded, err)
	}
	items, err := second.ListTerminals(t.Context(), owner.ID)
	if err != nil || len(items) != 1 || items[0] != item {
		t.Fatalf("tabs not durable: %+v %v", items, err)
	}
	if err := p.UpdateTerminal(t.Context(), owner.ID, item.ID, "Renamed", 0); err != nil {
		t.Fatal(err)
	}
	if err := p.DeleteTerminal(t.Context(), owner.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	items, err = p.ListTerminals(t.Context(), owner.ID)
	if err != nil || len(items) != 0 {
		t.Fatalf("delete: %+v %v", items, err)
	}
}

func TestTerminalConcurrentLimit(t *testing.T) {
	p := newTestStore(t, nil)
	owner, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "terminal-owner", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	var succeeded atomic.Int32
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			item := service.TerminalSession{ID: ulid.Make().String(), OwnerID: owner.ID, TargetID: "host", Username: "root", Title: fmt.Sprint(i)}
			if p.CreateTerminal(t.Context(), item) == nil {
				succeeded.Add(1)
			}
		})
	}
	wg.Wait()
	if succeeded.Load() != 32 {
		t.Fatalf("created %d terminals; want 32", succeeded.Load())
	}
}

func TestTerminalLifecycleLock(t *testing.T) {
	p := newTestStore(t, nil)
	owner, err := p.CreateAuthUser(t.Context(), service.AuthUser{Username: "terminal-owner", Admin: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	item := service.TerminalSession{ID: ulid.Make().String(), OwnerID: owner.ID, TargetID: "host", Username: "root", Title: "test"}
	if err := p.CreateTerminal(t.Context(), item); err != nil {
		t.Fatal(err)
	}
	failedStop := errors.New("host unavailable")
	if err := p.OperateTerminal(t.Context(), owner.ID, item.ID, true, func(service.TerminalSession) error { return failedStop }); !errors.Is(err, failedStop) {
		t.Fatal(err)
	}
	if got, err := p.GetTerminal(t.Context(), owner.ID, item.ID); err != nil || got == nil {
		t.Fatalf("failed stop removed saved row: %+v %v", got, err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	stopped := make(chan error, 1)
	go func() {
		stopped <- p.OperateTerminal(t.Context(), owner.ID, item.ID, true, func(service.TerminalSession) error { close(entered); <-release; return nil })
	}()
	<-entered
	restarted := make(chan error, 1)
	var started atomic.Bool
	go func() {
		restarted <- p.OperateTerminal(t.Context(), owner.ID, item.ID, false, func(service.TerminalSession) error { started.Store(true); return nil })
	}()
	close(release)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if err := <-restarted; err == nil || started.Load() {
		t.Fatal("a concurrent restart ran after terminal deletion")
	}
}
