package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// Cron run history. Every firing of a cron trigger — scheduled or started
// with the Run now button — gets a row and a log. Organization runs stay open
// until their task finishes; agents inside the task append milestones with
// run_log (and telegram_notify, which is mirrored here).

const (
	cronRunWriteTimeout = 10 * time.Second
	cronRunLogMaxBytes  = 1500
)

// cronRunStore returns the store when it supports cron run history.
func (s *Server) cronRunStore() (service.CronRunStorer, bool) {
	store, ok := s.store.(service.CronRunStorer)
	return store, ok
}

// startCronRun opens a run record under the trigger's bound execution
// identity. It never fails the run: a write error is logged and nil returned.
func (s *Server) startCronRun(ctx context.Context, trigger service.Trigger, source, triggeredBy string) *workflow.CronRun {
	store, ok := s.cronRunStore()
	if !ok {
		return nil
	}
	// Writes outlive the tick that opened the run: organization runs are
	// closed when their task ends, long after the cron function returned.
	writeCtx := context.WithoutCancel(ctx)
	startCtx, cancel := context.WithTimeout(writeCtx, cronRunWriteTimeout)
	rec, err := store.StartCronRun(startCtx, service.CronRun{
		TriggerID: trigger.ID, TargetType: trigger.TargetType, TargetID: trigger.TargetID,
		Source: source, TriggeredBy: triggeredBy,
	})
	cancel()
	if err != nil || rec == nil {
		slog.Warn("cron run history: start not recorded", "trigger_id", trigger.ID, "error", err)
		return nil
	}
	id := rec.ID
	write := func(what string, fn func(context.Context) error) {
		c, cancel := context.WithTimeout(writeCtx, cronRunWriteTimeout)
		defer cancel()
		if err := fn(c); err != nil && !errors.Is(err, service.ErrCronRunLogFull) {
			slog.Warn("cron run history: write failed", "run_id", id, "write", what, "error", err)
		}
	}
	var once sync.Once
	run := &workflow.CronRun{
		ID:     id,
		Source: source,
		LinkWorkflowRun: func(runID string) {
			write("link", func(c context.Context) error { return store.LinkCronRun(c, id, "", "", runID) })
		},
		LinkTask: func(taskID, identifier string) {
			write("link", func(c context.Context) error { return store.LinkCronRun(c, id, taskID, identifier, "") })
		},
		Log: func(level, message string) {
			write("log", func(c context.Context) error { return store.AppendCronRunLog(c, id, level, message) })
		},
		Report: func(status, summary string) {
			write("report", func(c context.Context) error { return store.ReportCronRun(c, id, status, summary) })
		},
	}
	run.Finish = func(status, errMsg, result string) {
		once.Do(func() {
			if errMsg != "" {
				run.Log(service.CronRunLogError, errMsg)
			}
			write("finish", func(c context.Context) error { return store.FinishCronRun(c, id, status, errMsg, result) })
		})
	}
	started := "Scheduled run started."
	if source == service.CronRunSourceManual {
		started = "Started manually."
		if triggeredBy != "" {
			started = "Started manually by " + triggeredBy + "."
		}
	}
	run.Log(service.CronRunLogSystem, started)
	return run
}

// cronRunStatusForTask maps a finished task to the run's terminal status.
func cronRunStatusForTask(status string) string {
	switch service.NormalizeTaskStatus(status) {
	case service.TaskStatusDone:
		return service.CronRunCompleted
	case service.TaskStatusBlocked:
		return service.CronRunBlocked
	case service.TaskStatusCancelled:
		return service.CronRunCancelled
	case "failed":
		return service.CronRunFailed
	}
	return service.CronRunCompleted
}

// cronRunIDForTask finds the cron run that launched the task tree containing
// taskID. ctx must carry the task's execution identity.
func (s *Server) cronRunIDForTask(ctx context.Context, taskID string) string {
	store, ok := s.cronRunStore()
	if !ok || taskID == "" {
		return ""
	}
	root := taskID
	if s.taskStore != nil {
		for range 16 {
			task, err := s.taskStore.GetTask(ctx, root)
			if err != nil || task == nil || task.ParentID == "" {
				break
			}
			root = task.ParentID
		}
	}
	id, err := store.CronRunForTask(ctx, root)
	if err != nil {
		return ""
	}
	return id
}

// appendCronRunLogForTask mirrors a milestone into the run log of the cron
// run that launched the task, if any. It reports whether a run was found.
func (s *Server) appendCronRunLogForTask(ctx context.Context, taskID, level, message string) (bool, error) {
	store, ok := s.cronRunStore()
	if !ok {
		return false, nil
	}
	runID := s.cronRunIDForTask(ctx, taskID)
	if runID == "" {
		return false, nil
	}
	return true, store.AppendCronRunLog(ctx, runID, level, message)
}

// execRunLog is the run_log built-in: agents record milestones and, when the
// work is finished, the outcome of the cron run they are working on. The run
// is resolved from the current task or the workflow run, never from
// arguments, so an agent can only write to its own run.
func (s *Server) execRunLog(ctx context.Context, args map[string]any) (string, error) {
	store, ok := s.cronRunStore()
	if !ok {
		return "", fmt.Errorf("run log is not available")
	}
	message, _ := args["message"].(string)
	message = strings.TrimSpace(message)
	status, _ := args["status"].(string)
	status = strings.ToLower(strings.TrimSpace(status))
	if message == "" {
		return "", fmt.Errorf("message is required")
	}
	if status != "" && !service.ValidCronRunReport(status) {
		return "", fmt.Errorf("status must be one of completed, partial, failed")
	}
	if len(message) > cronRunLogMaxBytes {
		message = message[:cronRunLogMaxBytes] + "…"
	}

	runID := ""
	if run, ok := workflow.LookupCronRun(ctx); ok {
		runID = run.ID
	} else if taskID := taskIDFromContext(ctx); taskID != "" {
		runID = s.cronRunIDForTask(ctx, taskID)
	}
	if runID == "" {
		out, _ := json.Marshal(map[string]any{"logged": false, "reason": "this work was not started by a cron schedule; nothing was recorded"})
		return string(out), nil
	}

	level := service.CronRunLogMilestone
	if status != "" {
		level = service.CronRunLogReport
		if err := store.ReportCronRun(ctx, runID, status, message); err != nil {
			return "", fmt.Errorf("report outcome: %w", err)
		}
		message = "[" + status + "] " + message
	}
	if err := store.AppendCronRunLog(ctx, runID, level, message); err != nil {
		if errors.Is(err, service.ErrCronRunLogFull) {
			return "", fmt.Errorf("run log is full (%d entries); report only the final outcome", service.CronRunLogMax)
		}
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"logged": true})
	return string(out), nil
}

// ─── HTTP ───

// RunTriggerNowAPI handles POST /api/v1/triggers/{id}/run: fire a cron
// trigger once now, under its bound execution identity.
func (s *Server) RunTriggerNowAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil || s.scheduler == nil {
		httpResponse(w, "scheduler not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	// Visibility check under the caller's own identity first, so a trigger
	// of another workspace answers 404 rather than running.
	trigger, err := s.triggerStore.GetTrigger(r.Context(), id)
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, "failed to load trigger", http.StatusInternalServerError)
		return
	}
	if trigger == nil {
		httpResponse(w, "trigger not found", http.StatusNotFound)
		return
	}
	if trigger.Type != "cron" {
		httpResponse(w, "only cron triggers can be run from here", http.StatusBadRequest)
		return
	}
	if err := s.scheduler.RunNow(id, s.getUserEmail(r)); err != nil {
		switch {
		case errors.Is(err, service.ErrExecutionDenied):
			httpResponse(w, "this schedule has no usable execution identity: open it and set Run as first", http.StatusConflict)
		case errors.Is(err, workflow.ErrCronUnavailable):
			httpResponse(w, err.Error(), http.StatusServiceUnavailable)
		case errors.Is(err, service.ErrAccessResourceNotFound):
			httpResponse(w, "trigger not found", http.StatusNotFound)
		default:
			slog.Error("run trigger now failed", "trigger_id", id, "error", err)
			httpResponse(w, "failed to start run", http.StatusInternalServerError)
		}
		return
	}
	httpResponse(w, "started", http.StatusAccepted)
}

// ListCronRunsAPI handles GET /api/v1/triggers/{id}/runs[?before=&limit=].
func (s *Server) ListCronRunsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.cronRunStore()
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := store.ListCronRuns(r.Context(), service.CronRunQuery{
		TriggerID: r.PathValue("id"), Before: r.URL.Query().Get("before"), Limit: limit,
	})
	if err != nil {
		if errors.Is(err, service.ErrAccessResourceNotFound) {
			httpResponse(w, "trigger not found", http.StatusNotFound)
			return
		}
		if workspaceBusinessError(w, err) {
			return
		}
		slog.Error("list cron runs failed", "trigger_id", r.PathValue("id"), "error", err)
		httpResponse(w, "failed to list runs", http.StatusInternalServerError)
		return
	}
	s.attachCronRunTaskStatus(r.Context(), runs)
	httpResponseJSON(w, map[string]any{"data": runs}, http.StatusOK)
}

// GetCronRunAPI handles GET /api/v1/cron-runs/{id}.
func (s *Server) GetCronRunAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.cronRunStore()
	if !ok {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	run, err := store.GetCronRun(r.Context(), r.PathValue("id"))
	if err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		slog.Error("get cron run failed", "run_id", r.PathValue("id"), "error", err)
		httpResponse(w, "failed to load run", http.StatusInternalServerError)
		return
	}
	if run == nil {
		httpResponse(w, "run not found", http.StatusNotFound)
		return
	}
	runs := []service.CronRun{*run}
	s.attachCronRunTaskStatus(r.Context(), runs)
	httpResponseJSON(w, runs[0], http.StatusOK)
}

// attachCronRunTaskStatus fills the live status of linked tasks the caller
// can read; tasks it cannot read are left blank.
func (s *Server) attachCronRunTaskStatus(ctx context.Context, runs []service.CronRun) {
	if s.taskStore == nil {
		return
	}
	for i := range runs {
		if runs[i].TaskID == "" {
			continue
		}
		if task, err := s.taskStore.GetTask(ctx, runs[i].TaskID); err == nil && task != nil {
			runs[i].TaskStatus = task.Status
		}
	}
}
