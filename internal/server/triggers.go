package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/logi"

	// Blank import triggers init() registration of all built-in node types.
	_ "github.com/rakunlabs/at/internal/service/workflow/nodes"

	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
)

// ─── Trigger CRUD API ───

// triggersResponse wraps a list of trigger records for JSON output.
type triggersResponse struct {
	Triggers []service.Trigger `json:"triggers"`
}

// ListTriggersAPI handles GET /api/v1/workflows/:wf_id/triggers.
func (s *Server) ListTriggersAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	wfID := r.PathValue("workflow_id")
	if wfID == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	records, err := s.triggerStore.ListTriggers(r.Context(), wfID)
	if err != nil {
		slog.Error("list triggers failed", "workflow_id", wfID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list triggers: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Trigger{}
	}

	httpResponseJSON(w, triggersResponse{Triggers: records}, http.StatusOK)
}

// CreateTriggerAPI handles POST /api/v1/workflows/:wf_id/triggers.
func (s *Server) CreateTriggerAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	wfID := r.PathValue("workflow_id")
	if wfID == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	var req service.Trigger
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Type != "http" && req.Type != "cron" {
		httpResponse(w, "type must be 'http' or 'cron'", http.StatusBadRequest)
		return
	}

	if req.Type == "cron" {
		schedule, _ := req.Config["schedule"].(string)
		if schedule == "" {
			httpResponse(w, "cron trigger requires 'schedule' in config", http.StatusBadRequest)
			return
		}
	}

	userEmail := s.getUserEmail(r)

	// Validate alias uniqueness.
	if req.Alias != "" {
		existing, err := s.triggerStore.GetTriggerByAlias(r.Context(), req.Alias)
		if err != nil {
			slog.Error("check alias uniqueness failed", "alias", req.Alias, "error", err)
			httpResponse(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing != nil {
			httpResponse(w, fmt.Sprintf("alias %q is already in use", req.Alias), http.StatusConflict)
			return
		}
	}

	req.WorkflowID = wfID
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.triggerStore.CreateTrigger(r.Context(), req)
	if err != nil {
		slog.Error("create trigger failed", "workflow_id", wfID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create trigger: %v", err), http.StatusInternalServerError)
		return
	}

	// If it's a cron trigger, reload the scheduler.
	if req.Type == "cron" && req.Enabled && s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Error("scheduler reload failed after trigger create", "error", err)
		}
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// GetTriggerAPI handles GET /api/v1/triggers/:id.
func (s *Server) GetTriggerAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "trigger id is required", http.StatusBadRequest)
		return
	}

	record, err := s.triggerStore.GetTrigger(r.Context(), id)
	if err != nil {
		slog.Error("get trigger failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get trigger: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("trigger %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// UpdateTriggerAPI handles PUT /api/v1/triggers/:id.
func (s *Server) UpdateTriggerAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "trigger id is required", http.StatusBadRequest)
		return
	}

	var req service.Trigger
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Type != "" && req.Type != "http" && req.Type != "cron" {
		httpResponse(w, "type must be 'http' or 'cron'", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)

	// Validate alias uniqueness (if alias is being set/changed).
	if req.Alias != "" {
		existing, err := s.triggerStore.GetTriggerByAlias(r.Context(), req.Alias)
		if err != nil {
			slog.Error("check alias uniqueness failed", "alias", req.Alias, "error", err)
			httpResponse(w, "internal error", http.StatusInternalServerError)
			return
		}
		if existing != nil && existing.ID != id {
			httpResponse(w, fmt.Sprintf("alias %q is already in use", req.Alias), http.StatusConflict)
			return
		}
	}

	req.UpdatedBy = userEmail
	record, err := s.triggerStore.UpdateTrigger(r.Context(), id, req)
	if err != nil {
		slog.Error("update trigger failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update trigger: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("trigger %q not found", id), http.StatusNotFound)
		return
	}

	// Reload scheduler — the trigger's type, schedule, or enabled status may have changed.
	if s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Error("scheduler reload failed after trigger update", "error", err)
		}
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteTriggerAPI handles DELETE /api/v1/triggers/:id.
func (s *Server) DeleteTriggerAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "trigger id is required", http.StatusBadRequest)
		return
	}

	// Check if the trigger exists and is a cron trigger (for scheduler reload).
	existing, _ := s.triggerStore.GetTrigger(r.Context(), id)

	if err := s.triggerStore.DeleteTrigger(r.Context(), id); err != nil {
		slog.Error("delete trigger failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete trigger: %v", err), http.StatusInternalServerError)
		return
	}

	// Reload scheduler if we deleted a cron trigger.
	if existing != nil && existing.Type == "cron" && s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Error("scheduler reload failed after trigger delete", "error", err)
		}
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Webhook Handler ───

// WebhookAPI handles POST /webhooks/:trigger_id_or_alias.
// It looks up the HTTP trigger by ID or alias, verifies it is enabled,
// enforces authentication for non-public triggers, loads the associated
// workflow, and starts execution. By default runs asynchronously (202).
// Pass ?sync=true to block until the workflow completes and return outputs.
func (s *Server) WebhookAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil || s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	idOrAlias := r.PathValue("id")
	if idOrAlias == "" {
		httpResponse(w, "trigger id or alias is required", http.StatusBadRequest)
		return
	}

	// Try by ID first, then by alias.
	trigger, err := s.triggerStore.GetTrigger(r.Context(), idOrAlias)
	if err != nil {
		slog.Error("webhook: get trigger failed", "id_or_alias", idOrAlias, "error", err)
		httpResponse(w, "internal error", http.StatusInternalServerError)
		return
	}

	if trigger == nil {
		trigger, err = s.triggerStore.GetTriggerByAlias(r.Context(), idOrAlias)
		if err != nil {
			slog.Error("webhook: get trigger by alias failed", "alias", idOrAlias, "error", err)
			httpResponse(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	if trigger == nil {
		httpResponse(w, "webhook not found", http.StatusNotFound)
		return
	}

	if trigger.Type != "http" {
		httpResponse(w, "trigger is not an HTTP trigger", http.StatusBadRequest)
		return
	}

	if !trigger.Enabled {
		httpResponse(w, "trigger is disabled", http.StatusForbidden)
		return
	}

	// Enforce authentication for non-public triggers.
	if !trigger.Public {
		auth, reason := s.authenticateRequest(r)
		if auth == nil {
			httpResponse(w, "unauthorized: "+reason, http.StatusUnauthorized)
			return
		}

		// Check webhook scoping: if the token restricts webhooks,
		// verify this trigger's ID or alias is in the allowed list.
		if auth.token != nil && len(auth.token.AllowedWebhooks) > 0 {
			allowed := false
			for _, w := range auth.token.AllowedWebhooks {
				if w == trigger.ID || (trigger.Alias != "" && w == trigger.Alias) {
					allowed = true
					break
				}
			}
			if !allowed {
				httpResponse(w, "token does not have access to this webhook", http.StatusForbidden)
				return
			}
		}
	}

	// Load the workflow.
	wf, err := s.workflowStore.GetWorkflow(r.Context(), trigger.WorkflowID)
	if err != nil {
		slog.Error("webhook: get workflow failed",
			"trigger_id", trigger.ID,
			"workflow_id", trigger.WorkflowID,
			"error", err)
		httpResponse(w, "internal error", http.StatusInternalServerError)
		return
	}

	if wf == nil {
		httpResponse(w, "associated workflow not found", http.StatusNotFound)
		return
	}

	// Use the active version's graph if available.
	graphToRun := wf.Graph
	if wf.ActiveVersion != nil && s.workflowVersionStore != nil {
		ver, err := s.workflowVersionStore.GetWorkflowVersion(r.Context(), trigger.WorkflowID, *wf.ActiveVersion)
		if err != nil {
			slog.Error("webhook: get active version failed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"version", *wf.ActiveVersion,
				"error", err)
			// Fall back to wf.Graph on error.
		} else if ver != nil {
			graphToRun = ver.Graph
		}
	}

	// Buffer the request body before returning the response, since r.Body
	// will be closed once the handler returns.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("webhook: read body failed", "trigger_id", trigger.ID, "error", err)
		httpResponse(w, "failed to read request body", http.StatusBadRequest)
		return
	}

	// Build structured input data from the HTTP request.
	inputs := map[string]any{
		"method":       r.Method,
		"path":         r.URL.Path,
		"trigger_type": "http",
		"trigger_id":   trigger.ID,
		"triggered_at": time.Now().UTC().Format(time.RFC3339),
	}

	// Query parameters (first value per key).
	query := make(map[string]string, len(r.URL.Query()))
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			query[k] = v[0]
		}
	}
	inputs["query"] = query

	// Request headers (first value per key).
	headers := make(map[string]string, len(r.Header))
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}
	inputs["headers"] = headers

	// Pass buffered body as an io.ReadCloser so downstream BodyWrapper works.
	inputs["body"] = io.NopCloser(bytes.NewReader(bodyBytes))

	syncMode := r.URL.Query().Get("sync") == "true"

	// Both sync and async modes run the engine in a goroutine that outlives
	// the HTTP request. Use context.Background() so the request context
	// cancellation does not kill background graph execution.
	parentCtx := context.Background()

	// Enrich context with workflow metadata for structured logging.
	requestID := r.Header.Get(mrequestid.HeaderXRequestID)
	parentCtx = logi.WithContext(parentCtx, slog.With(
		slog.String("workflow_id", trigger.WorkflowID),
		slog.String("workflow_name", wf.Name),
		slog.String("request_id", requestID),
		slog.String("user", s.getUserEmail(r)),
	))

	// Register the run and get a cancellable context.
	runID, ctx, cleanup := s.registerRun(parentCtx, trigger.WorkflowID, "webhook")

	// Build a provider lookup function for the engine.
	providerLookup := func(key string) (service.LLMProvider, string, error) {
		s.providerMu.RLock()
		info, ok := s.providers[key]
		s.providerMu.RUnlock()
		if !ok {
			return nil, "", fmt.Errorf("provider %q not found", key)
		}
		return info.provider, info.defaultModel, nil
	}

	// Build a skill lookup function for agent_call nodes.
	var skillLookup workflow.SkillLookup
	if s.skillStore != nil {
		skillLookup = func(nameOrID string) (*service.Skill, error) {
			sk, err := s.skillStore.GetSkill(ctx, nameOrID)
			if err != nil {
				return nil, err
			}
			if sk != nil {
				return sk, nil
			}
			return s.skillStore.GetSkillByName(ctx, nameOrID)
		}
	}

	// Build a variable lookup function for getVar() in Goja JS.
	var varLookup workflow.VarLookup
	var varLister workflow.VarLister
	if s.variableStore != nil {
		varLookup = func(key string) (string, error) {
			v, err := s.variableStore.GetVariableByKey(ctx, key)
			if err != nil {
				return "", err
			}
			if v == nil {
				return "", fmt.Errorf("variable %q not found", key)
			}
			return v.Value, nil
		}
		varLister = func() (map[string]string, error) {
			vars, err := s.variableStore.ListVariables(ctx)
			if err != nil {
				return nil, err
			}
			m := make(map[string]string, len(vars))
			for _, v := range vars {
				m[v.Key] = v.Value
			}
			return m, nil
		}
	}

	// Build a node config lookup function for nodes that reference external configs.
	var nodeConfigLookup workflow.NodeConfigLookup
	if s.nodeConfigStore != nil {
		nodeConfigLookup = func(id string) (*service.NodeConfig, error) {
			return s.nodeConfigStore.GetNodeConfig(ctx, id)
		}
	}

	// Build a workflow lookup function for workflow_call nodes.
	var workflowLookup workflow.WorkflowLookup
	if s.workflowStore != nil {
		workflowLookup = func(ctx context.Context, id string) (*service.Workflow, error) {
			return s.workflowStore.GetWorkflow(ctx, id)
		}
	}

	engine := workflow.NewEngine(providerLookup, skillLookup, varLookup, varLister, nodeConfigLookup, workflowLookup)

	// Find the specific http_trigger node that matches this trigger's ID.
	var entryNodeIDs []string
	hasOutputNode := false
	for _, n := range graphToRun.Nodes {
		if n.Type == "http_trigger" {
			if tid, _ := n.Data["trigger_id"].(string); tid == trigger.ID {
				entryNodeIDs = append(entryNodeIDs, n.ID)
			}
		}
		if n.Type == "output" {
			hasOutputNode = true
		}
	}

	if syncMode && hasOutputNode {
		// Synchronous with output node: run the engine in a goroutine and
		// wait for the first output node to fire. The rest of the graph
		// continues in the background.
		outputCh := make(chan workflow.EarlyOutput, 1)

		go func() {
			defer cleanup()

			logi.Ctx(ctx).Info("webhook: workflow started",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"run_id", runID)
			result, err := engine.Run(ctx, graphToRun, inputs, entryNodeIDs, outputCh)
			if err != nil {
				logi.Ctx(ctx).Error("webhook: workflow execution failed",
					"trigger_id", trigger.ID,
					"workflow_id", trigger.WorkflowID,
					"run_id", runID,
					"error", err)
				return
			}

			logi.Ctx(ctx).Info("webhook: workflow completed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"run_id", runID,
				"output_keys", mapKeys(result.Outputs))
		}()

		early := <-outputCh
		if early.Err != nil {
			logi.Ctx(ctx).Error("webhook: workflow execution failed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"run_id", runID,
				"error", early.Err)
			httpResponse(w, fmt.Sprintf("workflow execution failed: %v", early.Err), http.StatusInternalServerError)
			return
		}

		httpResponseJSON(w, runWorkflowResponse{
			RunID:      runID,
			WorkflowID: trigger.WorkflowID,
			Status:     "completed",
			Outputs:    early.Outputs,
		}, http.StatusOK)
	} else {
		// Asynchronous (or sync without output node): run in goroutine,
		// return immediately. Nothing to wait for.
		go func() {
			defer cleanup()

			logi.Ctx(ctx).Info("webhook: workflow started",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"run_id", runID)
			result, err := engine.Run(ctx, graphToRun, inputs, entryNodeIDs, nil)
			if err != nil {
				logi.Ctx(ctx).Error("webhook: workflow execution failed",
					"trigger_id", trigger.ID,
					"workflow_id", trigger.WorkflowID,
					"run_id", runID,
					"error", err)
				return
			}

			logi.Ctx(ctx).Info("webhook: workflow completed",
				"trigger_id", trigger.ID,
				"workflow_id", trigger.WorkflowID,
				"run_id", runID,
				"output_keys", mapKeys(result.Outputs))
		}()

		httpResponseJSON(w, runWorkflowResponse{
			RunID:      runID,
			WorkflowID: trigger.WorkflowID,
			Status:     "running",
		}, http.StatusAccepted)
	}
}
