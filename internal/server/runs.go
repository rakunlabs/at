package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// activeRun tracks a single in-flight workflow execution.
type activeRun struct {
	ID         string             `json:"id"`
	WorkflowID string             `json:"workflow_id"`
	Source     string             `json:"source"` // "api", "webhook", "cron"
	StartedAt  time.Time          `json:"started_at"`
	Cancel     context.CancelFunc `json:"-"`
}

// activeRunResponse is the JSON-safe representation of an active run.
type activeRunResponse struct {
	ID         string `json:"id"`
	WorkflowID string `json:"workflow_id"`
	Source     string `json:"source"`
	StartedAt  string `json:"started_at"`
	Duration   string `json:"duration"`
}

// activeRunsResponse wraps a list of active runs for JSON output.
type activeRunsResponse struct {
	Runs []activeRunResponse `json:"runs"`
}

// registerRun creates a cancellable context, registers the run, and returns
// the run ID, derived context, and a cleanup function that must be deferred.
func (s *Server) registerRun(parent context.Context, workflowID, source string) (string, context.Context, func()) {
	runID := "run_" + ulid.Make().String()
	ctx, cancel := context.WithCancel(parent)

	run := &activeRun{
		ID:         runID,
		WorkflowID: workflowID,
		Source:     source,
		StartedAt:  time.Now(),
		Cancel:     cancel,
	}
	s.activeRuns.Store(runID, run)

	cleanup := func() {
		s.activeRuns.Delete(runID)
		cancel()
	}

	return runID, ctx, cleanup
}

// ListActiveRunsAPI handles GET /api/v1/runs.
func (s *Server) ListActiveRunsAPI(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	var runs []activeRunResponse

	s.activeRuns.Range(func(key, value any) bool {
		run := value.(*activeRun)
		runs = append(runs, activeRunResponse{
			ID:         run.ID,
			WorkflowID: run.WorkflowID,
			Source:     run.Source,
			StartedAt:  run.StartedAt.UTC().Format(time.RFC3339),
			Duration:   now.Sub(run.StartedAt).Truncate(time.Second).String(),
		})
		return true
	})

	if runs == nil {
		runs = []activeRunResponse{}
	}

	httpResponseJSON(w, activeRunsResponse{Runs: runs}, http.StatusOK)
}

// CancelRunAPI handles POST /api/v1/runs/:run_id/cancel.
func (s *Server) CancelRunAPI(w http.ResponseWriter, r *http.Request) {
	runID := extractRunID(r)
	if runID == "" {
		httpResponse(w, "run id is required", http.StatusBadRequest)
		return
	}

	val, ok := s.activeRuns.Load(runID)
	if !ok {
		httpResponse(w, fmt.Sprintf("run %q not found or already completed", runID), http.StatusNotFound)
		return
	}

	run := val.(*activeRun)
	run.Cancel()

	httpResponseJSON(w, map[string]any{
		"message": "cancel signal sent",
		"run_id":  runID,
	}, http.StatusOK)
}

// extractRunID extracts the run ID from cancel URLs.
// Expected path: /api/v1/runs/{run_id}/cancel
func extractRunID(r *http.Request) string {
	path := r.URL.Path
	const prefix = "/api/v1/runs/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}

	rest := strings.TrimPrefix(path, prefix)
	rest = strings.TrimSuffix(rest, "/cancel")
	rest = strings.TrimSuffix(rest, "/")

	return rest
}
