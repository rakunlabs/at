package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/logi"

	// Blank import triggers init() registration of all built-in node types.
	_ "github.com/rakunlabs/at/internal/service/workflow/nodes"

	mrequestid "github.com/rakunlabs/ada/middleware/requestid"
)

// ─── Workflow CRUD API ───

// workflowsResponse wraps a list of workflow records for JSON output.
type workflowsResponse struct {
	Workflows []service.Workflow `json:"workflows"`
}

// ListWorkflowsAPI handles GET /api/v1/workflows.
func (s *Server) ListWorkflowsAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	records, err := s.workflowStore.ListWorkflows(r.Context())
	if err != nil {
		slog.Error("list workflows failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list workflows: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Workflow{}
	}

	httpResponseJSON(w, workflowsResponse{Workflows: records}, http.StatusOK)
}

// GetWorkflowAPI handles GET /api/v1/workflows/:id.
func (s *Server) GetWorkflowAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	record, err := s.workflowStore.GetWorkflow(r.Context(), id)
	if err != nil {
		slog.Error("get workflow failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get workflow: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("workflow %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateWorkflowAPI handles POST /api/v1/workflows.
func (s *Server) CreateWorkflowAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Workflow
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail
	record, err := s.workflowStore.CreateWorkflow(r.Context(), req)
	if err != nil {
		slog.Error("create workflow failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create workflow: %v", err), http.StatusInternalServerError)
		return
	}

	// Sync triggers: create DB trigger records for any trigger nodes in the graph.
	if s.triggerStore != nil {
		cronChanged, err := s.syncTriggers(r.Context(), record.ID, &record.Graph, userEmail)
		if err != nil {
			slog.Error("sync triggers failed after create", "id", record.ID, "error", err)
			// Non-fatal: workflow was created, triggers just didn't sync.
		} else if s.hasTriggerNodes(record.Graph) {
			// Persist the graph with trigger IDs written back into node data.
			record.UpdatedBy = userEmail
			record, err = s.workflowStore.UpdateWorkflow(r.Context(), record.ID, *record)
			if err != nil {
				slog.Error("update workflow after trigger sync failed", "id", record.ID, "error", err)
			}
		}

		if cronChanged && s.scheduler != nil {
			if err := s.scheduler.Reload(); err != nil {
				slog.Error("scheduler reload failed after workflow create", "error", err)
			}
		}
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateWorkflowAPI handles PUT /api/v1/workflows/:id.
func (s *Server) UpdateWorkflowAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	var req service.Workflow
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	// Sync triggers before saving: creates/updates/deletes DB trigger records
	// based on trigger nodes in the graph, and writes trigger_id back into
	// node data so the saved graph contains the assigned IDs.
	var cronChanged bool
	if s.triggerStore != nil {
		var err error
		cronChanged, err = s.syncTriggers(r.Context(), id, &req.Graph, userEmail)
		if err != nil {
			slog.Error("sync triggers failed", "id", id, "error", err)
			httpResponse(w, fmt.Sprintf("failed to sync triggers: %v", err), http.StatusInternalServerError)
			return
		}
	}

	record, err := s.workflowStore.UpdateWorkflow(r.Context(), id, req)
	if err != nil {
		slog.Error("update workflow failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update workflow: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("workflow %q not found", id), http.StatusNotFound)
		return
	}

	// Auto-create a new version snapshot on every save.
	if s.workflowVersionStore != nil {
		ver, err := s.workflowVersionStore.CreateWorkflowVersion(r.Context(), service.WorkflowVersion{
			WorkflowID:  id,
			Name:        record.Name,
			Description: record.Description,
			Graph:       record.Graph,
			CreatedBy:   userEmail,
		})
		if err != nil {
			slog.Error("create workflow version failed", "id", id, "error", err)
			// Non-fatal: workflow was updated, version just didn't get created.
		} else {
			// On first save (no active version yet), auto-set active version.
			if record.ActiveVersion == nil {
				if err := s.workflowVersionStore.SetActiveVersion(r.Context(), id, ver.Version); err != nil {
					slog.Error("set initial active version failed", "id", id, "error", err)
				} else {
					record.ActiveVersion = &ver.Version
				}
			}
		}
	}

	if cronChanged && s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Error("scheduler reload failed after workflow update", "error", err)
		}
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteWorkflowAPI handles DELETE /api/v1/workflows/:id.
func (s *Server) DeleteWorkflowAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	// Delete all triggers associated with this workflow before deleting the workflow.
	var hadCronTriggers bool
	if s.triggerStore != nil {
		triggers, err := s.triggerStore.ListTriggers(r.Context(), id)
		if err != nil {
			slog.Error("list triggers for delete failed", "id", id, "error", err)
		} else {
			for _, t := range triggers {
				if t.Type == "cron" {
					hadCronTriggers = true
				}
				if err := s.triggerStore.DeleteTrigger(r.Context(), t.ID); err != nil {
					slog.Error("delete trigger failed during workflow delete", "trigger_id", t.ID, "error", err)
				}
			}
		}
	}

	if err := s.workflowStore.DeleteWorkflow(r.Context(), id); err != nil {
		slog.Error("delete workflow failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete workflow: %v", err), http.StatusInternalServerError)
		return
	}

	// Reload scheduler if any cron triggers were deleted.
	if hadCronTriggers && s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Error("scheduler reload failed after workflow delete", "error", err)
		}
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Workflow Execution ───

// runWorkflowRequest is the JSON body for POST /api/v1/workflows/run/:id.
type runWorkflowRequest struct {
	Inputs map[string]any `json:"inputs"`
}

// runWorkflowResponse is returned when a workflow is started (async) or completed (sync).
type runWorkflowResponse struct {
	RunID      string         `json:"run_id"`
	WorkflowID string         `json:"workflow_id"`
	Status     string         `json:"status"`
	Outputs    map[string]any `json:"outputs,omitempty"`
}

// RunWorkflowAPI handles POST /api/v1/workflows/run/:id.
// By default the workflow is executed asynchronously and the response returns
// a run_id that can be used to cancel the run.
// Pass ?sync=true to run synchronously: the request blocks until the workflow
// completes and the response includes the collected outputs.
func (s *Server) RunWorkflowAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	wf, err := s.workflowStore.GetWorkflow(r.Context(), id)
	if err != nil {
		slog.Error("run workflow: get failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get workflow: %v", err), http.StatusInternalServerError)
		return
	}

	if wf == nil {
		httpResponse(w, fmt.Sprintf("workflow %q not found", id), http.StatusNotFound)
		return
	}

	// Determine which graph to run: if ?version=N is set, load that version's graph.
	graphToRun := wf.Graph
	if versionStr := r.URL.Query().Get("version"); versionStr != "" {
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			httpResponse(w, fmt.Sprintf("invalid version parameter: %v", err), http.StatusBadRequest)
			return
		}
		if s.workflowVersionStore == nil {
			httpResponse(w, "version store not configured", http.StatusServiceUnavailable)
			return
		}
		ver, err := s.workflowVersionStore.GetWorkflowVersion(r.Context(), id, version)
		if err != nil {
			slog.Error("run workflow: get version failed", "id", id, "version", version, "error", err)
			httpResponse(w, fmt.Sprintf("failed to get workflow version: %v", err), http.StatusInternalServerError)
			return
		}
		if ver == nil {
			httpResponse(w, fmt.Sprintf("workflow %q version %d not found", id, version), http.StatusNotFound)
			return
		}
		graphToRun = ver.Graph
	}

	var req runWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Inputs == nil {
		req.Inputs = make(map[string]any)
	}

	syncMode := r.URL.Query().Get("sync") == "true"

	// Both sync and async modes run the engine in a goroutine that outlives
	// the HTTP request. Use context.Background() so the request context
	// cancellation does not kill background graph execution.
	parentCtx := context.Background()

	// Enrich context with workflow metadata for structured logging.
	requestID := r.Header.Get(mrequestid.HeaderXRequestID)
	parentCtx = logi.WithContext(parentCtx, slog.With(
		slog.String("workflow_id", id),
		slog.String("workflow_name", wf.Name),
		slog.String("request_id", requestID),
	))

	// Register the run and get a cancellable context.
	runID, ctx, cleanup := s.registerRun(parentCtx, id, "api")

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

	// Manual/API runs start from "input" nodes only.
	var entryNodeIDs []string
	hasOutputNode := false
	for _, n := range graphToRun.Nodes {
		if n.Type == "input" {
			entryNodeIDs = append(entryNodeIDs, n.ID)
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

			result, err := engine.Run(ctx, graphToRun, req.Inputs, entryNodeIDs, outputCh)
			if err != nil {
				logi.Ctx(ctx).Error("run workflow failed", "id", id, "run_id", runID, "error", err)
				return
			}

			logi.Ctx(ctx).Info("workflow completed", "id", id, "run_id", runID,
				"output_keys", mapKeys(result.Outputs))
		}()

		early := <-outputCh
		if early.Err != nil {
			logi.Ctx(ctx).Error("run workflow failed", "id", id, "run_id", runID, "error", early.Err)
			httpResponse(w, fmt.Sprintf("workflow execution failed: %v", early.Err), http.StatusInternalServerError)
			return
		}

		httpResponseJSON(w, runWorkflowResponse{
			RunID:      runID,
			WorkflowID: id,
			Status:     "completed",
			Outputs:    early.Outputs,
		}, http.StatusOK)
	} else {
		// Asynchronous (or sync without output node): run in goroutine,
		// return immediately. Nothing to wait for.
		go func() {
			defer cleanup()

			result, err := engine.Run(ctx, graphToRun, req.Inputs, entryNodeIDs, nil)
			if err != nil {
				logi.Ctx(ctx).Error("run workflow failed", "id", id, "run_id", runID, "error", err)
				return
			}

			logi.Ctx(ctx).Info("workflow completed", "id", id, "run_id", runID,
				"output_keys", mapKeys(result.Outputs))
		}()

		httpResponseJSON(w, runWorkflowResponse{
			RunID:      runID,
			WorkflowID: id,
			Status:     "running",
		}, http.StatusAccepted)
	}
}

// ─── Workflow Version API ───

// workflowVersionsResponse wraps a list of workflow version records for JSON output.
type workflowVersionsResponse struct {
	Versions []service.WorkflowVersion `json:"versions"`
}

// ListWorkflowVersionsAPI handles GET /api/v1/workflows/:id/versions.
func (s *Server) ListWorkflowVersionsAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowVersionStore == nil {
		httpResponse(w, "version store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	versions, err := s.workflowVersionStore.ListWorkflowVersions(r.Context(), id)
	if err != nil {
		slog.Error("list workflow versions failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to list workflow versions: %v", err), http.StatusInternalServerError)
		return
	}

	if versions == nil {
		versions = []service.WorkflowVersion{}
	}

	httpResponseJSON(w, workflowVersionsResponse{Versions: versions}, http.StatusOK)
}

// GetWorkflowVersionAPI handles GET /api/v1/workflows/:id/versions/:version.
func (s *Server) GetWorkflowVersionAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowVersionStore == nil {
		httpResponse(w, "version store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	versionStr := r.PathValue("version")
	version, err := strconv.Atoi(versionStr)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid version: %v", err), http.StatusBadRequest)
		return
	}

	ver, err := s.workflowVersionStore.GetWorkflowVersion(r.Context(), id, version)
	if err != nil {
		slog.Error("get workflow version failed", "id", id, "version", version, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get workflow version: %v", err), http.StatusInternalServerError)
		return
	}

	if ver == nil {
		httpResponse(w, fmt.Sprintf("workflow %q version %d not found", id, version), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, ver, http.StatusOK)
}

// setActiveVersionRequest is the JSON body for PUT /api/v1/workflows/:id/active-version.
type setActiveVersionRequest struct {
	Version int `json:"version"`
}

// SetActiveVersionAPI handles PUT /api/v1/workflows/:id/active-version.
func (s *Server) SetActiveVersionAPI(w http.ResponseWriter, r *http.Request) {
	if s.workflowVersionStore == nil {
		httpResponse(w, "version store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "workflow id is required", http.StatusBadRequest)
		return
	}

	var req setActiveVersionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Version <= 0 {
		httpResponse(w, "version must be a positive integer", http.StatusBadRequest)
		return
	}

	// Verify the version exists.
	ver, err := s.workflowVersionStore.GetWorkflowVersion(r.Context(), id, req.Version)
	if err != nil {
		slog.Error("set active version: get version failed", "id", id, "version", req.Version, "error", err)
		httpResponse(w, fmt.Sprintf("failed to verify version: %v", err), http.StatusInternalServerError)
		return
	}
	if ver == nil {
		httpResponse(w, fmt.Sprintf("workflow %q version %d not found", id, req.Version), http.StatusNotFound)
		return
	}

	if err := s.workflowVersionStore.SetActiveVersion(r.Context(), id, req.Version); err != nil {
		slog.Error("set active version failed", "id", id, "version", req.Version, "error", err)
		httpResponse(w, fmt.Sprintf("failed to set active version: %v", err), http.StatusInternalServerError)
		return
	}

	// Also update the workflow's graph to match the active version for backward compatibility.
	if _, err := s.workflowStore.UpdateWorkflow(r.Context(), id, service.Workflow{
		Name:        ver.Name,
		Description: ver.Description,
		Graph:       ver.Graph,
		UpdatedBy:   s.getUserEmail(r),
	}); err != nil {
		slog.Error("update workflow graph from active version failed", "id", id, "version", req.Version, "error", err)
		// Non-fatal: active_version was set, graph sync just failed.
	}

	// Reload scheduler in case the active version changed cron triggers.
	if s.scheduler != nil {
		if err := s.scheduler.Reload(); err != nil {
			slog.Error("scheduler reload failed after set active version", "error", err)
		}
	}

	httpResponse(w, fmt.Sprintf("active version set to %d", req.Version), http.StatusOK)
}

// ─── Trigger Sync ───

// triggerNodeType maps graph node types to DB trigger types.
var triggerNodeType = map[string]string{
	"http_trigger": "http",
	"cron_trigger": "cron",
}

// isTriggerNode returns true if the node type is a trigger type.
func isTriggerNode(nodeType string) bool {
	_, ok := triggerNodeType[nodeType]
	return ok
}

// hasTriggerNodes returns true if the graph contains any trigger nodes.
func (s *Server) hasTriggerNodes(graph service.WorkflowGraph) bool {
	for _, n := range graph.Nodes {
		if isTriggerNode(n.Type) {
			return true
		}
	}
	return false
}

// syncTriggers synchronises DB trigger records with the trigger nodes present
// in the workflow graph. It:
//   - Creates new triggers for trigger nodes that have no trigger_id yet
//   - Updates existing triggers whose config has changed
//   - Deletes DB triggers that no longer have a corresponding node in the graph
//   - Writes the assigned trigger_id back into each trigger node's data map
//
// The graph is mutated in-place. Returns whether any cron triggers were
// created, updated or deleted (so the caller can reload the scheduler).
func (s *Server) syncTriggers(ctx context.Context, workflowID string, graph *service.WorkflowGraph, userEmail string) (cronChanged bool, err error) {
	// 1. Load existing DB triggers for this workflow.
	existing, err := s.triggerStore.ListTriggers(ctx, workflowID)
	if err != nil {
		return false, fmt.Errorf("list triggers: %w", err)
	}

	// Build map: trigger ID → existing trigger.
	existingByID := make(map[string]service.Trigger, len(existing))
	for _, t := range existing {
		existingByID[t.ID] = t
	}

	// 2. Walk graph nodes and collect trigger nodes.
	// Track which existing trigger IDs are still referenced by a node.
	seenTriggerIDs := make(map[string]bool)

	for i := range graph.Nodes {
		node := &graph.Nodes[i]
		dbType, ok := triggerNodeType[node.Type]
		if !ok {
			continue // not a trigger node
		}

		if node.Data == nil {
			node.Data = make(map[string]any)
		}

		triggerID, _ := node.Data["trigger_id"].(string)

		if triggerID != "" {
			// Node already has a trigger_id — check if it still exists in DB.
			if t, exists := existingByID[triggerID]; exists {
				seenTriggerIDs[triggerID] = true

				// Check if config changed and needs updating.
				newConfig := s.buildTriggerConfig(node)
				alias, _ := node.Data["alias"].(string)
				public, _ := node.Data["public"].(bool)
				if configChanged(t.Config, newConfig) || t.Alias != alias || t.Public != public {
					updated, err := s.triggerStore.UpdateTrigger(ctx, triggerID, service.Trigger{
						Type:      dbType,
						Config:    newConfig,
						Alias:     alias,
						Public:    public,
						Enabled:   true,
						UpdatedBy: userEmail,
					})
					if err != nil {
						slog.Error("sync: update trigger failed", "trigger_id", triggerID, "error", err)
					} else if updated != nil {
						if dbType == "cron" {
							cronChanged = true
						}
					}
				}
				continue
			}
			// trigger_id references a non-existent trigger — treat as new.
		}

		// Create a new trigger for this node.
		newConfig := s.buildTriggerConfig(node)
		alias, _ := node.Data["alias"].(string)
		public, _ := node.Data["public"].(bool)
		created, err := s.triggerStore.CreateTrigger(ctx, service.Trigger{
			WorkflowID: workflowID,
			Type:       dbType,
			Config:     newConfig,
			Alias:      alias,
			Public:     public,
			Enabled:    true,
			CreatedBy:  userEmail,
			UpdatedBy:  userEmail,
		})
		if err != nil {
			slog.Error("sync: create trigger failed", "node_id", node.ID, "error", err)
			continue
		}
		// Write the new trigger ID back into the node data.
		node.Data["trigger_id"] = created.ID
		seenTriggerIDs[created.ID] = true

		if dbType == "cron" {
			cronChanged = true
		}
	}

	// 3. Delete DB triggers that no longer have a matching node.
	for _, t := range existing {
		if seenTriggerIDs[t.ID] {
			continue
		}
		if err := s.triggerStore.DeleteTrigger(ctx, t.ID); err != nil {
			slog.Error("sync: delete orphaned trigger failed", "trigger_id", t.ID, "error", err)
			continue
		}
		if t.Type == "cron" {
			cronChanged = true
		}
		slog.Info("sync: deleted orphaned trigger", "trigger_id", t.ID, "type", t.Type)
	}

	return cronChanged, nil
}

// buildTriggerConfig extracts trigger-specific config from a graph node's data.
func (s *Server) buildTriggerConfig(node *service.WorkflowNode) map[string]any {
	config := make(map[string]any)
	switch node.Type {
	case "cron_trigger":
		if schedule, ok := node.Data["schedule"].(string); ok && schedule != "" {
			config["schedule"] = schedule
		}
		if payload, ok := node.Data["payload"]; ok {
			config["payload"] = payload
		}
	case "http_trigger":
		// HTTP triggers have no user-configurable settings beyond existence.
	}
	return config
}

// configChanged returns true if two config maps differ in meaningful ways.
func configChanged(old, new map[string]any) bool {
	if len(old) != len(new) {
		return true
	}
	for k, v := range new {
		oldV, exists := old[k]
		if !exists {
			return true
		}
		// Simple comparison via JSON serialization for nested values.
		oldJSON, _ := json.Marshal(oldV)
		newJSON, _ := json.Marshal(v)
		if string(oldJSON) != string(newJSON) {
			return true
		}
	}
	return false
}

// mapKeys returns the keys of a map for logging.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
