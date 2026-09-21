package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func (s *Server) enqueueDurableWorkflow(ctx context.Context, workflowID string, graph service.WorkflowGraph, inputs map[string]any, entries []string, source string) (*service.WorkflowExecution, error) {
	store, ok := s.store.(service.WorkflowExecutionStorer)
	if !ok {
		return nil, fmt.Errorf("durable workflow store not configured")
	}
	if err := workflow.ValidateDurableGraph(graph, entries); err != nil {
		return nil, err
	}
	if err := workflow.PortableJSON(inputs); err != nil {
		return nil, err
	}
	id := ulid.Make().String()
	ctx, err := service.DeriveExecution(ctx, id, "durable_workflow")
	if err != nil {
		return nil, err
	}
	p, _, _ := service.ExecutionFromContext(ctx)
	if p.SessionID == "" && p.ServiceID == "" {
		return nil, fmt.Errorf("durable workflows require a resumable account session or execution service binding")
	}
	if err := s.persistRuntimeRun(ctx); err != nil {
		return nil, err
	}
	value, err := store.CreateWorkflowExecution(ctx, service.WorkflowExecution{ID: id, WorkflowID: workflowID, WorkspaceID: p.WorkspaceID, OwnerUserID: p.UserID, Payload: service.WorkflowExecutionPayload{Source: source, Graph: graph, Inputs: inputs, EntryNodeIDs: entries}})
	if err != nil {
		return nil, err
	}
	select {
	case s.durableWake <- struct{}{}:
	default:
	}
	return value, nil
}

func durableSummary(value *service.WorkflowExecution) map[string]any {
	return map[string]any{"id": value.ID, "workflow_id": value.WorkflowID, "owner_user_id": value.OwnerUserID, "source": value.Payload.Source, "status": value.Status, "revision": value.Revision,
		"wait_node_id": value.WaitNodeID, "wait_mode": value.WaitMode, "wait_prompt": value.WaitPrompt, "wake_at": value.WakeAt, "expires_at": value.ExpiresAt,
		"decision_by": value.DecisionBy, "error": value.Error, "created_at": value.CreatedAt, "updated_at": value.UpdatedAt}
}

func durableSummaryForRequest(ctx context.Context, value *service.WorkflowExecution) map[string]any {
	result := durableSummary(value)
	a, ok := service.AccessPrincipalFromContext(ctx)
	result["can_decide"] = ok && a.Allows("workflows.execute", service.AccessResource{WorkspaceID: value.WorkspaceID, ID: value.WorkflowID}) && (a.UserID == value.OwnerUserID || a.PlatformAdmin || service.WorkspaceRoleRank(a.Role) >= 3)
	return result
}

func (s *Server) ListWorkflowExecutionsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.WorkflowExecutionStorer)
	if !ok {
		httpResponse(w, "durable workflow store not configured", 503)
		return
	}
	items, err := store.ListWorkflowExecutions(r.Context(), r.PathValue("id"))
	if err != nil {
		durableAPIError(w, err)
		return
	}
	data := make([]map[string]any, 0, len(items))
	for i := range items {
		data = append(data, durableSummaryForRequest(r.Context(), &items[i]))
	}
	httpResponseJSON(w, map[string]any{"data": data}, 200)
}

func (s *Server) GetWorkflowExecutionAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(service.WorkflowExecutionStorer)
	if !ok {
		httpResponse(w, "durable workflow store not configured", 503)
		return
	}
	item, err := store.GetWorkflowExecution(r.Context(), r.PathValue("id"), r.PathValue("execution"))
	if err != nil {
		durableAPIError(w, err)
		return
	}
	if item == nil {
		httpResponse(w, "execution not found", 404)
		return
	}
	result := durableSummaryForRequest(r.Context(), item)
	result["outputs"] = item.Checkpoint.Outputs
	result["in_flight"] = item.Checkpoint.InFlight
	if item.Checkpoint.Waiting != nil {
		result["waiting_data"] = item.Checkpoint.Waiting.Data
	}
	httpResponseJSON(w, result, 200)
}

func (s *Server) DecideWorkflowExecutionAPI(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("action")
	if action != "approve" && action != "reject" && action != "cancel" {
		httpResponse(w, "unknown execution action", 400)
		return
	}
	store, ok := s.store.(service.WorkflowExecutionStorer)
	if !ok {
		httpResponse(w, "durable workflow store not configured", 503)
		return
	}
	item, err := store.GetWorkflowExecution(r.Context(), r.PathValue("id"), r.PathValue("execution"))
	if err != nil {
		durableAPIError(w, err)
		return
	}
	if item == nil {
		httpResponse(w, "execution not found", 404)
		return
	}
	var req struct {
		Revision *int64 `json:"revision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Revision == nil {
		httpResponse(w, "revision is required", 400)
		return
	}
	if err := store.DecideWorkflowExecution(r.Context(), item.WorkflowID, item.ID, *req.Revision, r.PathValue("action")); err != nil {
		durableAPIError(w, err)
		return
	}
	actor, _ := service.AccessPrincipalFromContext(r.Context())
	slog.Info("workflow execution decision", "execution_id", item.ID, "actor_id", actor.UserID, "action", r.PathValue("action"), "revision", *req.Revision)
	select {
	case s.durableWake <- struct{}{}:
	default:
	}
	httpResponseJSON(w, map[string]any{"status": "accepted"}, 200)
}

func durableAPIError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrExecutionDenied):
		httpResponse(w, "execution not found or access denied", 404)
	case errors.Is(err, service.ErrWorkflowExecutionConflict):
		httpResponse(w, "Execution changed, expired or was already handled. Refresh its status.", 409)
	default:
		slog.Error("durable workflow API failed", "error", err)
		httpResponse(w, "durable workflow operation failed", 500)
	}
}

func (s *Server) startDurableWorkflows(ctx context.Context) {
	store, ok := s.store.(service.WorkflowExecutionStorer)
	if !ok {
		return
	}
	s.durableWake = make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		worker := ulid.Make().String()
		go func() {
			maintenance := service.WithExecutionMaintenance(ctx)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				sources := []string{"__disabled__"}
				for source, feature := range map[string]string{"api": service.FeatureWorkflowBuilder, "cron": service.FeatureCronTriggers, "webhook": service.FeatureWebhookTriggers} {
					if enabled, err := s.isFeatureEnabled(ctx, feature); err == nil && enabled {
						sources = append(sources, source)
					}
				}
				job, err := store.ClaimWorkflowExecution(maintenance, worker, sources...)
				if err != nil && ctx.Err() == nil {
					slog.Error("claim durable workflow", "error", err)
				}
				if job != nil {
					s.runDurableWorkflow(maintenance, store, job, worker)
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				case <-s.durableWake:
				}
			}
		}()
	}
}

func (s *Server) durableRuntime(ctx context.Context, job *service.WorkflowExecution) (context.Context, error) {
	store, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	p, err := store.GetExecutionProvenance(ctx, job.ID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.Source != "durable_workflow" || p.WorkspaceID != job.WorkspaceID || p.UserID != job.OwnerUserID {
		return nil, service.ErrExecutionDenied
	}
	ctx = service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: p.UserID, WorkspaceID: p.WorkspaceID, SessionID: p.SessionID, MembershipVersion: p.MembershipVersion})
	if p.ServiceID == "" {
		ctx, err = s.revalidateAccessPrincipal(ctx)
	} else if workspaces, ok := s.store.(service.WorkspaceStorer); ok {
		var live service.AccessPrincipal
		live, _, err = workspaces.ResolveWorkspaceAccess(ctx, p.WorkspaceID, p.UserID, "")
		ctx = service.WithAccessPrincipal(ctx, live)
	} else {
		return nil, service.ErrExecutionDenied
	}
	if err != nil {
		return nil, err
	}
	feature := service.FeatureWorkflowBuilder
	if job.Payload.Source == "cron" {
		feature = service.FeatureCronTriggers
	}
	if job.Payload.Source == "webhook" {
		feature = service.FeatureWebhookTriggers
	}
	validate := func(ctx context.Context, who service.ExecutionProvenance, action service.ExecutionAction) (service.ExecutionValidation, error) {
		enabled, err := s.isFeatureEnabled(ctx, feature)
		if err != nil {
			return service.ExecutionValidation{}, err
		}
		if !enabled {
			return service.ExecutionValidation{}, fmt.Errorf("workflow execution feature %s is disabled: %w", feature, service.ErrExecutionDenied)
		}
		return s.revalidateRuntimeExecution(ctx, who, action)
	}
	ctx, err = s.BindRuntimeExecution(ctx, *p, validate)
	if err != nil {
		return nil, err
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: job.WorkflowID}); err != nil {
		return nil, err
	}
	return ctx, nil
}

func (s *Server) runDurableWorkflow(maintenance context.Context, store service.WorkflowExecutionStorer, job *service.WorkflowExecution, worker string) {
	ctx, cancel := context.WithCancel(maintenance)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				renewCtx, renewCancel := context.WithTimeout(maintenance, 10*time.Second)
				err := store.RenewWorkflowExecutionLease(renewCtx, job.ID, worker)
				renewCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	runCtx, err := s.durableRuntime(ctx, job)
	revision := job.Revision
	last := job.Checkpoint
	if err == nil {
		runCtx = logi.WithContext(runCtx, logi.Ctx(runCtx).With("execution_id", job.ID, "workflow_id", job.WorkflowID))
		engine := s.buildWorkflowEngine(runCtx)
		_, err = engine.RunDurable(runCtx, job.Payload.Graph, job.Payload.Inputs, job.Payload.EntryNodeIDs, job.Checkpoint, job.ResumeRequested, func(checkpoint service.WorkflowCheckpoint, status string) error {
			next, err := store.SaveWorkflowExecutionCheckpoint(maintenance, job.ID, worker, revision, status, checkpoint, "")
			if err != nil {
				return err
			}
			revision = next
			// Detach the acknowledged state from the engine's mutable maps.
			encoded, _ := json.Marshal(checkpoint)
			var acknowledged service.WorkflowCheckpoint
			_ = json.Unmarshal(encoded, &acknowledged)
			last = acknowledged
			if status == "waiting" || status == "completed" {
				slog.Info("durable workflow checkpoint", "execution_id", job.ID, "workflow_id", job.WorkflowID, "status", status)
			}
			return nil
		})
	}
	if err != nil {
		slog.Error("durable workflow segment stopped", "execution_id", job.ID, "workflow_id", job.WorkflowID, "error", err)
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(maintenance), 5*time.Second)
		defer finishCancel()
		if _, saveErr := store.SaveWorkflowExecutionCheckpoint(finishCtx, job.ID, worker, revision, "blocked", last, err.Error()); saveErr != nil && !errors.Is(saveErr, service.ErrWorkflowExecutionConflict) {
			slog.Error("persist blocked workflow", "execution_id", job.ID, "error", saveErr)
		}
	}
}
