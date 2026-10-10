package container

import (
	"context"
	"fmt"
	"sync"
)

// Reconfigure closes admission before cancelling and draining all registered
// activity. Persistent storage is never purged or moved between backends.
// A failed drain keeps admission closed until an explicit retry succeeds.
// This is local coordination: callers must require a single AT replica.
func (m *Manager) Reconfigure(ctx context.Context, next Driver) (err error) {
	if next == nil {
		return fmt.Errorf("sandbox backend is required")
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.mu.Lock()
	for _, info := range m.containers {
		if info.stopping {
			m.mu.Unlock()
			return fmt.Errorf("a sandbox control operation is in progress; retry after it finishes")
		}
	}
	m.reconfiguring = true
	old := m.driver
	uses := []*sandboxUse{}
	for use := range m.runtimeUses {
		use.cancel()
		uses = append(uses, use)
	}
	for _, info := range m.containers {
		for use := range info.uses {
			use.cancel()
			uses = append(uses, use)
		}
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.reconfiguring = false
		m.runtimeErr = err
		m.mu.Unlock()
	}()
	for _, use := range uses {
		select {
		case <-use.done:
		case <-ctx.Done():
			return fmt.Errorf("drain sandbox activity: %w", ctx.Err())
		}
	}
	if shutdown, ok := old.(ShutdownDriver); ok {
		if err := shutdown.Shutdown(ctx); err != nil {
			// The ownership guard is closed after a failed release, so retrying
			// in-process cannot switch safely.
			return fmt.Errorf("stop previous sandbox controller: %w; recover the previous controller and restart AT (see the Kubernetes guide)", err)
		}
	} else {
		m.mu.Lock()
		for scope, info := range m.containers {
			var stopErr error
			stopper, canStopScope := old.(ScopeStopper)
			switch {
			case info.containerID != "":
				stopErr = old.Stop(ctx, info.containerID)
			case canStopScope:
				// Failed-drain placeholders have no handle; stop by scope.
				stopErr = stopper.StopScope(ctx, scope)
			default:
				stopErr = fmt.Errorf("no workload handle")
			}
			if stopErr != nil {
				m.mu.Unlock()
				return fmt.Errorf("stop previous sandbox %s: %w", scope, stopErr)
			}
		}
		m.mu.Unlock()
		if closer, ok := old.(DriverCloser); ok {
			if err := closer.Close(); err != nil {
				return fmt.Errorf("close previous sandbox controller: %w", err)
			}
		}
	}
	m.mu.Lock()
	clear(m.containers)
	m.driver = next
	m.mu.Unlock()
	return nil
}

// RuntimeError reports failed transition/startup state without opening admission.
func (m *Manager) RuntimeError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runtimeErr
}

// Suspend leaves the UI available when stored backend credentials cannot load.
func (m *Manager) Suspend(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtimeErr = err
}

// BeginRuntimeRun covers a whole coding/isolated-agent turn, including the time
// it spends in a model call. A transition must not move an in-flight turn to a
// different backend between two tools.
func (m *Manager) BeginRuntimeRun(ctx context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reconfiguring || m.runtimeErr != nil {
		return nil, nil, fmt.Errorf("sandbox backend is changing or unavailable; check System settings")
	}
	ctx, cancel := context.WithCancel(ctx)
	use := &sandboxUse{cancel: cancel, done: make(chan struct{})}
	if m.runtimeUses == nil {
		m.runtimeUses = map[*sandboxUse]struct{}{}
	}
	m.runtimeUses[use] = struct{}{}
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			cancel()
			m.mu.Lock()
			delete(m.runtimeUses, use)
			close(use.done)
			m.mu.Unlock()
		})
	}, nil
}
