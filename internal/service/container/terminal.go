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
	ctx, containerID, finish, err := m.beginUse(ctx, scopeID, cfg)
	if err != nil {
		return nil, err
	}
	if containerID == "" {
		return nil, fmt.Errorf("container not enabled for scope %s", scopeID)
	}
	inner, err := m.driver.Attach(ctx, containerID, workDir, cols, rows)
	if err != nil {
		finish()
		return nil, err
	}
	term := &managedTerminal{Terminal: inner, done: make(chan struct{}), release: finish}
	{
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
					if cfg.DiskLimitBytes <= 0 {
						continue
					}
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
		t.err = t.Terminal.Close()
		t.release()
	})
	return t.err
}
