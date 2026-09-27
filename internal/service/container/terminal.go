package container

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// AttachShell starts an interactive shell in a managed container. The returned
// terminal belongs to this attachment; closing it terminates only this shell,
// not the persistent developer-space container or volume.
func (m *Manager) AttachShell(ctx context.Context, scopeID string, cfg Config, workDir string, cols, rows uint16) (Terminal, error) {
	if !insideWorkspace(workDir) {
		return nil, fmt.Errorf("work directory must stay inside /workspace")
	}
	containerID, err := m.EnsureContainer(ctx, scopeID, cfg)
	if err != nil {
		return nil, err
	}
	if containerID == "" {
		return nil, fmt.Errorf("container not enabled for scope %s", scopeID)
	}
	inner, err := m.driver.Attach(ctx, containerID, workDir, cols, rows)
	if err != nil {
		return nil, err
	}
	m.markActive(scopeID, 1)
	term := &managedTerminal{Terminal: inner, done: make(chan struct{}), release: func() { m.markActive(scopeID, -1) }}
	if cfg.DiskLimitBytes > 0 {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-term.done:
					return
				case <-ctx.Done():
					_ = term.Close()
					return
				case <-ticker.C:
					if used, usageErr := m.workspaceUsage(ctx, containerID); usageErr == nil && used > cfg.DiskLimitBytes {
						_ = term.Close()
						return
					}
				}
			}
		}()
	}
	return term, nil
}

// managedTerminal adds the Manager's bookkeeping to a driver terminal: the
// scope stays active while attached and the quota watcher stops on close.
type managedTerminal struct {
	Terminal
	once    sync.Once
	done    chan struct{}
	release func()
	err     error
}

func (t *managedTerminal) Close() error {
	t.once.Do(func() {
		close(t.done)
		t.release()
		t.err = t.Terminal.Close()
	})
	return t.err
}
