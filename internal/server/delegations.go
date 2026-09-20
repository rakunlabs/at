package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

var errDelegationAlreadyRunning = errors.New("delegation already running")

// activeDelegation tracks a single in-flight task delegation goroutine.
type activeDelegation struct {
	TaskID    string             `json:"task_id"`
	AgentID   string             `json:"agent_id"`
	OrgID     string             `json:"org_id"`
	StartedAt time.Time          `json:"started_at"`
	Cancel    context.CancelFunc `json:"-"`
	ctx       context.Context
}

type activeDelegationContextKey struct{}

// activeDelegationResponse is the JSON-safe representation.
type activeDelegationResponse struct {
	TaskID    string `json:"task_id"`
	AgentID   string `json:"agent_id"`
	OrgID     string `json:"org_id"`
	StartedAt string `json:"started_at"`
	Duration  string `json:"duration"`
}

func (s *Server) tryRegisterDelegation(parent context.Context, taskID, agentID, orgID string) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(parent)

	deleg := &activeDelegation{
		TaskID:    taskID,
		AgentID:   agentID,
		OrgID:     orgID,
		StartedAt: time.Now(),
		Cancel:    cancel,
		ctx:       ctx,
	}
	if _, loaded := s.activeDelegations.LoadOrStore(taskID, deleg); loaded {
		cancel()
		return nil, nil, fmt.Errorf("%w for task %q", errDelegationAlreadyRunning, taskID)
	}
	ctx = context.WithValue(ctx, activeDelegationContextKey{}, deleg)

	cleanup := func() {
		s.activeDelegations.CompareAndDelete(taskID, deleg)
		cancel()
	}

	return ctx, cleanup, nil
}

func (s *Server) contextOwnsDelegation(ctx context.Context, taskID string) bool {
	deleg, ok := ctx.Value(activeDelegationContextKey{}).(*activeDelegation)
	if !ok || deleg == nil || deleg.TaskID != taskID {
		return false
	}
	active, ok := s.activeDelegations.Load(taskID)
	return ok && active == deleg
}

type delegationRunDoneFunc func(context.Context, error)

type delegationRunReservation struct {
	ctx     context.Context
	cleanup func()
}

// reserveDelegationRun claims a task without launching it. The caller must
// either release the reservation or pass it to startReservedDelegationRun.
func (s *Server) reserveDelegationRun(parent context.Context, taskID, agentID, orgID string) (*delegationRunReservation, error) {
	// New always sets Server.ctx. The fallback keeps manually constructed test
	// servers safe without reintroducing Background use at production callers.
	if parent == nil {
		parent = context.Background()
	}

	ctx, cleanup, err := s.tryRegisterDelegation(parent, taskID, agentID, orgID)
	if err != nil {
		return nil, err
	}
	// A detached request retains its runtime identity but must still stop when
	// this server shuts down (including bot callers using WithoutCancel).
	if s.ctx != nil {
		deleg := ctx.Value(activeDelegationContextKey{}).(*activeDelegation)
		stop := context.AfterFunc(s.ctx, deleg.Cancel)
		previousCleanup := cleanup
		cleanup = func() { stop(); previousCleanup() }
	}
	if owner, ok := parent.Value(activeDelegationContextKey{}).(*activeDelegation); ok && owner.ctx != nil && owner.TaskID != taskID {
		deleg := ctx.Value(activeDelegationContextKey{}).(*activeDelegation)
		stop := context.AfterFunc(owner.ctx, deleg.Cancel)
		previousCleanup := cleanup
		cleanup = func() { stop(); previousCleanup() }
	}
	if orgTraceIDFromContext(ctx) == "" {
		ctx = contextWithOrgTraceID(ctx, ulid.Make().String())
	}
	return &delegationRunReservation{ctx: ctx, cleanup: cleanup}, nil
}

// startDelegationRun atomically reserves a task and owns the common lifecycle
// for an asynchronous root delegation. parent should be the server lifecycle
// context so shutdown cancels the run.
func (s *Server) startDelegationRun(
	parent context.Context,
	org *service.Organization,
	task *service.Task,
	agentID string,
	depth int,
	onDone delegationRunDoneFunc,
) error {
	reservation, err := s.reserveDelegationRun(parent, task.ID, agentID, org.ID)
	if err != nil {
		return err
	}
	s.startReservedDelegationRun(reservation, org, task, agentID, depth, onDone)
	return nil
}

// startReservedDelegationRun consumes a reservation and launches its run.
func (s *Server) startReservedDelegationRun(
	reservation *delegationRunReservation,
	org *service.Organization,
	task *service.Task,
	agentID string,
	depth int,
	onDone delegationRunDoneFunc,
) {
	go func() {
		defer reservation.cleanup()

		runErr := s.runOrgDelegation(reservation.ctx, org, task, agentID, depth)
		postRunCtx, cancel := context.WithTimeout(context.WithoutCancel(reservation.ctx), 30*time.Second)
		defer cancel()
		if runErr != nil {
			slog.Error("org-delegation: background run failed",
				"org_id", org.ID,
				"task_id", task.ID,
				"agent_id", agentID,
				"error", runErr,
			)
			s.persistDelegationFailure(reservation.ctx, task, runErr)
		}
		if onDone != nil {
			onDone(postRunCtx, runErr)
		}
	}()
}

// Preserve cancellation semantics, but distinguish a failed run from a user's
// Stop. Use a live bounded context so cancelled children do not remain running.
func (s *Server) persistDelegationFailure(ctx context.Context, task *service.Task, runErr error) {
	if s.taskStore == nil {
		return
	}
	status := service.TaskStatusBlocked
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) {
		status = service.TaskStatusCancelled
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := s.taskStore.UpdateTaskStatus(writeCtx, task.ID, status, fmt.Sprintf("delegation failed: %v", runErr)); err != nil {
		slog.Error("persist delegation failure failed", "task_id", task.ID, "error", err)
	}
}

func waitDelegationRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
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
