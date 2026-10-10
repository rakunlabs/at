package container

import (
	"context"
	"errors"
	"testing"
	"time"
)

type probeFailureDriver struct {
	*fakeDriver
	handle    string
	createErr error
}

type shutdownDriver struct {
	*fakeDriver
	err    error
	called bool
}

func (d *shutdownDriver) Shutdown(context.Context) error { d.called = true; return d.err }

func TestManagerShutdownUsesBackendDrainAndRetainsFailures(t *testing.T) {
	d := &shutdownDriver{fakeDriver: newFakeDriver(), err: errors.New("remote shutdown failed")}
	m := NewWithDriver(d)
	if _, err := m.EnsureContainer(context.Background(), "scope", enabled); err != nil {
		t.Fatal(err)
	}
	if err := m.Shutdown(context.Background()); !errors.Is(err, d.err) {
		t.Fatalf("shutdown failure hidden: %v", err)
	}
	if !d.called || len(m.ListContainers()) != 1 || len(d.removed) != 0 {
		t.Fatal("manager bypassed remote drain or discarded tracking")
	}
	d.err = nil
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(m.ListContainers()) != 0 {
		t.Fatal("successful shutdown retained local tracking")
	}
}

func TestManagerShutdownReportsFallbackCleanupFailure(t *testing.T) {
	d := newFakeDriver()
	m := NewWithDriver(d)
	if _, err := m.EnsureContainer(context.Background(), "scope", enabled); err != nil {
		t.Fatal(err)
	}
	d.failRemove = errors.New("delete failed")
	if err := m.Shutdown(context.Background()); err == nil {
		t.Fatal("failed Docker shutdown reported success")
	}
	if len(m.ListContainers()) != 1 {
		t.Fatal("failed shutdown discarded retry tracking")
	}
}

func (d *probeFailureDriver) Running(context.Context, string) bool { return false }
func (d *probeFailureDriver) Create(context.Context, string, Config) (string, error) {
	return d.handle, d.createErr
}

func TestManagerFailedHealthProbePreservesActivity(t *testing.T) {
	d := &probeFailureDriver{fakeDriver: newFakeDriver(), handle: "existing"}
	m := NewWithDriver(d)
	if _, err := m.EnsureContainer(context.Background(), "scope", enabled); err != nil {
		t.Fatal(err)
	}
	m.markActive("scope", 1)
	original := m.containers["scope"]
	d.createErr = errors.New("API unavailable")
	if _, err := m.EnsureContainer(context.Background(), "scope", enabled); err == nil {
		t.Fatal("reconciliation failure ignored")
	}
	if m.containers["scope"] != original || original.active != 1 {
		t.Fatal("failed health/reconciliation discarded active ownership")
	}
	d.createErr = nil
	if _, err := m.EnsureContainer(context.Background(), "scope", enabled); err == nil {
		t.Fatal("active workload reconciled after failed health probe")
	}
	if m.containers["scope"] != original || original.active != 1 {
		t.Fatal("reusing the same handle reset active work")
	}
}

func TestManagerLifecycleFailureRetainsTracking(t *testing.T) {
	backendErr := errors.New("backend unavailable")
	for _, operation := range []string{"stop", "remove", "idle", "shutdown", "purge", "reconfigure"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			driver := newFakeDriver()
			m := NewWithDriver(driver)
			cfg := enabled
			cfg.RetainWhenIdle = operation == "stop"
			handle, err := m.EnsureContainer(ctx, "scope", cfg)
			if err != nil {
				t.Fatal(err)
			}
			m.containers["scope"].lastUsed = time.Now().Add(-time.Hour)
			driver.failRemove, driver.failStop, driver.failPurge = backendErr, backendErr, backendErr
			var got error
			switch operation {
			case "stop", "remove":
				got = m.StopContainer(ctx, "scope")
			case "idle":
				m.CleanupIdle(ctx, time.Minute)
			case "shutdown":
				m.StopAll(ctx)
			case "purge":
				got = m.RemoveScope(ctx, "scope")
			case "reconfigure":
				changed := cfg
				changed.Image = "img:2"
				_, got = m.EnsureContainer(ctx, "scope", changed)
			}
			if operation != "idle" && operation != "shutdown" && !errors.Is(got, backendErr) {
				t.Fatalf("must return the backend error, got %v", got)
			}
			if info := m.containers["scope"]; info == nil || info.containerID != handle {
				t.Fatal("a failed lifecycle operation must retain the original tracking record")
			}
			if len(driver.created) != 1 || !driver.Running(ctx, handle) {
				t.Fatal("a failed replacement must not create another sandbox or lose the running one")
			}
			driver.failRemove, driver.failStop, driver.failPurge = nil, nil, nil
			if err := m.StopContainer(ctx, "scope"); err != nil {
				t.Fatalf("cleanup must be retryable: %v", err)
			}
			if len(m.ListContainers()) != 0 || driver.Running(ctx, handle) {
				t.Fatal("successful retry must release the sandbox")
			}
		})
	}
}

func TestManagerCleanupContinuesAfterFailure(t *testing.T) {
	ctx := context.Background()
	driver := newFakeDriver()
	m := NewWithDriver(driver)
	retained := enabled
	retained.RetainWhenIdle = true
	_, _ = m.EnsureContainer(ctx, "fails", retained)
	_, _ = m.EnsureContainer(ctx, "succeeds", enabled)
	driver.failStop = errors.New("cannot stop")
	m.StopAll(ctx)
	if len(m.containers) != 1 || m.containers["fails"] == nil {
		t.Fatal("shutdown must retain failures but continue releasing other scopes")
	}
}

type fakeHomeDriver struct {
	*fakeDriver
	failHome     error
	removedHomes []string
}

func (f *fakeHomeDriver) RemoveHome(_ context.Context, homeScope string) error {
	if f.failHome != nil {
		return f.failHome
	}
	f.removedHomes = append(f.removedHomes, homeScope)
	return nil
}

func TestManagerHomeRemovalInvalidatesAllLocalMounts(t *testing.T) {
	ctx := context.Background()
	driver := &fakeHomeDriver{fakeDriver: newFakeDriver()}
	m := NewWithDriver(driver)
	for _, scope := range []string{"workspace-a", "workspace-b"} {
		cfg := enabled
		cfg.HomeScope, cfg.HomePath = "user-home", "/root"
		_, _ = m.EnsureContainer(ctx, scope, cfg)
	}
	other := enabled
	other.HomeScope, other.HomePath = "other-home", "/root"
	_, _ = m.EnsureContainer(ctx, "other", other)
	_, _ = m.EnsureContainer(ctx, "explicit", enabled)
	backendErr := errors.New("home volume busy")
	driver.failHome = backendErr
	if err := m.RemoveHome(ctx, "user-home", "explicit"); !errors.Is(err, backendErr) {
		t.Fatalf("must surface home removal error: %v", err)
	}
	if len(m.containers) != 4 {
		t.Fatal("failed home removal must not invalidate local mounts")
	}
	driver.failHome = nil
	if err := m.RemoveHome(ctx, "user-home", "explicit"); err != nil {
		t.Fatal(err)
	}
	if len(m.containers) != 1 || m.containers["other"] == nil {
		t.Fatal("home reset must invalidate every matching mount, but never another user's scope")
	}
}
