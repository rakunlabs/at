package container

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// sandboxUse lasts until the backend operation has returned (or a terminal has
// actually closed), not merely until cancellation has been requested.
type sandboxUse struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (m *Manager) beginUse(ctx context.Context, scope string, cfg Config) (context.Context, string, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !cfg.Enabled {
		return ctx, "", nil, fmt.Errorf("container not enabled for scope %s", scope)
	}
	handle, err := m.ensureContainer(ctx, scope, cfg)
	if err != nil {
		return ctx, "", nil, err
	}
	info := m.containers[scope]
	ctx, cancel := context.WithCancel(ctx)
	use := &sandboxUse{cancel: cancel, done: make(chan struct{})}
	if info.uses == nil {
		info.uses = make(map[*sandboxUse]struct{})
	}
	info.uses[use] = struct{}{}
	info.active++
	var once sync.Once
	finish := func() {
		once.Do(func() {
			cancel()
			m.mu.Lock()
			delete(info.uses, use)
			info.active--
			info.lastUsed = time.Now()
			close(use.done)
			m.mu.Unlock()
		})
	}
	return ctx, handle, finish, nil
}

// Stop/Reset first close admission, then cancel and drain uses without holding
// mu (their finish callbacks need it), and only then touch workload/storage.
// This is local draining, not proof that remote effects are fenced after an outage.
func (m *Manager) endScope(ctx context.Context, scope string, purge bool) error {
	m.mu.Lock()
	if m.reconfiguring {
		m.mu.Unlock()
		return fmt.Errorf("sandbox backend is changing; retry after the transition")
	}
	info := m.containers[scope]
	if info == nil && !purge {
		if _, ok := m.driver.(ScopeStopper); !ok {
			m.mu.Unlock()
			return nil
		}
	}
	if info == nil {
		info = &containerInfo{stopping: true}
		m.containers[scope] = info
	} else if info.stopping {
		m.mu.Unlock()
		return fmt.Errorf("sandbox for scope %s is already stopping", scope)
	} else {
		info.stopping = true
	}
	uses := make([]*sandboxUse, 0, len(info.uses))
	for use := range info.uses {
		uses = append(uses, use)
		use.cancel()
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		info.stopping = false
		m.mu.Unlock()
	}()
	for _, use := range uses {
		select {
		case <-use.done:
		case <-ctx.Done():
			m.mu.Lock()
			info.drainFailed = true
			m.mu.Unlock()
			return fmt.Errorf("drain sandbox for scope %s: %w", scope, ctx.Err())
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	if purge {
		err = m.driver.Purge(ctx, scope)
	} else if info.containerID == "" {
		err = m.driver.(ScopeStopper).StopScope(ctx, scope)
	} else {
		err = m.release(ctx, info)
	}
	if err != nil {
		info.drainFailed = true
		return fmt.Errorf("release sandbox for scope %s: %w", scope, err)
	}
	delete(m.containers, scope)
	return nil
}
