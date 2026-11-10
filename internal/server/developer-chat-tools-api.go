package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// DeveloperToolListAPI handles GET /api/v1/developer-space/tools: the tool
// definitions Chats offers when a conversation enables the Developer Space
// source, plus the space's state so the page can explain an unusable space.
func (s *Server) DeveloperToolListAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	space, err := store.EnsureDeveloperSpace(r.Context())
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	tools := make([]map[string]any, 0, len(developerToolDefs))
	for _, tool := range developerToolDefs {
		tools = append(tools, map[string]any{
			"name": tool.Name, "description": tool.Description, "input_schema": tool.InputSchema,
			"confirm": developerToolShell[tool.Name], "edits": developerToolEdits[tool.Name],
		})
	}
	httpResponseJSON(w, map[string]any{
		"tools": tools,
		"space": map[string]any{"status": space.Status, "execution_suspended": space.ExecutionSuspended},
	}, http.StatusOK)
}

type developerToolCallRequest struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	// Project is the conversation's selected project; an explicit project
	// argument wins over it.
	Project   string `json:"project"`
	TraceID   string `json:"trace_id"`
	SessionID string `json:"session_id"`
}

// DeveloperToolCallAPI handles POST /api/v1/developer-space/tools/call: one
// Developer Space tool call from a Chats turn. Approval of shell commands is
// asked in the page before this request is sent; the server runs only for
// the caller's own space under its live execution identity.
func (s *Server) DeveloperToolCallAPI(w http.ResponseWriter, r *http.Request) {
	var req developerToolCallRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, developerWriteMaxBytes*2+(1<<20)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if !isDeveloperTool(req.Name) {
		httpResponse(w, "unknown Developer Space tool", http.StatusBadRequest)
		return
	}
	if req.Arguments == nil {
		req.Arguments = map[string]any{}
	}
	started := time.Now()
	result, err := s.executeDeveloperTool(r.Context(), req.Name, req.Arguments, strings.TrimSpace(req.Project))
	if err != nil {
		var runtimeErr *developerRuntimeError
		switch {
		case errors.As(err, &runtimeErr) && runtimeErr.status != http.StatusConflict:
			httpResponse(w, runtimeErr.message, runtimeErr.status)
			return
		case errors.Is(err, service.ErrAccessDenied), errors.Is(err, service.ErrExecutionDenied):
			httpResponse(w, "Developer Space access denied", http.StatusForbidden)
			return
		}
		// Anything else (space stopped, feature off) is the model's to read.
		result = developerToolResult{Error: err.Error()}
	}
	s.recordDeveloperToolObservation(r, req, result, time.Since(started))
	httpResponseJSON(w, result, http.StatusOK)
}

func (s *Server) recordDeveloperToolObservation(r *http.Request, req developerToolCallRequest, result developerToolResult, elapsed time.Duration) {
	if req.TraceID == "" {
		return
	}
	input, _ := json.Marshal(req.Arguments)
	params := llmAuditParams{
		id: ulid.Make().String(), source: "chat", endpoint: r.URL.Path, obsType: service.ObservationTool,
		name: req.Name, traceID: req.TraceID, sessionID: req.SessionID,
		input: string(input), output: developerToolResultText(result), latencyMs: elapsed.Milliseconds(), status: "success",
		metadata: map[string]any{"origin": "developer_space", "project": result.Project, "changed": result.Changed},
	}
	if result.Error != "" {
		params.status, params.level, params.errMsg = "error", service.ObservationLevelError, result.Error
	}
	s.recordLLMCallAsync(r.Context(), params)
}
