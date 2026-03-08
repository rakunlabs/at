package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// GetAgentBudgetAPI handles GET /api/v1/agents/{id}/budget.
func (s *Server) GetAgentBudgetAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	record, err := s.agentBudgetStore.GetAgentBudget(r.Context(), agentID)
	if err != nil {
		slog.Error("get agent budget failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent budget: %v", err), http.StatusInternalServerError)
		return
	}

	if record == nil {
		httpResponse(w, fmt.Sprintf("budget for agent %q not found", agentID), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, record, http.StatusOK)
}

// SetAgentBudgetAPI handles PUT /api/v1/agents/{id}/budget.
func (s *Server) SetAgentBudgetAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	var req service.AgentBudget
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	req.AgentID = agentID

	if err := s.agentBudgetStore.SetAgentBudget(r.Context(), req); err != nil {
		slog.Error("set agent budget failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to set agent budget: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "budget updated", http.StatusOK)
}

// GetAgentUsageAPI handles GET /api/v1/agents/{id}/usage.
func (s *Server) GetAgentUsageAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	q, err := query.Parse(r.URL.RawQuery)
	if err != nil {
		httpResponse(w, fmt.Sprintf("invalid query: %v", err), http.StatusBadRequest)
		return
	}

	records, err := s.agentBudgetStore.GetAgentUsage(r.Context(), agentID, q)
	if err != nil {
		slog.Error("get agent usage failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent usage: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = &service.ListResult[service.AgentUsageRecord]{Data: []service.AgentUsageRecord{}}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// GetAgentSpendAPI handles GET /api/v1/agents/{id}/spend.
func (s *Server) GetAgentSpendAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		httpResponse(w, "agent id is required", http.StatusBadRequest)
		return
	}

	totalSpend, err := s.agentBudgetStore.GetAgentTotalSpend(r.Context(), agentID)
	if err != nil {
		slog.Error("get agent spend failed", "agent_id", agentID, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get agent spend: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, map[string]any{"agent_id": agentID, "total_spend": totalSpend}, http.StatusOK)
}

// ListModelPricingAPI handles GET /api/v1/model-pricing.
func (s *Server) ListModelPricingAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	records, err := s.agentBudgetStore.ListModelPricing(r.Context())
	if err != nil {
		slog.Error("list model pricing failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list model pricing: %v", err), http.StatusInternalServerError)
		return
	}

	if records == nil {
		records = []service.ModelPricing{}
	}

	httpResponseJSON(w, records, http.StatusOK)
}

// SetModelPricingAPI handles POST /api/v1/model-pricing.
func (s *Server) SetModelPricingAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req service.ModelPricing
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if err := s.agentBudgetStore.SetModelPricing(r.Context(), req); err != nil {
		slog.Error("set model pricing failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to set model pricing: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponse(w, "pricing updated", http.StatusOK)
}
