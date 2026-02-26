package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"

	// Blank import triggers init() registration of all built-in node types.
	_ "github.com/rakunlabs/at/internal/service/workflow/nodes"
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

	record, err := s.workflowStore.CreateWorkflow(r.Context(), req)
	if err != nil {
		slog.Error("create workflow failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create workflow: %v", err), http.StatusInternalServerError)
		return
	}

	// Sync triggers: create DB trigger records for any trigger nodes in the graph.
	if s.triggerStore != nil {
		cronChanged, err := s.syncTriggers(r.Context(), record.ID, &record.Graph)
		if err != nil {
			slog.Error("sync triggers failed after create", "id", record.ID, "error", err)
			// Non-fatal: workflow was created, triggers just didn't sync.
		} else if s.hasTriggerNodes(record.Graph) {
			// Persist the graph with trigger IDs written back into node data.
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

	// Sync triggers before saving: creates/updates/deletes DB trigger records
	// based on trigger nodes in the graph, and writes trigger_id back into
	// node data so the saved graph contains the assigned IDs.
	var cronChanged bool
	if s.triggerStore != nil {
		var err error
		cronChanged, err = s.syncTriggers(r.Context(), id, &req.Graph)
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

// runWorkflowResponse is returned immediately when a workflow is started.
type runWorkflowResponse struct {
	RunID      string `json:"run_id"`
	WorkflowID string `json:"workflow_id"`
	Status     string `json:"status"`
}

// RunWorkflowAPI handles POST /api/v1/workflows/run/:id.
// The workflow is executed asynchronously. The response returns a run_id
// that can be used to cancel the run via POST /api/v1/runs/:run_id/cancel.
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

	var req runWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Inputs == nil {
		req.Inputs = make(map[string]any)
	}

	// Register the run and get a cancellable context (detached from request).
	runID, ctx, cleanup := s.registerRun(context.Background(), id, "api")

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

	engine := workflow.NewEngine(providerLookup, skillLookup, varLookup, varLister, nodeConfigLookup)

	// Run workflow asynchronously.
	go func() {
		defer cleanup()

		result, err := engine.Run(ctx, wf.Graph, req.Inputs)
		if err != nil {
			slog.Error("run workflow failed", "id", id, "run_id", runID, "error", err)
			return
		}

		slog.Info("workflow completed", "id", id, "run_id", runID,
			"output_keys", mapKeys(result.Outputs))
	}()

	httpResponseJSON(w, runWorkflowResponse{
		RunID:      runID,
		WorkflowID: id,
		Status:     "running",
	}, http.StatusAccepted)
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
func (s *Server) syncTriggers(ctx context.Context, workflowID string, graph *service.WorkflowGraph) (cronChanged bool, err error) {
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
						Type:    dbType,
						Config:  newConfig,
						Alias:   alias,
						Public:  public,
						Enabled: true,
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
