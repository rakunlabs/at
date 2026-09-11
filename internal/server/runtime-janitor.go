package server

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Sweep only terminal per-task directories with verified workspace ownership.
// Assets and unassociated run directories are never guessed from age.
func (s *Server) sweepExecutionWorkspaces(ctx context.Context, base string, now time.Time, ttl time.Duration) int {
	store, ok := s.store.(service.ExecutionCleanupStorer)
	if !ok || !service.HasExecutionMaintenance(ctx) || ttl < 0 {
		return 0
	}
	root, err := os.OpenRoot(filepath.Join(base, "workspaces"))
	if err != nil {
		return 0
	}
	defer root.Close()
	dir, err := service.OpenExecutionFile(root, ".", os.O_RDONLY, 0)
	if err != nil {
		return 0
	}
	defer dir.Close()
	workspaces, err := dir.ReadDir(-1)
	if err != nil {
		return 0
	}
	removed := 0
	for _, workspace := range workspaces {
		if !workspace.IsDir() || workspace.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(workspace.Name(), "tasks")
		tasks, err := service.OpenExecutionFile(root, path, os.O_RDONLY, 0)
		if err != nil {
			continue
		}
		entries, err := tasks.ReadDir(-1)
		tasks.Close()
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if ctx.Err() != nil {
				return removed
			}
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			task, err := store.GetExecutionCleanupTask(ctx, workspace.Name(), entry.Name())
			if err != nil || task == nil || !service.IsTerminalTaskStatus(task.Status) {
				continue
			}
			terminal := pickTerminalTime(task)
			if terminal.IsZero() || now.Sub(terminal) < ttl {
				continue
			}
			if err := service.RemoveExecutionTree(ctx, root, filepath.Join(path, entry.Name())); err == nil {
				removed++
			}
		}
	}
	return removed
}
