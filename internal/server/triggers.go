package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"

	// Blank import triggers init() registration of all built-in node types.
	_ "github.com/rakunlabs/at/internal/service/workflow/nodes"
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

	wfID := extractTriggerWorkflowID(r)
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

	wfID := extractTriggerWorkflowID(r)
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

	req.WorkflowID = wfID

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

	id := extractTriggerID(r)
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

	id := extractTriggerID(r)
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

	id := extractTriggerID(r)
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

// WebhookAPI handles POST /api/v1/webhooks/:trigger_id.
// It looks up the HTTP trigger, verifies it is enabled, loads the associated
// workflow, and runs the engine with the request body as input.
func (s *Server) WebhookAPI(w http.ResponseWriter, r *http.Request) {
	if s.triggerStore == nil || s.workflowStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	triggerID := extractWebhookTriggerID(r)
	if triggerID == "" {
		httpResponse(w, "trigger id is required", http.StatusBadRequest)
		return
	}

	trigger, err := s.triggerStore.GetTrigger(r.Context(), triggerID)
	if err != nil {
		slog.Error("webhook: get trigger failed", "trigger_id", triggerID, "error", err)
		httpResponse(w, "internal error", http.StatusInternalServerError)
		return
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

	// Load the workflow.
	wf, err := s.workflowStore.GetWorkflow(r.Context(), trigger.WorkflowID)
	if err != nil {
		slog.Error("webhook: get workflow failed",
			"trigger_id", triggerID,
			"workflow_id", trigger.WorkflowID,
			"error", err)
		httpResponse(w, "internal error", http.StatusInternalServerError)
		return
	}

	if wf == nil {
		httpResponse(w, "associated workflow not found", http.StatusNotFound)
		return
	}

	// Build structured input data from the HTTP request.
	// Body is passed as io.ReadCloser — unconsumed. Downstream script nodes
	// use BodyWrapper methods (.toString(), .jsonParse(), etc.) to read it.
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

	// Pass body as unconsumed io.ReadCloser.
	// The engine's goja setup will wrap this in a BodyWrapper automatically.
	inputs["body"] = r.Body

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

	engine := workflow.NewEngine(providerLookup)

	result, err := engine.Run(r.Context(), wf.Graph, inputs)
	if err != nil {
		slog.Error("webhook: workflow execution failed",
			"trigger_id", triggerID,
			"workflow_id", trigger.WorkflowID,
			"error", err)
		httpResponse(w, fmt.Sprintf("workflow execution failed: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, result, http.StatusOK)
}

// ─── Helpers ───

// extractTriggerWorkflowID extracts the workflow ID from trigger list/create URLs.
// Expected path: /api/v1/workflows/{wf_id}/triggers
func extractTriggerWorkflowID(r *http.Request) string {
	path := r.URL.Path
	const prefix = "/api/v1/workflows/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}

	rest := strings.TrimPrefix(path, prefix)
	// rest is "{wf_id}/triggers" or "{wf_id}/triggers/"
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 1 {
		return ""
	}

	return parts[0]
}

// extractTriggerID extracts the trigger ID from trigger CRUD URLs.
// Expected path: /api/v1/triggers/{id}
func extractTriggerID(r *http.Request) string {
	path := r.URL.Path
	const prefix = "/api/v1/triggers/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}

	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimSuffix(id, "/")

	return id
}

// extractWebhookTriggerID extracts the trigger ID from webhook URLs.
// Expected path: /api/v1/webhooks/{trigger_id}
func extractWebhookTriggerID(r *http.Request) string {
	path := r.URL.Path
	const prefix = "/api/v1/webhooks/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}

	id := strings.TrimPrefix(path, prefix)
	id = strings.TrimSuffix(id, "/")

	return id
}
