package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ListOrganizationsAPI handles GET /api/v1/organizations.
func (s *Server) ListOrganizationsAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.organizationStore.ListOrganizations(r.Context(), q)
	if err != nil {
		slog.Error("list organizations failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list organizations: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.Organization]{Data: []service.Organization{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetOrganizationAPI handles GET /api/v1/organizations/{id}.
func (s *Server) GetOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	record, err := s.organizationStore.GetOrganization(r.Context(), id)
	if err != nil {
		slog.Error("get organization failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// GetOrganizationBudgetAPI handles GET /api/v1/organizations/{id}/budget.
func (s *Server) GetOrganizationBudgetAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil || s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}
	org, err := s.organizationStore.GetOrganization(r.Context(), id)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}
	if org == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}
	status, err := s.organizationBudgetStatus(r.Context(), org, time.Now())
	if err != nil {
		slog.Error("get organization budget failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization budget: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, status, http.StatusOK)
}

// CreateOrganizationAPI handles POST /api/v1/organizations.
func (s *Server) CreateOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.Organization
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		httpResponse(w, "name is required", http.StatusBadRequest)
		return
	}
	if req.BudgetMonthlyCents < 0 {
		httpResponse(w, "budget_monthly_cents must be non-negative", http.StatusBadRequest)
		return
	}
	if req.BudgetPeriod != "" || req.BudgetResetDay != 0 || req.BudgetResetTime != "" || req.BudgetTimezone != "" {
		schedule, err := service.NormalizeBudgetSchedule(req.BudgetSchedule)
		if err != nil {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.BudgetSchedule = schedule
		req.BudgetResetAt = ""
	}

	userEmail := s.getUserEmail(r)
	req.CreatedBy = userEmail
	req.UpdatedBy = userEmail

	record, err := s.organizationStore.CreateOrganization(r.Context(), req)
	if err != nil {
		slog.Error("create organization failed", "name", req.Name, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create organization: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, record, http.StatusCreated)
}

// UpdateOrganizationAPI handles PUT /api/v1/organizations/{id}.
func (s *Server) UpdateOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	// Fetch existing so partial updates preserve existing fields.
	existing, err := s.organizationStore.GetOrganization(r.Context(), id)
	if err != nil {
		slog.Error("get organization for update failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get organization: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}

	var fields map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&fields); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	// Allow partial updates while preserving the ability to explicitly clear
	// string fields such as head_agent_id.
	req := *existing
	if existing.BudgetPeriod == "" && hasBudgetScheduleField(fields) {
		schedule, err := effectiveOrganizationBudgetSchedule(existing)
		if err != nil {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.BudgetSchedule = schedule
	}
	if raw, ok := fields["name"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid name: %v", err), http.StatusBadRequest)
			return
		}
		if v != "" {
			req.Name = v
		}
	}
	if raw, ok := fields["description"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid description: %v", err), http.StatusBadRequest)
			return
		}
		req.Description = v
	}
	if raw, ok := fields["issue_prefix"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid issue_prefix: %v", err), http.StatusBadRequest)
			return
		}
		req.IssuePrefix = v
	}
	if raw, ok := fields["budget_monthly_cents"]; ok {
		var v int64
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid budget_monthly_cents: %v", err), http.StatusBadRequest)
			return
		}
		if v < 0 {
			httpResponse(w, "budget_monthly_cents must be non-negative", http.StatusBadRequest)
			return
		}
		req.BudgetMonthlyCents = v
	}
	if raw, ok := fields["spent_monthly_cents"]; ok {
		var v int64
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid spent_monthly_cents: %v", err), http.StatusBadRequest)
			return
		}
		req.SpentMonthlyCents = v
	}
	if raw, ok := fields["budget_reset_at"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid budget_reset_at: %v", err), http.StatusBadRequest)
			return
		}
		req.BudgetResetAt = v
		if v != "" {
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				httpResponse(w, fmt.Sprintf("invalid budget_reset_at: %v", err), http.StatusBadRequest)
				return
			}
		}
	}
	scheduleChanged := false
	resetDayChanged := false
	if raw, ok := fields["budget_period"]; ok {
		if err := json.Unmarshal(raw, &req.BudgetPeriod); err != nil {
			httpResponse(w, fmt.Sprintf("invalid budget_period: %v", err), http.StatusBadRequest)
			return
		}
		scheduleChanged = true
	}
	if raw, ok := fields["budget_reset_day"]; ok {
		if err := json.Unmarshal(raw, &req.BudgetResetDay); err != nil {
			httpResponse(w, fmt.Sprintf("invalid budget_reset_day: %v", err), http.StatusBadRequest)
			return
		}
		scheduleChanged = true
		resetDayChanged = true
	}
	if raw, ok := fields["budget_reset_time"]; ok {
		if err := json.Unmarshal(raw, &req.BudgetResetTime); err != nil {
			httpResponse(w, fmt.Sprintf("invalid budget_reset_time: %v", err), http.StatusBadRequest)
			return
		}
		scheduleChanged = true
	}
	if raw, ok := fields["budget_timezone"]; ok {
		if err := json.Unmarshal(raw, &req.BudgetTimezone); err != nil {
			httpResponse(w, fmt.Sprintf("invalid budget_timezone: %v", err), http.StatusBadRequest)
			return
		}
		scheduleChanged = true
	}
	if scheduleChanged {
		if req.BudgetPeriod == service.BudgetPeriodDaily && !resetDayChanged {
			req.BudgetResetDay = 0
		}
		schedule, err := service.NormalizeBudgetSchedule(req.BudgetSchedule)
		if err != nil {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.BudgetSchedule = schedule
		req.BudgetResetAt = ""
	}
	if raw, ok := fields["require_board_approval_for_new_agents"]; ok {
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid require_board_approval_for_new_agents: %v", err), http.StatusBadRequest)
			return
		}
		req.RequireBoardApproval = v
	}
	if raw, ok := fields["head_agent_id"]; ok {
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid head_agent_id: %v", err), http.StatusBadRequest)
			return
		}
		req.HeadAgentID = v
	}
	if raw, ok := fields["max_delegation_depth"]; ok {
		var v int
		if err := json.Unmarshal(raw, &v); err != nil {
			httpResponse(w, fmt.Sprintf("invalid max_delegation_depth: %v", err), http.StatusBadRequest)
			return
		}
		if v > 0 {
			req.MaxDelegationDepth = v
		}
	}
	if raw, ok := fields["canvas_layout"]; ok {
		if string(raw) == "null" {
			req.CanvasLayout = nil
		} else {
			req.CanvasLayout = json.RawMessage(raw)
		}
	}
	if raw, ok := fields["container_config"]; ok {
		if string(raw) == "null" {
			req.ContainerConfig = nil
		} else {
			var cfg service.ContainerConfig
			if err := json.Unmarshal(raw, &cfg); err != nil {
				httpResponse(w, fmt.Sprintf("invalid container_config: %v", err), http.StatusBadRequest)
				return
			}
			req.ContainerConfig = &cfg
		}
	}

	// Validate head_agent_id if being changed.
	if req.HeadAgentID != "" && req.HeadAgentID != existing.HeadAgentID {
		if s.orgAgentStore != nil {
			member, err := s.orgAgentStore.GetOrganizationAgentByPair(r.Context(), id, req.HeadAgentID)
			if err != nil {
				slog.Error("validate head agent failed", "org_id", id, "agent_id", req.HeadAgentID, "error", err)
				httpResponse(w, fmt.Sprintf("failed to validate head agent: %v", err), http.StatusInternalServerError)
				return
			}
			if member == nil {
				httpResponse(w, "head agent is not a member of this organization", http.StatusBadRequest)
				return
			}
			if member.Status != "active" {
				httpResponse(w, "head agent is not active", http.StatusBadRequest)
				return
			}
		}
	}

	req.UpdatedBy = s.getUserEmail(r)

	record, err := s.organizationStore.UpdateOrganization(r.Context(), id, req)
	if err != nil {
		slog.Error("update organization failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update organization: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("organization %q not found", id), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

func hasBudgetScheduleField(fields map[string]json.RawMessage) bool {
	for _, key := range []string{"budget_period", "budget_reset_day", "budget_reset_time", "budget_timezone"} {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}

// DeleteOrganizationAPI handles DELETE /api/v1/organizations/{id}.
func (s *Server) DeleteOrganizationAPI(w http.ResponseWriter, r *http.Request) {
	if s.organizationStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	id := r.PathValue("id")
	if id == "" {
		httpResponse(w, "organization id is required", http.StatusBadRequest)
		return
	}

	if err := s.organizationStore.DeleteOrganization(r.Context(), id); err != nil {
		slog.Error("delete organization failed", "id", id, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete organization: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "deleted", http.StatusOK)
}
