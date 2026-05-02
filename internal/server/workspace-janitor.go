package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// workspaceJanitorInterval is how often the janitor wakes up. We don't
// need this to fire often: TTL is in hours, deletion is cheap, and we
// already wake on server start. Once an hour is the right cadence.
const workspaceJanitorInterval = 1 * time.Hour

// terminalTaskStatuses are the statuses for which a task workspace is
// safe to remove (after the TTL has elapsed). Anything else — backlog,
// todo, open, in_progress, in_review, review — keeps its workspace.
var terminalTaskStatuses = map[string]bool{
	service.TaskStatusDone:      true,
	service.TaskStatusCompleted: true,
	service.TaskStatusCancelled: true,
	service.TaskStatusBlocked:   true,
}

// startWorkspaceJanitor starts a background goroutine that periodically
// sweeps WorkspaceRoot for old task workspaces and tool-output dumps.
//
// Two paths are swept:
//  1. <WorkspaceRoot>/<task-id>/        — per-task workspaces created
//     by the org-delegation pipeline. We look up the task in the DB:
//     skip unknown ids (might be from a different host on shared FS),
//     skip tasks not in a terminal status, skip tasks whose terminal
//     timestamp is younger than WorkspaceTTL.
//  2. <WorkspaceRoot>/.at-tool-output/<run-id>/  — truncated tool
//     payload dumps (loopgov.TruncateToolResult). These aren't tied
//     to task lifecycle, so we use mtime: anything older than TTL
//     gets removed.
//
// A WorkspaceTTL < 0 disables the janitor entirely (operator opt-out).
// Errors per-entry are logged at debug level — a single bad entry
// shouldn't stop the sweep.
func (s *Server) startWorkspaceJanitor(ctx context.Context) {
	if s.loopGov == nil {
		return
	}
	cfg := s.loopGov.Config()
	if cfg.WorkspaceRoot == "" || cfg.WorkspaceTTL < 0 {
		slog.Info("workspace_janitor: disabled",
			"workspace_root", cfg.WorkspaceRoot,
			"workspace_ttl", cfg.WorkspaceTTL)
		return
	}

	go func() {
		// Run once on start so a fresh process immediately picks up
		// stale workspaces from a previous run that crashed.
		s.sweepWorkspaceOnce(ctx, cfg.WorkspaceRoot, cfg.WorkspaceTTL)

		ticker := time.NewTicker(workspaceJanitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.sweepWorkspaceOnce(ctx, cfg.WorkspaceRoot, cfg.WorkspaceTTL)
			}
		}
	}()
}

// sweepWorkspaceOnce performs one pass over the workspace root.
// Exposed (lowercase but package-internal) so tests can drive it.
func (s *Server) sweepWorkspaceOnce(ctx context.Context, root string, ttl time.Duration) {
	if ctx.Err() != nil {
		return
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		// Root not yet created, or permission error. Either way,
		// nothing to sweep — log at debug to avoid spam at startup
		// when /tmp/at-tasks doesn't exist yet.
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("workspace_janitor: read root failed",
				"root", root, "error", err.Error())
		}
		return
	}

	now := time.Now()
	cutoff := now.Add(-ttl)
	removedTasks := 0
	removedDumps := 0
	keptTasks := 0
	var freedBytes int64

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		full := filepath.Join(root, name)

		// Tool-output dump dir: sweep by mtime alone.
		if name == ".at-tool-output" {
			n, freed := s.sweepToolOutputDir(ctx, full, cutoff)
			removedDumps += n
			freedBytes += freed
			continue
		}

		// Skip dot-dirs and obviously non-task entries we may add later.
		if strings.HasPrefix(name, ".") {
			continue
		}

		// Treat the dir name as a task ID. If the task isn't in the
		// store, it's probably from a different deployment sharing
		// the FS — leave it alone.
		removed, freed := s.maybeRemoveTaskWorkspace(ctx, full, name, now, ttl)
		if removed {
			removedTasks++
			freedBytes += freed
		} else {
			keptTasks++
		}
	}

	if removedTasks > 0 || removedDumps > 0 {
		slog.Info("workspace_janitor: swept",
			"removed_tasks", removedTasks,
			"removed_dumps", removedDumps,
			"kept_tasks", keptTasks,
			"freed_bytes", freedBytes,
			"ttl", ttl,
			"root", root)
	}
}

// maybeRemoveTaskWorkspace decides whether to delete a per-task
// workspace dir and returns (removed, freedBytes).
func (s *Server) maybeRemoveTaskWorkspace(ctx context.Context, fullPath, taskID string, now time.Time, ttl time.Duration) (bool, int64) {
	if s.taskStore == nil {
		return false, 0
	}
	task, err := s.taskStore.GetTask(ctx, taskID)
	if err != nil {
		slog.Debug("workspace_janitor: GetTask failed",
			"task_id", taskID, "error", err.Error())
		return false, 0
	}
	if task == nil {
		// Unknown task on disk — we'd rather leak than nuke someone
		// else's data. A separate "stranger sweep" could reclaim
		// these by mtime + a longer TTL, but that's a future change.
		return false, 0
	}
	if !terminalTaskStatuses[task.Status] {
		return false, 0
	}

	// Pick the most recent terminal timestamp we know. CompletedAt is
	// preferred (fires on done/completed); CancelledAt for cancelled
	// tasks; otherwise fall back to UpdatedAt as a belt-and-suspenders
	// guard against missing timestamps in older rows.
	terminalAt := pickTerminalTime(task)
	if terminalAt.IsZero() {
		// No usable timestamp — assume "recently terminal", skip.
		return false, 0
	}
	if now.Sub(terminalAt) < ttl {
		return false, 0
	}

	freed := dirSize(fullPath)
	if err := os.RemoveAll(fullPath); err != nil {
		slog.Warn("workspace_janitor: RemoveAll failed",
			"path", fullPath, "error", err.Error())
		return false, 0
	}
	slog.Debug("workspace_janitor: removed task workspace",
		"task_id", taskID,
		"status", task.Status,
		"terminal_at", terminalAt,
		"freed_bytes", freed)
	return true, freed
}

// sweepToolOutputDir removes per-runID dump dirs whose mtime is older
// than cutoff. Returns (removedCount, freedBytes).
func (s *Server) sweepToolOutputDir(_ context.Context, dumpRoot string, cutoff time.Time) (int, int64) {
	entries, err := os.ReadDir(dumpRoot)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Debug("workspace_janitor: read tool-output dir failed",
				"path", dumpRoot, "error", err.Error())
		}
		return 0, 0
	}
	var removed int
	var freed int64
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		full := filepath.Join(dumpRoot, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		size := dirSize(full)
		if err := os.RemoveAll(full); err != nil {
			slog.Debug("workspace_janitor: RemoveAll dump dir failed",
				"path", full, "error", err.Error())
			continue
		}
		removed++
		freed += size
	}
	return removed, freed
}

// pickTerminalTime returns the most relevant terminal timestamp on the
// task. RFC3339 strings → time.Time. Returns zero on any parse failure.
func pickTerminalTime(t *service.Task) time.Time {
	for _, s := range []string{t.CompletedAt, t.CancelledAt, t.UpdatedAt} {
		if s == "" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, s); err == nil {
			return ts
		}
	}
	return time.Time{}
}

// dirSize sums the byte size of every regular file under root. Best
// effort: walk errors fall through silently. The return value is
// strictly informational (logged with the sweep summary), never used
// for a decision.
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}
