package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// Run history for non-durable workflow runs. The row is written when the run
// starts and closed when it ends, so a run killed with its process is still
// visible: its heartbeat stops and the sweeper marks it interrupted.

const (
	workflowRunHeartbeatInterval = 30 * time.Second
	workflowRunStaleAfter        = 3 * time.Minute
	workflowRunKeepCompleted     = 7 * 24 * time.Hour
	workflowRunKeepFailed        = 30 * 24 * time.Hour
	workflowRunSweepInterval     = 10 * time.Minute
	workflowRunWriteTimeout      = 10 * time.Second
)

// historySources maps registerRun sources to recorded sources. The editor's
// live stream and test runs are omitted: they are watched as they happen.
var historySources = map[string]string{"api": "api", "cron": "cron", "webhook": "webhook", "tool": "tool"}

// workflowRunLive is the set of run IDs this process owns and heartbeats.
type workflowRunLive struct {
	mu  sync.Mutex
	ids map[string]struct{}
}

func (l *workflowRunLive) add(id string) {
	l.mu.Lock()
	if l.ids == nil {
		l.ids = map[string]struct{}{}
	}
	l.ids[id] = struct{}{}
	l.mu.Unlock()
}

func (l *workflowRunLive) remove(id string) {
	l.mu.Lock()
	delete(l.ids, id)
	l.mu.Unlock()
}

func (l *workflowRunLive) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	ids := make([]string, 0, len(l.ids))
	for id := range l.ids {
		ids = append(ids, id)
	}
	return ids
}

// runRecorder closes one history row. finish is safe to call more than once;
// only the first call is recorded.
type runRecorder struct {
	finish func(result *workflow.RunResult, err error)
}

// startRunHistory opens a history row for runID. It never fails the run: a
// history write error is logged and the run proceeds unrecorded.
func (s *Server) startRunHistory(ctx context.Context, runID, workflowID, source, triggerID string) runRecorder {
	noop := runRecorder{finish: func(*workflow.RunResult, error) {}}
	recorded, ok := historySources[source]
	if !ok {
		return noop
	}
	store, ok := s.store.(service.WorkflowRunStorer)
	if !ok {
		return noop
	}
	p, _, bound := service.ExecutionFromContext(ctx)
	if !bound {
		return noop
	}
	// Writes outlive the run's own cancellation: a cancelled run is exactly
	// the one whose terminal state must still be written.
	writeCtx := context.WithoutCancel(ctx)
	startCtx, cancel := context.WithTimeout(writeCtx, workflowRunWriteTimeout)
	err := store.StartWorkflowRun(startCtx, service.WorkflowRun{
		ID: runID, WorkspaceID: p.WorkspaceID, WorkflowID: workflowID, OwnerUserID: p.UserID,
		Source: recorded, TriggerID: triggerID,
	})
	cancel()
	if err != nil {
		slog.Warn("workflow run history: start not recorded", "run_id", runID, "workflow_id", workflowID, "error", err)
		return noop
	}
	s.workflowRunsLive.add(runID)

	var once sync.Once
	return runRecorder{finish: func(result *workflow.RunResult, runErr error) {
		once.Do(func() {
			defer s.workflowRunsLive.remove(runID)
			finish := runFinish(ctx, result, runErr)
			finishCtx, cancel := context.WithTimeout(writeCtx, workflowRunWriteTimeout)
			defer cancel()
			if err := store.FinishWorkflowRun(finishCtx, runID, finish); err != nil {
				slog.Warn("workflow run history: finish not recorded", "run_id", runID, "status", finish.Status, "error", err)
			}
		})
	}}
}

func runFinish(ctx context.Context, result *workflow.RunResult, runErr error) service.WorkflowRunFinish {
	finish := service.WorkflowRunFinish{Status: service.WorkflowRunCompleted}
	if result != nil {
		finish.HandledErrors = result.HandledErrors
	}
	if runErr == nil {
		return finish
	}
	finish.Error = runErr.Error()
	finish.FailedNodeID, finish.FailedNodeType = workflow.FailedNode(runErr)
	if errors.Is(runErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		finish.Status = service.WorkflowRunCancelled
	} else {
		finish.Status = service.WorkflowRunFailed
	}
	return finish
}

// startWorkflowRunHistory runs the heartbeat and retention loops.
func (s *Server) startWorkflowRunHistory(ctx context.Context) {
	store, ok := s.store.(service.WorkflowRunStorer)
	if !ok {
		return
	}
	maintenance := service.WithExecutionMaintenance(ctx)
	go func() {
		heartbeat := time.NewTicker(workflowRunHeartbeatInterval)
		sweep := time.NewTicker(workflowRunSweepInterval)
		defer heartbeat.Stop()
		defer sweep.Stop()
		s.sweepWorkflowRunsOnce(maintenance, store)
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				ids := s.workflowRunsLive.snapshot()
				if len(ids) == 0 {
					continue
				}
				hbCtx, cancel := context.WithTimeout(maintenance, workflowRunWriteTimeout)
				if err := store.HeartbeatWorkflowRuns(hbCtx, ids); err != nil && ctx.Err() == nil {
					slog.Warn("workflow run history: heartbeat failed", "runs", len(ids), "error", err)
				}
				cancel()
			case <-sweep.C:
				s.sweepWorkflowRunsOnce(maintenance, store)
			}
		}
	}()
}

func (s *Server) sweepWorkflowRunsOnce(ctx context.Context, store service.WorkflowRunStorer) {
	sweepCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	interrupted, deleted, err := store.SweepWorkflowRuns(sweepCtx, workflowRunStaleAfter, workflowRunKeepCompleted, workflowRunKeepFailed)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("workflow run history: sweep failed", "error", err)
		}
		return
	}
	if interrupted > 0 || deleted > 0 {
		slog.Info("workflow run history: swept", "interrupted", interrupted, "deleted", deleted)
	}
}

// ListWorkflowRunsAPI handles GET /api/v1/workflows/{id}/runs[?status=&limit=].
// Visibility follows saved runs: readers see their own runs; workspace
// administrators see every run of the workflow.
func (s *Server) ListWorkflowRunsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.WorkflowRunStorer)
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	q := service.WorkflowRunQuery{WorkflowID: r.PathValue("id"), Status: r.URL.Query().Get("status")}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			httpResponse(w, "limit must be between 1 and 200", http.StatusBadRequest)
			return
		}
		q.Limit = limit
	}
	runs, err := store.ListWorkflowRuns(r.Context(), q)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrExecutionDenied):
			httpResponse(w, "workflow not found or access denied", http.StatusNotFound)
		case errors.Is(err, service.ErrInvalidWorkflowRunQuery):
			httpResponse(w, err.Error(), http.StatusBadRequest)
		default:
			slog.Error("list workflow runs failed", "workflow_id", q.WorkflowID, "error", err)
			httpResponse(w, "failed to list workflow runs", http.StatusInternalServerError)
		}
		return
	}
	httpResponseJSON(w, map[string]any{"data": runs}, http.StatusOK)
}
