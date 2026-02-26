package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// ─── Skill CRUD API ───

// skillsResponse wraps a list of skill records for JSON output.
type skillsResponse struct {
	Skills []service.Skill `json:"skills"`
}

// ListSkillsAPI handles GET /api/v1/skills.
func (s *Server) ListSkillsAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	records, err := s.skillStore.ListSkills(r.Context())
	if err != nil {
		slog.Error("list skills failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list skills: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.Skill{}
	}

	httpResponseJSON(w, skillsResponse{Skills: records}, http.StatusOK)
}

// GetSkillAPI handles GET /api/v1/skills/:id.
func (s *Server) GetSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill id is required", http.StatusBadRequest)
		return
	}

	record, err := s.skillStore.GetSkill(r.Context(), id)
	if err != nil {
		slog.Error("get skill failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get skill: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("skill %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateSkillAPI handles POST /api/v1/skills.
func (s *Server) CreateSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Skill
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	record, err := s.skillStore.CreateSkill(r.Context(), req)
	if err != nil {
		slog.Error("create skill failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateSkillAPI handles PUT /api/v1/skills/:id.
func (s *Server) UpdateSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill id is required", http.StatusBadRequest)
		return
	}

	var req service.Skill
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}

	record, err := s.skillStore.UpdateSkill(r.Context(), id, req)
	if err != nil {
		slog.Error("update skill failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update skill: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("skill %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteSkillAPI handles DELETE /api/v1/skills/:id.
func (s *Server) DeleteSkillAPI(w http.ResponseWriter, r *http.Request) {
	if s.skillStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "skill id is required", http.StatusBadRequest)
		return
	}

	if err := s.skillStore.DeleteSkill(r.Context(), id); err != nil {
		slog.Error("delete skill failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete skill: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}

// ─── Test Handler API ───

// testHandlerRequest is the request body for TestHandlerAPI.
type testHandlerRequest struct {
	Handler     string         `json:"handler"`
	HandlerType string         `json:"handler_type"` // "js" (default) or "bash"
	Arguments   map[string]any `json:"arguments"`
}

// testHandlerResponse is the response body for TestHandlerAPI.
type testHandlerResponse struct {
	Result     string `json:"result"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// TestHandlerAPI handles POST /api/v1/skills/test-handler.
// It executes a tool handler (JS or bash) server-side with sample arguments
// and returns the result. Used by the Skill Builder AI panel to test handlers.
func (s *Server) TestHandlerAPI(w http.ResponseWriter, r *http.Request) {
	var req testHandlerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Handler == "" {
		httpResponse(w, "handler is required", http.StatusBadRequest)
		return
	}

	if req.Arguments == nil {
		req.Arguments = make(map[string]any)
	}

	start := time.Now()
	var result string
	var execErr error

	if req.HandlerType == "bash" {
		// Build a VarLister from the variable store.
		var varLister workflow.VarLister
		if s.variableStore != nil {
			varLister = func() (map[string]string, error) {
				vars, err := s.variableStore.ListVariables(context.Background())
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
		result, execErr = workflow.ExecuteBashHandler(r.Context(), req.Handler, req.Arguments, varLister)
	} else {
		// Default: JS handler.
		var varLookup workflow.VarLookup
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
		}
		result, execErr = workflow.ExecuteJSHandler(req.Handler, req.Arguments, varLookup)
	}

	durationMs := time.Since(start).Milliseconds()

	resp := testHandlerResponse{
		Result:     result,
		DurationMs: durationMs,
	}
	if execErr != nil {
		resp.Error = execErr.Error()
		slog.Warn("test handler failed", "handler_type", req.HandlerType, "error", execErr, "duration_ms", durationMs)
	}

	httpResponseJSON(w, resp, http.StatusOK)
}
