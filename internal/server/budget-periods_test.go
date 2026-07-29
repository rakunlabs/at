package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type periodCostStore struct {
	service.CostEventStorer
	spend       float64
	filter      service.UsageFilter
	calls       int
	recordCalls int
	recordErr   error
}

func (s *periodCostStore) GetUsageSummary(_ context.Context, filter service.UsageFilter) (service.UsageSummary, error) {
	s.filter = filter
	s.calls++
	return service.UsageSummary{CostCents: s.spend}, nil
}

func (s *periodCostStore) RecordCostEvent(context.Context, service.CostEvent) error {
	s.recordCalls++
	return s.recordErr
}

type periodAgentBudgetStore struct {
	service.AgentBudgetStorer
	budget     *service.AgentBudget
	set        service.AgentBudget
	usageCalls int
	usageErr   error
}

func (s *periodAgentBudgetStore) GetAgentBudget(context.Context, string) (*service.AgentBudget, error) {
	if s.budget == nil && s.set.AgentID != "" {
		copy := s.set
		return &copy, nil
	}
	return s.budget, nil
}

func (s *periodAgentBudgetStore) SetAgentBudget(_ context.Context, budget service.AgentBudget) error {
	s.set = budget
	return nil
}

func (s *periodAgentBudgetStore) ListAgentBudgets(context.Context) ([]service.AgentBudget, error) {
	if s.budget == nil {
		return nil, nil
	}
	return []service.AgentBudget{*s.budget}, nil
}

func (s *periodAgentBudgetStore) RecordAgentUsage(context.Context, service.AgentUsageRecord) error {
	s.usageCalls++
	return s.usageErr
}

func (s *periodAgentBudgetStore) ListModelPricing(context.Context) ([]service.ModelPricing, error) {
	return nil, nil
}

type periodOrganizationStore struct {
	service.OrganizationStorer
	org *service.Organization
}

func (s *periodOrganizationStore) GetOrganization(context.Context, string) (*service.Organization, error) {
	return s.org, nil
}

func TestAgentBudgetEnforcementUsesCurrentCostEventPeriod(t *testing.T) {
	costs := &periodCostStore{spend: 750}
	budgets := &periodAgentBudgetStore{budget: &service.AgentBudget{
		AgentID: "agent-1", MonthlyLimit: 5,
		BudgetSchedule: service.BudgetSchedule{BudgetPeriod: service.BudgetPeriodDaily, BudgetTimezone: "UTC"},
	}}
	s := &Server{agentBudgetStore: budgets, costEventStore: costs}

	err := s.checkBudgetFunc()(context.Background(), "agent-1")
	if err == nil || !strings.Contains(err.Error(), "exceeded budget") {
		t.Fatalf("checkBudgetFunc error = %v, want exceeded budget", err)
	}
	assertCurrentUsageFilter(t, costs.filter, "agent-1", "")
}

func TestRecordUsageWritesAuthoritativeCostEventFirst(t *testing.T) {
	costs := &periodCostStore{recordErr: errors.New("cost store down")}
	budgets := &periodAgentBudgetStore{}
	s := &Server{agentBudgetStore: budgets, costEventStore: costs}

	err := s.recordUsageFunc()(context.Background(), workflow.UsageEvent{AgentID: "agent-1"})
	if err == nil || !strings.Contains(err.Error(), "authoritative cost event") {
		t.Fatalf("recordUsageFunc error = %v", err)
	}
	if costs.recordCalls != 1 || budgets.usageCalls != 0 {
		t.Fatalf("record calls: costs=%d legacy=%d", costs.recordCalls, budgets.usageCalls)
	}
}

func TestRecordUsageLegacyFailureIsNonFatal(t *testing.T) {
	costs := &periodCostStore{}
	budgets := &periodAgentBudgetStore{usageErr: errors.New("legacy store down")}
	s := &Server{agentBudgetStore: budgets, costEventStore: costs}

	if err := s.recordUsageFunc()(context.Background(), workflow.UsageEvent{AgentID: "agent-1"}); err != nil {
		t.Fatalf("recordUsageFunc: %v", err)
	}
	if costs.recordCalls != 1 || budgets.usageCalls != 1 {
		t.Fatalf("record calls: costs=%d legacy=%d", costs.recordCalls, budgets.usageCalls)
	}
}

func TestAgentBudgetEnforcementNonpositiveLimitDisabled(t *testing.T) {
	costs := &periodCostStore{spend: 10000}
	s := &Server{
		agentBudgetStore: &periodAgentBudgetStore{budget: &service.AgentBudget{AgentID: "agent-1", MonthlyLimit: 0}},
		costEventStore:   costs,
	}

	if err := s.checkBudgetFunc()(context.Background(), "agent-1"); err != nil {
		t.Fatalf("checkBudgetFunc: %v", err)
	}
	if costs.calls != 0 {
		t.Fatalf("usage calls = %d, want 0", costs.calls)
	}
}

func TestOrganizationBudgetEnforcementAndStatusUseCurrentCostEventPeriod(t *testing.T) {
	org := &service.Organization{
		ID: "org-1", BudgetMonthlyCents: 500,
		BudgetSchedule: service.BudgetSchedule{
			BudgetPeriod: service.BudgetPeriodWeekly, BudgetResetDay: 1,
			BudgetResetTime: "09:00", BudgetTimezone: "America/New_York",
		},
	}
	costs := &periodCostStore{spend: 625}
	s := &Server{costEventStore: costs}

	if err := s.checkOrganizationBudget(context.Background(), org); err == nil {
		t.Fatal("checkOrganizationBudget error = nil, want exceeded budget")
	}
	assertCurrentUsageFilter(t, costs.filter, "", "org-1")

	status, err := s.organizationBudgetStatus(context.Background(), org, time.Now())
	if err != nil {
		t.Fatalf("organizationBudgetStatus: %v", err)
	}
	if status.SpendCents != 625 || status.RemainingCents != 0 || status.UsagePercent != 125 {
		t.Errorf("status = %+v", status)
	}
	if status.NextResetAt != status.PeriodEnd {
		t.Errorf("next_reset_at = %q, period_end = %q", status.NextResetAt, status.PeriodEnd)
	}
}

func TestOrganizationBudgetEnforcementNonpositiveLimitDisabled(t *testing.T) {
	costs := &periodCostStore{spend: 10000}
	s := &Server{costEventStore: costs}
	if err := s.checkOrganizationBudget(context.Background(), &service.Organization{ID: "org-1", BudgetMonthlyCents: -1}); err != nil {
		t.Fatalf("checkOrganizationBudget: %v", err)
	}
	if costs.calls != 0 {
		t.Fatalf("usage calls = %d, want 0", costs.calls)
	}
}

func TestSetAgentBudgetAPIDerivesResponse(t *testing.T) {
	costs := &periodCostStore{spend: 275}
	budgets := &periodAgentBudgetStore{}
	s := &Server{agentBudgetStore: budgets, costEventStore: costs}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-1/budget", strings.NewReader(`{
		"monthly_limit":10,"current_spend":999,"period_start":"2000-01-01T00:00:00Z",
		"budget_period":"daily","budget_reset_time":"08:00","budget_timezone":"UTC"
	}`))
	req.SetPathValue("id", "agent-1")
	w := httptest.NewRecorder()

	s.SetAgentBudgetAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var got service.AgentBudget
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.CurrentSpend != 2.75 || got.PeriodStart == "2000-01-01T00:00:00Z" {
		t.Errorf("derived budget = %+v", got)
	}
	if budgets.set.CurrentSpend != 2.75 || budgets.set.BudgetPeriod != service.BudgetPeriodDaily {
		t.Errorf("stored budget = %+v", budgets.set)
	}
	assertCurrentUsageFilter(t, costs.filter, "agent-1", "")
}

func TestSetAgentBudgetAPIRejectsInvalidSchedule(t *testing.T) {
	s := &Server{agentBudgetStore: &periodAgentBudgetStore{}, costEventStore: &periodCostStore{}}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-1/budget", strings.NewReader(`{
		"monthly_limit":10,"budget_period":"weekly","budget_reset_day":8
	}`))
	req.SetPathValue("id", "agent-1")
	w := httptest.NewRecorder()

	s.SetAgentBudgetAPI(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestSetAgentBudgetAPIRejectsNegativeLimit(t *testing.T) {
	s := &Server{agentBudgetStore: &periodAgentBudgetStore{}, costEventStore: &periodCostStore{}}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/agents/agent-1/budget", strings.NewReader(`{
		"monthly_limit":-1,"budget_period":"monthly"
	}`))
	req.SetPathValue("id", "agent-1")
	w := httptest.NewRecorder()

	s.SetAgentBudgetAPI(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestGetOrganizationBudgetAPI(t *testing.T) {
	org := &service.Organization{ID: "org-1", BudgetMonthlyCents: 1000}
	s := &Server{
		organizationStore: &periodOrganizationStore{org: org},
		costEventStore:    &periodCostStore{spend: 250},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations/org-1/budget", nil)
	req.SetPathValue("id", "org-1")
	w := httptest.NewRecorder()

	s.GetOrganizationBudgetAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var status service.BudgetStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if status.LimitCents != 1000 || status.SpendCents != 250 || status.RemainingCents != 750 || status.UsagePercent != 25 {
		t.Errorf("status = %+v", status)
	}
	if status.BudgetPeriod != service.BudgetPeriodMonthly || status.BudgetResetDay != 1 || status.BudgetResetTime != "00:00" || status.BudgetTimezone != "UTC" {
		t.Errorf("default schedule = %+v", status.BudgetSchedule)
	}
}

func TestGetUsageBudgetsAPIDerivesCurrentSpendAndPeriod(t *testing.T) {
	budget := &service.AgentBudget{
		AgentID: "agent-1", MonthlyLimit: 20,
		BudgetSchedule: service.BudgetSchedule{
			BudgetPeriod: service.BudgetPeriodDaily, BudgetResetTime: "08:00", BudgetTimezone: "UTC",
		},
	}
	costs := &periodCostStore{spend: 500}
	s := &Server{agentBudgetStore: &periodAgentBudgetStore{budget: budget}, costEventStore: costs}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage/budgets", nil)
	w := httptest.NewRecorder()

	s.GetUsageBudgetsAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data []service.BudgetUtilization `json:"data"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].CurrentSpend != 5 || response.Data[0].UsagePercent != 25 {
		t.Fatalf("data = %+v", response.Data)
	}
	if response.Data[0].PeriodStart == "" || response.Data[0].PeriodEnd == "" {
		t.Errorf("period not derived: %+v", response.Data[0])
	}
	assertCurrentUsageFilter(t, costs.filter, "agent-1", "")
}

func TestLegacyBudgetPeriods(t *testing.T) {
	now := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	t.Run("agent valid stored period", func(t *testing.T) {
		start, end, err := agentBudgetPeriod(&service.AgentBudget{
			PeriodStart: "2026-07-10T00:00:00Z", PeriodEnd: "2026-07-20T00:00:00Z",
		}, now)
		if err != nil || start.Format(time.RFC3339) != "2026-07-10T00:00:00Z" || end.Format(time.RFC3339) != "2026-07-20T00:00:00Z" {
			t.Fatalf("period = [%s, %s), error = %v", start, end, err)
		}
	})
	t.Run("agent stale stored period uses UTC month", func(t *testing.T) {
		start, end, err := agentBudgetPeriod(&service.AgentBudget{
			PeriodStart: "2026-06-01T00:00:00Z", PeriodEnd: "2026-07-01T00:00:00Z",
		}, now)
		if err != nil || start.Format(time.RFC3339) != "2026-07-01T00:00:00Z" || end.Format(time.RFC3339) != "2026-08-01T00:00:00Z" {
			t.Fatalf("period = [%s, %s), error = %v", start, end, err)
		}
	})
	t.Run("organization reset timestamp anchors legacy month", func(t *testing.T) {
		start, end, err := organizationBudgetPeriod(&service.Organization{
			BudgetResetAt: "2026-07-05T09:30:00Z",
		}, now)
		if err != nil || start.Format(time.RFC3339) != "2026-07-05T09:30:00Z" || end.Format(time.RFC3339) != "2026-08-05T09:30:00Z" {
			t.Fatalf("period = [%s, %s), error = %v", start, end, err)
		}
	})
	t.Run("organization old reset timestamp recurs in current month", func(t *testing.T) {
		start, end, err := organizationBudgetPeriod(&service.Organization{
			BudgetResetAt: "2026-06-05T09:30:00Z",
		}, now)
		if err != nil || start.Format(time.RFC3339) != "2026-07-05T09:30:00Z" || end.Format(time.RFC3339) != "2026-08-05T09:30:00Z" {
			t.Fatalf("period = [%s, %s), error = %v", start, end, err)
		}
	})
}

func assertCurrentUsageFilter(t *testing.T, filter service.UsageFilter, agentID, orgID string) {
	t.Helper()
	start, startErr := time.Parse(time.RFC3339, filter.From)
	end, endErr := time.Parse(time.RFC3339, filter.To)
	if startErr != nil || endErr != nil || !start.Before(end) || time.Now().Before(start) || !time.Now().Before(end) {
		t.Fatalf("filter period = [%q, %q), errors = %v, %v", filter.From, filter.To, startErr, endErr)
	}
	if agentID != "" && (len(filter.AgentIDs) != 1 || filter.AgentIDs[0] != agentID) {
		t.Errorf("agent filter = %v, want %q", filter.AgentIDs, agentID)
	}
	if orgID != "" && (len(filter.OrgIDs) != 1 || filter.OrgIDs[0] != orgID) {
		t.Errorf("org filter = %v, want %q", filter.OrgIDs, orgID)
	}
}
