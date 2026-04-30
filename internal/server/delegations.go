package server

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// activeDelegation tracks a single in-flight task delegation goroutine.
type activeDelegation struct {
	TaskID    string             `json:"task_id"`
	AgentID   string             `json:"agent_id"`
	OrgID     string             `json:"org_id"`
	StartedAt time.Time          `json:"started_at"`
	Cancel    context.CancelFunc `json:"-"`
}

// activeDelegationResponse is the JSON-safe representation.
type activeDelegationResponse struct {
	TaskID    string `json:"task_id"`
	AgentID   string `json:"agent_id"`
	OrgID     string `json:"org_id"`
	StartedAt string `json:"started_at"`
	Duration  string `json:"duration"`
}

// registerDelegation creates a cancellable context, registers the delegation
// in activeDelegations, and returns the derived context plus a cleanup
// function that must be deferred.
func (s *Server) registerDelegation(parent context.Context, taskID, agentID, orgID string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)

	deleg := &activeDelegation{
		TaskID:    taskID,
		AgentID:   agentID,
		OrgID:     orgID,
		StartedAt: time.Now(),
		Cancel:    cancel,
	}
	s.activeDelegations.Store(taskID, deleg)

	cleanup := func() {
		s.activeDelegations.Delete(taskID)
		cancel()
	}

	return ctx, cleanup
}

// isDelegationActive returns true if a delegation goroutine is running for
// the given task ID.
func (s *Server) isDelegationActive(taskID string) bool {
	_, ok := s.activeDelegations.Load(taskID)
	return ok
}

// cancelDelegation sends a cancel signal to the delegation goroutine for
// the given task ID and returns true if one was running.
func (s *Server) cancelDelegation(taskID string) bool {
	v, ok := s.activeDelegations.Load(taskID)
	if !ok {
		return false
	}
	deleg, ok := v.(*activeDelegation)
	if !ok || deleg == nil {
		return false
	}
	deleg.Cancel()
	return true
}

// ListActiveDelegationsAPI handles GET /api/v1/active-delegations.
func (s *Server) ListActiveDelegationsAPI(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	var delegations []activeDelegationResponse

	s.activeDelegations.Range(func(_, value any) bool {
		d := value.(*activeDelegation)
		delegations = append(delegations, activeDelegationResponse{
			TaskID:    d.TaskID,
			AgentID:   d.AgentID,
			OrgID:     d.OrgID,
			StartedAt: d.StartedAt.UTC().Format(time.RFC3339),
			Duration:  now.Sub(d.StartedAt).Truncate(time.Second).String(),
		})
		return true
	})

	if delegations == nil {
		delegations = []activeDelegationResponse{}
	}

	httpResponseJSON(w, map[string]any{"delegations": delegations}, http.StatusOK)
}

// CancelTaskDelegationAPI handles POST /api/v1/tasks/{id}/cancel.
// Sends a cancel signal to the delegation goroutine if one is running.
func (s *Server) CancelTaskDelegationAPI(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	if taskID == "" {
		httpResponse(w, "task id is required", http.StatusBadRequest)
		return
	}

	if !s.cancelDelegation(taskID) {
		httpResponse(w, fmt.Sprintf("no active delegation for task %q", taskID), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, map[string]any{
		"message": "cancel signal sent",
		"task_id": taskID,
	}, http.StatusOK)
}
