package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Usage Dashboard Endpoints ───
//
// All endpoints share a common query-parameter vocabulary parsed via parseUsageFilter:
//
//   from          = RFC3339 lower bound (inclusive)
//   to            = RFC3339 upper bound (exclusive)
//   status        = single status value ("ok" or "error")
//   provider      = repeated; any provider key in the set
//   model         = repeated
//   agent_id      = repeated
//   org_id        = repeated
//   project_id    = repeated
//   goal_id       = repeated
//   billing_code  = repeated
//
// Additional params:
//   group_by  (for /usage/grouped) = provider|model|agent|org|project|goal|billing_code|status
//   bucket    (for /usage/timeseries) = hour|day (default day)
//   limit     (for /usage/grouped)  = top-N cap; 0 means no cap

func parseUsageFilter(r *http.Request) service.UsageFilter {
	q := r.URL.Query()

	pickAll := func(keys ...string) []string {
		var out []string
		for _, k := range keys {
			out = append(out, q[k]...)
		}
		return out
	}

	return service.UsageFilter{
		From:         q.Get("from"),
		To:           q.Get("to"),
		Status:       q.Get("status"),
		Providers:    pickAll("provider"),
		Models:       pickAll("model"),
		AgentIDs:     pickAll("agent_id"),
		OrgIDs:       pickAll("org_id", "organization_id"),
		ProjectIDs:   pickAll("project_id"),
		GoalIDs:      pickAll("goal_id"),
		BillingCodes: pickAll("billing_code"),
	}
}

// GetUsageSummaryAPI handles GET /api/v1/usage/summary.
func (s *Server) GetUsageSummaryAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	filter := parseUsageFilter(r)
	sum, err := s.costEventStore.GetUsageSummary(r.Context(), filter)
	if err != nil {
		slog.Error("usage summary failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to get usage summary: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, sum, http.StatusOK)
}

// GetUsageGroupedAPI handles GET /api/v1/usage/grouped?group_by=provider&....
func (s *Server) GetUsageGroupedAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		httpResponse(w, "group_by is required (provider|model|agent|org|project|goal|billing_code|status)", http.StatusBadRequest)
		return
	}

	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	filter := parseUsageFilter(r)
	rows, err := s.costEventStore.GetUsageGrouped(r.Context(), filter, groupBy, limit)
	if err != nil {
		// invalid group_by is a 400, other errors are 500.
		if strings.Contains(err.Error(), "invalid group_by") {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		slog.Error("usage grouped failed", "group_by", groupBy, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get usage grouped: %v", err), http.StatusInternalServerError)
		return
	}

	if rows == nil {
		rows = []service.UsageSummary{}
	}
	httpResponseJSON(w, map[string]any{
		"group_by": groupBy,
		"data":     rows,
	}, http.StatusOK)
}

// GetUsageTimeSeriesAPI handles GET /api/v1/usage/timeseries?bucket=day&....
func (s *Server) GetUsageTimeSeriesAPI(w http.ResponseWriter, r *http.Request) {
	if s.costEventStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	bucket := r.URL.Query().Get("bucket")
	if bucket == "" {
		bucket = "day"
	}

	filter := parseUsageFilter(r)
	points, err := s.costEventStore.GetUsageTimeSeries(r.Context(), filter, bucket)
	if err != nil {
		if strings.Contains(err.Error(), "invalid bucket") {
			httpResponse(w, err.Error(), http.StatusBadRequest)
			return
		}
		slog.Error("usage timeseries failed", "bucket", bucket, "error", err)
		httpResponse(w, fmt.Sprintf("failed to get usage timeseries: %v", err), http.StatusInternalServerError)
		return
	}

	if points == nil {
		points = []service.UsageTimeSeriesPoint{}
	}
	httpResponseJSON(w, map[string]any{
		"bucket": bucket,
		"data":   points,
	}, http.StatusOK)
}

// GetUsageBudgetsAPI handles GET /api/v1/usage/budgets.
// Returns all configured agent budgets with their utilization percent computed.
func (s *Server) GetUsageBudgetsAPI(w http.ResponseWriter, r *http.Request) {
	if s.agentBudgetStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	budgets, err := s.agentBudgetStore.ListAgentBudgets(r.Context())
	if err != nil {
		slog.Error("list agent budgets failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list budgets: %v", err), http.StatusInternalServerError)
		return
	}

	out := make([]service.BudgetUtilization, 0, len(budgets))
	for _, b := range budgets {
		var name string
		if s.agentStore != nil {
			if agent, err := s.agentStore.GetAgent(r.Context(), b.AgentID); err == nil && agent != nil {
				name = agent.Name
			}
		}

		// Prefer live spend (SUM of agent_usage.estimated_cost) over stored current_spend.
		spend := b.CurrentSpend
		if total, err := s.agentBudgetStore.GetAgentTotalSpend(r.Context(), b.AgentID); err == nil {
			spend = total
		}

		pct := 0.0
		if b.MonthlyLimit > 0 {
			pct = (spend / b.MonthlyLimit) * 100
		}

		out = append(out, service.BudgetUtilization{
			AgentID:      b.AgentID,
			AgentName:    name,
			MonthlyLimit: b.MonthlyLimit,
			CurrentSpend: spend,
			PeriodStart:  b.PeriodStart,
			PeriodEnd:    b.PeriodEnd,
			UsagePercent: pct,
		})
	}

	httpResponseJSON(w, map[string]any{"data": out}, http.StatusOK)
}
