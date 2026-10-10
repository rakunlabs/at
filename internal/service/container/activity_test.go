package container

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type drainingDriver struct {
	*fakeDriver
	started, cancelled, release chan struct{}
}

func (d *drainingDriver) Exec(ctx context.Context, _ string, _ ExecRequest) (int, error) {
	close(d.started)
	<-ctx.Done()
	close(d.cancelled)
	<-d.release // Simulate a backend that takes time to confirm termination.
	return -1, ctx.Err()
}

func TestManagerStopAndPurgeDrainBeforeRemoval(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop", true: "purge"}[purge], func(t *testing.T) {
			d := &drainingDriver{fakeDriver: newFakeDriver(), started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
			m := NewWithDriver(d)
			work := make(chan error, 1)
			go func() {
				_, _, _, err := m.Exec(t.Context(), "scope", enabled, "wait", nil)
				work <- err
			}()
			<-d.started
			stop := make(chan error, 1)
			go func() {
				if purge {
					stop <- m.RemoveScope(t.Context(), "scope")
				} else {
					stop <- m.StopContainer(t.Context(), "scope")
				}
			}()
			<-d.cancelled
			if _, err := m.EnsureContainer(t.Context(), "scope", enabled); err == nil {
				t.Fatal("new work admitted during draining")
			}
			d.mu.Lock()
			touched := len(d.purged)+len(d.removed)+len(d.stopped) != 0
			d.mu.Unlock()
			if touched {
				t.Fatal("storage/workload removed before command returned")
			}
			close(d.release)
			if err := <-work; !errors.Is(err, context.Canceled) {
				t.Fatalf("work cancellation = %v", err)
			}
			if err := <-stop; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestManagerDrainTimeoutKeepsAdmissionClosed(t *testing.T) {
	d := &drainingDriver{fakeDriver: newFakeDriver(), started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	m := NewWithDriver(d)
	work := make(chan struct{})
	go func() { defer close(work); _, _, _, _ = m.Exec(t.Context(), "scope", enabled, "wait", nil) }()
	<-d.started
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := m.RemoveScope(ctx, "scope"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain timeout = %v", err)
	}
	if _, err := m.EnsureContainer(t.Context(), "scope", enabled); err == nil {
		t.Fatal("timeout reopened admission to uncertain remote work")
	}
	close(d.release)
	<-work
	if _, err := m.EnsureContainer(t.Context(), "scope", enabled); err == nil {
		t.Fatal("failed drain was implicitly recovered")
	}
	if err := m.RemoveScope(t.Context(), "scope"); err != nil {
		t.Fatalf("explicit cleanup retry failed: %v", err)
	}
}

type closeBlockingTerminal struct {
	closed, release chan struct{}
	once            sync.Once
}

func (t *closeBlockingTerminal) Read([]byte) (int, error)    { return 0, nil }
func (t *closeBlockingTerminal) Write(p []byte) (int, error) { return len(p), nil }
func (t *closeBlockingTerminal) Resize(uint16, uint16) error { return nil }
func (t *closeBlockingTerminal) Close() error {
	t.once.Do(func() { close(t.closed); <-t.release })
	return nil
}

type terminalDrainDriver struct {
	*fakeDriver
	term *closeBlockingTerminal
}

func (d *terminalDrainDriver) Attach(context.Context, string, string, uint16, uint16) (Terminal, error) {
	return d.term, nil
}

func TestManagerTerminalCloseDrainsWithoutQuota(t *testing.T) {
	d := &terminalDrainDriver{fakeDriver: newFakeDriver(), term: &closeBlockingTerminal{closed: make(chan struct{}), release: make(chan struct{})}}
	m := NewWithDriver(d)
	term, err := m.AttachShell(t.Context(), "scope", enabled, "/workspace", 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer term.Close() //nolint:errcheck
	stop := make(chan error, 1)
	go func() { stop <- m.StopContainer(t.Context(), "scope") }()
	<-d.term.closed
	if m.ListContainers()["scope"]["active"] != 1 {
		t.Fatal("terminal activity released before backend close returned")
	}
	close(d.term.release)
	if err := <-stop; err != nil {
		t.Fatal(err)
	}
}

type scopeStopDriver struct {
	*fakeDriver
	scopes []string
	err    error
}

func (d *scopeStopDriver) StopScope(_ context.Context, scope string) error {
	d.scopes = append(d.scopes, scope)
	return d.err
}

func TestManagerStopUntrackedScopeChecksBackend(t *testing.T) {
	d := &scopeStopDriver{fakeDriver: newFakeDriver(), err: errors.New("controller unavailable")}
	m := NewWithDriver(d)
	if err := m.StopContainer(t.Context(), "remote"); !errors.Is(err, d.err) {
		t.Fatalf("untracked stop hid backend failure: %v", err)
	}
	d.err = nil
	if err := m.StopContainer(t.Context(), "remote"); err != nil {
		t.Fatal(err)
	}
	if len(d.scopes) != 2 || len(m.ListContainers()) != 0 {
		t.Fatal("untracked stop did not reconcile/retry")
	}
}
