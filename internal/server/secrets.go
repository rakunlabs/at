package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── Variable CRUD API ───

// variablesResponse wraps a list of variable records for JSON output.
type variablesResponse struct {
	Variables []service.Variable `json:"variables"`
}

// ListVariablesAPI handles GET /api/v1/variables.
// Secret variable values are redacted; non-secret values are shown inline.
func (s *Server) ListVariablesAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.variableStore.ListVariables(r.Context(), q)
	if err != nil {
		slog.Error("list variables failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list variables: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Variable]{Data: []service.Variable{}}
	}

	// Redact values only for secret variables.
	for i := range records.Data {
		if records.Data[i].Secret {
			records.Data[i].Value = "***"
		}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetVariableAPI handles GET /api/v1/variables/:id.
// The value is returned in full (not redacted) for single-variable retrieval.
func (s *Server) GetVariableAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "variable id is required", http.StatusBadRequest)
		return
	}

	record, err := s.variableStore.GetVariable(r.Context(), id)
	if err != nil {
		slog.Error("get variable failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get variable: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("variable %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// CreateVariableAPI handles POST /api/v1/variables.
func (s *Server) CreateVariableAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Variable
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	if req.Value == "" {
		httpResponse(w, "value is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	// Upsert: if a variable with this key already exists, update it.
	existing, _ := s.variableStore.GetVariableByKey(r.Context(), req.Key)
	if existing != nil {
		existing.Value = req.Value
		if req.Description != "" {
			existing.Description = req.Description
		}
		existing.Secret = req.Secret
		existing.UpdatedBy = userEmail
		record, err := s.variableStore.UpdateVariable(r.Context(), existing.ID, *existing)
		if err != nil {
			slog.Error("update variable failed", "key", req.Key, "error", err)
			httpResponse(w, fmt.Sprintf("failed to update variable: %v", err), http.StatusInternalServerError)
			return
		}
		httpResponseJSON(w, record, http.StatusOK)
		return
	}

	record, err := s.variableStore.CreateVariable(r.Context(), req)
	if err != nil {
		slog.Error("create variable failed", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create variable: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateVariableAPI handles PUT /api/v1/variables/:id.
func (s *Server) UpdateVariableAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "variable id is required", http.StatusBadRequest)
		return
	}

	var req service.Variable
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	req.UpdatedBy = userEmail

	record, err := s.variableStore.UpdateVariable(r.Context(), id, req)
	if err != nil {
		slog.Error("update variable failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update variable: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("variable %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// DeleteVariableAPI handles DELETE /api/v1/variables/:id.
func (s *Server) DeleteVariableAPI(w http.ResponseWriter, r *http.Request) {
	if s.variableStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "variable id is required", http.StatusBadRequest)
		return
	}

	if err := s.variableStore.DeleteVariable(r.Context(), id); err != nil {
		slog.Error("delete variable failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete variable: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
