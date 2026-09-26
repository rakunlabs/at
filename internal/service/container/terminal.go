package container

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

// AttachShell starts an interactive shell in a managed container. The returned
// PTY belongs to this attachment; closing it terminates only this shell, not the
// persistent developer-space container or volume.
func (m *Manager) AttachShell(ctx context.Context, scopeID string, cfg Config, workDir string, cols, rows uint16) (*os.File, func() error, error) {
	if workDir != "/workspace" && !isWorkspaceChild(workDir) {
		return nil, nil, fmt.Errorf("work directory must stay inside /workspace")
	}
	containerID, err := m.EnsureContainer(ctx, scopeID, cfg)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-it", "-w", workDir, containerID, "bash", "-l")
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, nil, fmt.Errorf("attach container shell: %w", err)
	}
	m.markActive(scopeID, 1)
	var once sync.Once
	var waitErr error
	done := make(chan struct{})
	closeFn := func() error {
		once.Do(func() {
			close(done)
			m.markActive(scopeID, -1)
			_ = file.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			waitErr = cmd.Wait()
		})
		return waitErr
	}
	if cfg.DiskLimitBytes > 0 {
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					_ = closeFn()
					return
				case <-ticker.C:
					if used, usageErr := workspaceUsage(ctx, containerID); usageErr == nil && used > cfg.DiskLimitBytes {
						_ = closeFn()
						return
					}
				}
			}
		}()
	}
	return file, closeFn, nil
}

func isWorkspaceChild(value string) bool {
	return len(value) > len("/workspace/") && value[:len("/workspace/")] == "/workspace/"
}

func ResizePTY(file *os.File, cols, rows uint16) error {
	return pty.Setsize(file, &pty.Winsize{Cols: cols, Rows: rows})
}
