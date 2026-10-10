package container

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestManagerReconfigureDrainsAndPreservesStorage(t *testing.T) {
	d := &drainingDriver{fakeDriver: newFakeDriver(), started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	m := NewWithDriver(d)
	next := newFakeDriver()
	work := make(chan error, 1)
	go func() { _, _, _, err := m.Exec(t.Context(), "scope", enabled, "wait", nil); work <- err }()
	<-d.started
	transition := make(chan error, 1)
	go func() { transition <- m.Reconfigure(t.Context(), next) }()
	<-d.cancelled
	if _, err := m.EnsureContainer(t.Context(), "other", enabled); err == nil {
		t.Fatal("admitted work while draining")
	}
	if m.Driver() != d {
		t.Fatal("switched before remote completion")
	}
	close(d.release)
	if err := <-work; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := <-transition; err != nil {
		t.Fatal(err)
	}
	if m.Driver() != next || m.RuntimeError() != nil {
		t.Fatal("driver not applied")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.stopped) != 1 || len(d.removed) != 0 || len(d.purged) != 0 {
		t.Fatalf("transition removed storage/root: %+v", d.fakeDriver)
	}
}

func TestManagerReconfigureFailedDrainRequiresExplicitRetry(t *testing.T) {
	m := NewWithDriver(newFakeDriver())
	ctx, finish, err := m.BeginRuntimeRun(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer finish()
	next := newFakeDriver()
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := m.Reconfigure(deadline, next); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error: %v", err)
	}
	if ctx.Err() == nil || m.Driver() == next {
		t.Fatal("run not cancelled or premature driver switch")
	}
	finish()
	if _, err := m.EnsureContainer(t.Context(), "scope", enabled); err == nil {
		t.Fatal("failed drain reopened admission")
	}
	if err := m.Reconfigure(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnsureContainer(t.Context(), "scope", enabled); err != nil {
		t.Fatal(err)
	}
}

type scopeOnlyDriver struct {
	*fakeDriver
	stopped []string
}

func (d *scopeOnlyDriver) StopScope(_ context.Context, scope string) error {
	d.stopped = append(d.stopped, scope)
	return nil
}

func TestManagerReconfigureStopsFailedDrainPlaceholders(t *testing.T) {
	d := &scopeOnlyDriver{fakeDriver: newFakeDriver()}
	m := NewWithDriver(d)
	m.containers["scope"] = &containerInfo{drainFailed: true}
	if err := m.Reconfigure(t.Context(), newFakeDriver()); err != nil {
		t.Fatalf("placeholder blocked recovery: %v", err)
	}
	if len(d.stopped) != 1 || d.stopped[0] != "scope" {
		t.Fatalf("placeholder not stopped by scope: %v", d.stopped)
	}
}

func TestManagerReconfigureStopsTerminalBeforeSwitch(t *testing.T) {
	d := &terminalDrainDriver{fakeDriver: newFakeDriver(), term: &closeBlockingTerminal{closed: make(chan struct{}), release: make(chan struct{})}}
	m := NewWithDriver(d)
	term, err := m.AttachShell(t.Context(), "scope", enabled, "/workspace", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close() //nolint:errcheck
	next := newFakeDriver()
	done := make(chan error, 1)
	go func() { done <- m.Reconfigure(t.Context(), next) }()
	<-d.term.closed
	if m.Driver() != d {
		t.Fatal("switched before terminal closed")
	}
	close(d.term.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
