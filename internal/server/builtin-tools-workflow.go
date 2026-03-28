package server

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ─── Workflow Management Tool Executors ───
//
// These executors allow agents in chat sessions to create, list, get,
// update, delete, and run workflows and triggers programmatically.

// execWorkflowList lists workflows with optional search query.
// Parameters: query (string, optional), limit (int, optional), offset (int, optional)
func (s *Server) execWorkflowList(ctx context.Context, args map[string]any) (string, error) {
	if s.workflowStore == nil {
		return "", fmt.Errorf("workflow store not configured")
	}

	records, err := s.workflowStore.ListWorkflows(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list workflows: %w", err)
	}

	if records == nil || len(records.Data) == 0 {
		return "No workflows found.", nil
	}

	// Return summary information (avoid huge graph dumps).
	type workflowSummary struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		ActiveVersion *int   `json:"active_version,omitempty"`
		CreatedAt     string `json:"created_at"`
		UpdatedAt     string `json:"updated_at"`
		NodeCount     int    `json:"node_count"`
		EdgeCount     int    `json:"edge_count"`
	}

	summaries := make([]workflowSummary, len(records.Data))
	for i, wf := range records.Data {
		summaries[i] = workflowSummary{
			ID:            wf.ID,
			Name:          wf.Name,
			Description:   wf.Description,
			ActiveVersion: wf.ActiveVersion,
			CreatedAt:     wf.CreatedAt,
			UpdatedAt:     wf.UpdatedAt,
			NodeCount:     len(wf.Graph.Nodes),
			EdgeCount:     len(wf.Graph.Edges),
		}
	}

	data, err := json.MarshalIndent(summaries, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal workflows: %w", err)
	}

	return string(data), nil
}

// execWorkflowGet retrieves a single workflow with its full graph.
// Parameters: id (string, required)
func (s *Server) execWorkflowGet(ctx context.Context, args map[string]any) (string, error) {
	if s.workflowStore == nil {
		return "", fmt.Errorf("workflow store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	wf, err := s.workflowStore.GetWorkflow(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get workflow: %w", err)
	}
	if wf == nil {
		return "", fmt.Errorf("workflow %q not found", id)
	}

	data, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal workflow: %w", err)
	}

	return string(data), nil
}

// execWorkflowCreate creates a new workflow.
// Parameters: name (string, required), description (string, optional), graph (object, required)
func (s *Server) execWorkflowCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.workflowStore == nil {
		return "", fmt.Errorf("workflow store not configured")
	}

	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name is required")
	}

	description, _ := args["description"].(string)

	graphRaw, ok := args["graph"]
	if !ok {
		return "", fmt.Errorf("graph is required")
	}

	// Parse graph JSON.
	graphJSON, err := json.Marshal(graphRaw)
	if err != nil {
		return "", fmt.Errorf("invalid graph format: %w", err)
	}

	var graph service.WorkflowGraph
	if err := json.Unmarshal(graphJSON, &graph); err != nil {
		return "", fmt.Errorf("invalid graph format: %w", err)
	}

	req := service.Workflow{
		Name:        name,
		Description: description,
		Graph:       graph,
	}

	record, err := s.workflowStore.CreateWorkflow(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create workflow: %w", err)
	}

	// Sync triggers: create DB trigger records for any trigger nodes in the graph.
	if s.triggerStore != nil {
		cronChanged, err := s.syncTriggers(ctx, record.ID, &record.Graph, "agent")
		if err != nil {
			// Non-fatal: workflow was created, triggers just didn't sync.
			return fmt.Sprintf("Workflow created (id: %s) but trigger sync failed: %v", record.ID, err), nil
		}
		if s.hasTriggerNodes(record.Graph) {
			record.UpdatedBy = "agent"
			if _, err = s.workflowStore.UpdateWorkflow(ctx, record.ID, *record); err != nil {
				// Non-fatal.
			}
		}

		if cronChanged && s.scheduler != nil {
			if err := s.scheduler.Reload(); err != nil {
				// Non-fatal.
			}
		}
	}

	// Auto-create version snapshot.
	if s.workflowVersionStore != nil {
		ver, err := s.workflowVersionStore.CreateWorkflowVersion(ctx, service.WorkflowVersion{
			WorkflowID:  record.ID,
			Name:        record.Name,
			Description: record.Description,
			Graph:       record.Graph,
			CreatedBy:   "agent",
		})
		if err == nil && ver != nil {
			if record.ActiveVersion == nil {
				_ = s.workflowVersionStore.SetActiveVersion(ctx, record.ID, ver.Version)
			}
		}
	}

	result := map[string]any{
		"status":      "created",
		"id":          record.ID,
		"name":        record.Name,
		"description": record.Description,
		"node_count":  len(record.Graph.Nodes),
		"edge_count":  len(record.Graph.Edges),
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

// execWorkflowUpdate updates an existing workflow.
// Parameters: id (string, required), name (string, optional), description (string, optional), graph (object, optional)
func (s *Server) execWorkflowUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.workflowStore == nil {
		return "", fmt.Errorf("workflow store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	// Load existing workflow.
	existing, err := s.workflowStore.GetWorkflow(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get workflow: %w", err)
	}
	if existing == nil {
		return "", fmt.Errorf("workflow %q not found", id)
	}

	// Apply updates.
	req := *existing
	if name, ok := args["name"].(string); ok && name != "" {
		req.Name = name
	}
	if description, ok := args["description"].(string); ok {
		req.Description = description
	}
	if graphRaw, ok := args["graph"]; ok {
		graphJSON, err := json.Marshal(graphRaw)
		if err != nil {
			return "", fmt.Errorf("invalid graph format: %w", err)
		}
		var graph service.WorkflowGraph
		if err := json.Unmarshal(graphJSON, &graph); err != nil {
			return "", fmt.Errorf("invalid graph format: %w", err)
		}
		req.Graph = graph
	}
	req.UpdatedBy = "agent"

	// Sync triggers.
	var cronChanged bool
	if s.triggerStore != nil {
		cronChanged, err = s.syncTriggers(ctx, id, &req.Graph, "agent")
		if err != nil {
			return "", fmt.Errorf("sync triggers: %w", err)
		}
	}

	record, err := s.workflowStore.UpdateWorkflow(ctx, id, req)
	if err != nil {
		return "", fmt.Errorf("update workflow: %w", err)
	}
	if record == nil {
		return "", fmt.Errorf("workflow %q not found", id)
	}

	// Auto-create version snapshot.
	if s.workflowVersionStore != nil {
		ver, err := s.workflowVersionStore.CreateWorkflowVersion(ctx, service.WorkflowVersion{
			WorkflowID:  id,
			Name:        record.Name,
			Description: record.Description,
			Graph:       record.Graph,
			CreatedBy:   "agent",
		})
		if err == nil && ver != nil {
			if record.ActiveVersion == nil {
				_ = s.workflowVersionStore.SetActiveVersion(ctx, id, ver.Version)
			}
		}
	}

	if cronChanged && s.scheduler != nil {
		_ = s.scheduler.Reload()
	}

	result := map[string]any{
		"status":      "updated",
		"id":          record.ID,
		"name":        record.Name,
		"description": record.Description,
		"node_count":  len(record.Graph.Nodes),
		"edge_count":  len(record.Graph.Edges),
	}

	data, _ := json.MarshalIndent(result, "", "  ")
	return string(data), nil
}

// execWorkflowDelete deletes a workflow and its associated triggers.
// Parameters: id (string, required)
func (s *Server) execWorkflowDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.workflowStore == nil {
		return "", fmt.Errorf("workflow store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	// Delete all triggers associated with this workflow.
	var hadCronTriggers bool
	if s.triggerStore != nil {
		triggers, err := s.triggerStore.ListTriggers(ctx, id)
		if err == nil {
			for _, t := range triggers {
				if t.Type == "cron" {
					hadCronTriggers = true
				}
				_ = s.triggerStore.DeleteTrigger(ctx, t.ID)
			}
		}
	}

	if err := s.workflowStore.DeleteWorkflow(ctx, id); err != nil {
		return "", fmt.Errorf("delete workflow: %w", err)
	}

	if hadCronTriggers && s.scheduler != nil {
		_ = s.scheduler.Reload()
	}

	return fmt.Sprintf("Workflow %q deleted successfully.", id), nil
}

// execWorkflowRun executes a workflow.
// Parameters: id (string, required), inputs (object, optional), sync (bool, optional)
func (s *Server) execWorkflowRun(ctx context.Context, args map[string]any) (string, error) {
	if s.workflowStore == nil {
		return "", fmt.Errorf("workflow store not configured")
	}

	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}

	wf, err := s.workflowStore.GetWorkflow(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get workflow: %w", err)
	}
	if wf == nil {
		return "", fmt.Errorf("workflow %q not found", id)
	}

	// Use active version graph if available.
	graphToRun := wf.Graph
	if wf.ActiveVersion != nil && s.workflowVersionStore != nil {
		ver, err := s.workflowVersionStore.GetWorkflowVersion(ctx, id, *wf.ActiveVersion)
		if err == nil && ver != nil {
			graphToRun = ver.Graph
		}
	}

	inputs, _ := args["inputs"].(map[string]any)
	if inputs == nil {
		inputs = make(map[string]any)
	}

	syncMode, _ := args["sync"].(bool)

	engine := s.buildWorkflowEngine()

	// Find input nodes as entry points.
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
		result, err := engine.Run(ctx, graphToRun, inputs, entryNodeIDs, nil)
		if err != nil {
			return "", fmt.Errorf("workflow execution failed: %w", err)
		}

		data, _ := json.MarshalIndent(map[string]any{
			"status":  "completed",
			"outputs": result.Outputs,
		}, "", "  ")
		return string(data), nil
	}

	// Async execution.
	go func() {
		_, _ = engine.Run(context.Background(), graphToRun, inputs, entryNodeIDs, nil)
	}()

	return fmt.Sprintf("Workflow %q started asynchronously.", id), nil
}

// execTriggerList lists triggers, optionally filtered by workflow ID.
// Parameters: workflow_id (string, optional)
func (s *Server) execTriggerList(ctx context.Context, args map[string]any) (string, error) {
	if s.triggerStore == nil {
		return "", fmt.Errorf("trigger store not configured")
	}

	workflowID, _ := args["workflow_id"].(string)

	var triggers []service.Trigger
	var err error
	if workflowID != "" {
		triggers, err = s.triggerStore.ListTriggers(ctx, workflowID)
	} else {
		triggers, err = s.triggerStore.ListAllTriggers(ctx)
	}
	if err != nil {
		return "", fmt.Errorf("list triggers: %w", err)
	}

	if len(triggers) == 0 {
		return "No triggers found.", nil
	}

	data, err := json.MarshalIndent(triggers, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal triggers: %w", err)
	}

	return string(data), nil
}

// buildWorkflowEngine creates a workflow engine with all the server's
// lookup functions wired in. This is a helper shared by workflow execution
// tools and the chat_reply node.
func (s *Server) buildWorkflowEngine() *workflow.Engine {
	providerLookup := func(key string) (service.LLMProvider, string, error) {
		s.providerMu.RLock()
		info, ok := s.providers[key]
		s.providerMu.RUnlock()
		if !ok {
			return nil, "", fmt.Errorf("provider %q not found", key)
		}
		return info.provider, info.defaultModel, nil
	}

	var skillLookup workflow.SkillLookup
	if s.skillStore != nil {
		skillLookup = func(nameOrID string) (*service.Skill, error) {
			sk, err := s.skillStore.GetSkill(context.Background(), nameOrID)
			if err != nil {
				return nil, err
			}
			if sk != nil {
				return sk, nil
			}
			return s.skillStore.GetSkillByName(context.Background(), nameOrID)
		}
	}

	var varLookup workflow.VarLookup
	var varLister workflow.VarLister
	if s.variableStore != nil {
		varLookup = func(key string) (string, error) {
			v, err := s.variableStore.GetVariableByKey(context.Background(), key)
			if err != nil {
				return "", err
			}
			if v == nil {
				return "", fmt.Errorf("variable %q not found", key)
			}
			return v.Value, nil
		}
		varLister = func() (map[string]string, error) {
			vars, err := s.variableStore.ListVariables(context.Background(), nil)
			if err != nil {
				return nil, err
			}
			m := make(map[string]string, len(vars.Data))
			for _, v := range vars.Data {
				m[v.Key] = v.Value
			}
			return m, nil
		}
	}

	var nodeConfigLookup workflow.NodeConfigLookup
	if s.nodeConfigStore != nil {
		nodeConfigLookup = func(id string) (*service.NodeConfig, error) {
			return s.nodeConfigStore.GetNodeConfig(context.Background(), id)
		}
	}

	var workflowLookup workflow.WorkflowLookup
	if s.workflowStore != nil {
		workflowLookup = func(ctx context.Context, id string) (*service.Workflow, error) {
			return s.workflowStore.GetWorkflow(ctx, id)
		}
	}

	var agentLookup workflow.AgentLookup
	if s.agentStore != nil {
		agentLookup = func(ctx context.Context, id string) (*service.Agent, error) {
			return s.agentStore.GetAgent(ctx, id)
		}
	}

	engine := workflow.NewEngine(providerLookup, skillLookup, varLookup, varLister, nodeConfigLookup, workflowLookup, agentLookup, s.ragSearchFunc(), s.ragIngestFunc(), s.ragIngestFileFunc(), s.ragDeleteBySourceFunc(), s.varSaveFunc(), s.ragStateLookupFunc(), s.ragStateSaveFunc(), s.dispatchBuiltinTool, builtinToolDefsForWorkflow(), nil, s.chatMessageCreatorFunc(), s.chatSessionLookupFunc(), s.recordUsageFunc(), s.checkBudgetFunc(), s.recordAuditFunc(), s.goalAncestryFunc(), s.versionLookupFunc())
	engine.SetRAGPageUpsert(s.ragPageUpsertFunc())
	engine.SetMemoryRecall(s.memoryRecallFunc())
	return engine
}

// chatMessageCreatorFunc returns a ChatMessageCreatorFunc that creates messages
// in chat sessions. Returns nil if the chat session store is not configured.
func (s *Server) chatMessageCreatorFunc() workflow.ChatMessageCreatorFunc {
	if s.chatSessionStore == nil {
		return nil
	}
	return func(ctx context.Context, sessionID, role, content string) error {
		_, err := s.chatSessionStore.CreateChatMessage(ctx, service.ChatMessage{
			SessionID: sessionID,
			Role:      role,
			Data: service.ChatMessageData{
				Content: content,
			},
		})
		return err
	}
}

// chatSessionLookupFunc returns a ChatSessionLookupFunc that resolves chat
// sessions by ID. Returns nil if the chat session store is not configured.
func (s *Server) chatSessionLookupFunc() workflow.ChatSessionLookupFunc {
	if s.chatSessionStore == nil {
		return nil
	}
	return func(ctx context.Context, id string) (*service.ChatSession, error) {
		return s.chatSessionStore.GetChatSession(ctx, id)
	}
}

// ─── Workflow-as-MCP-Tool Helpers ───

var reNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// workflowToolName converts a workflow name into a valid MCP tool name.
// Example: "My Data Pipeline" → "wf_my_data_pipeline"
func workflowToolName(wf *service.Workflow) string {
	name := strings.ToLower(strings.TrimSpace(wf.Name))
	name = reNonAlnum.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = wf.ID
	}
	return "wf_" + name
}

// workflowToolDef builds a service.Tool definition from a workflow.
// It lists available entry points (input node labels) in the description
// so the caller knows which entries are available.
func workflowToolDef(wf *service.Workflow) service.Tool {
	desc := wf.Description
	if desc == "" {
		desc = "Run workflow: " + wf.Name
	}

	// Collect input node labels for the description.
	graph := wf.Graph
	var entryLabels []string
	for _, n := range graph.Nodes {
		if n.Type == "input" {
			label, _ := n.Data["label"].(string)
			if label == "" {
				label = n.ID
			}
			entryLabels = append(entryLabels, label)
		}
	}
	if len(entryLabels) > 0 {
		desc += " | Available entries: " + strings.Join(entryLabels, ", ")
	}

	return service.Tool{
		Name:        workflowToolName(wf),
		Description: desc,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"entry": map[string]any{
					"type":        "string",
					"description": "Name of the input node to enter (the label of the input node). If omitted, all input nodes are triggered.",
				},
				"inputs": map[string]any{
					"type":        "object",
					"description": "Key-value inputs to pass to the workflow",
				},
			},
		},
	}
}

// executeWorkflowTool runs a workflow synchronously and returns the JSON result.
// If args contains an "entry" field, only the matching input node is used as
// the entry point. Otherwise all input nodes are triggered.
func (s *Server) executeWorkflowTool(ctx context.Context, wf *service.Workflow, args map[string]any) (string, error) {
	graphToRun := wf.Graph
	if wf.ActiveVersion != nil && s.workflowVersionStore != nil {
		ver, err := s.workflowVersionStore.GetWorkflowVersion(ctx, wf.ID, *wf.ActiveVersion)
		if err == nil && ver != nil {
			graphToRun = ver.Graph
		}
	}

	inputs, _ := args["inputs"].(map[string]any)
	if inputs == nil {
		inputs = make(map[string]any)
	}

	engine := s.buildWorkflowEngine()

	// Resolve entry node IDs.
	entryName, _ := args["entry"].(string)
	var entryNodeIDs []string

	if entryName != "" {
		// Find the input node matching the requested entry by label or ID.
		for _, n := range graphToRun.Nodes {
			if n.Type != "input" {
				continue
			}
			label, _ := n.Data["label"].(string)
			if strings.EqualFold(label, entryName) || n.ID == entryName {
				entryNodeIDs = append(entryNodeIDs, n.ID)
				break
			}
		}
		if len(entryNodeIDs) == 0 {
			return "", fmt.Errorf("input node %q not found in workflow %q", entryName, wf.Name)
		}
	} else {
		// Default: all input nodes.
		for _, n := range graphToRun.Nodes {
			if n.Type == "input" {
				entryNodeIDs = append(entryNodeIDs, n.ID)
			}
		}
	}

	result, err := engine.Run(ctx, graphToRun, inputs, entryNodeIDs, nil)
	if err != nil {
		return "", fmt.Errorf("workflow execution failed: %w", err)
	}

	data, _ := json.MarshalIndent(map[string]any{
		"status":  "completed",
		"outputs": result.Outputs,
	}, "", "  ")
	return string(data), nil
}
