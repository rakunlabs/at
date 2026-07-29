package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestUpdateOrganization_PartialPreservesFieldsAndClearsHead(t *testing.T) {
	orgStore := &mockOrganizationStoreForAgentTests{orgs: map[string]*service.Organization{
		"org1": {
			ID:                   "org1",
			Name:                 "Org",
			Description:          "Before",
			IssuePrefix:          "ORG",
			BudgetMonthlyCents:   5000,
			SpentMonthlyCents:    123,
			RequireBoardApproval: true,
			HeadAgentID:          "agent-head",
			MaxDelegationDepth:   4,
			ContainerConfig: &service.ContainerConfig{
				Enabled: true,
				Image:   "runtime:latest",
				CPU:     "2",
				Memory:  "4g",
				Network: true,
			},
		},
	}}
	s := &Server{organizationStore: orgStore, orgAgentStore: &mockOrgAgentStore{}}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/organizations/org1", strings.NewReader(`{"head_agent_id":"","description":"After"}`))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.UpdateOrganizationAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	updated := orgStore.orgs["org1"]
	if updated.HeadAgentID != "" {
		t.Errorf("HeadAgentID: got %q, want empty", updated.HeadAgentID)
	}
	if updated.Description != "After" {
		t.Errorf("Description: got %q, want %q", updated.Description, "After")
	}
	if updated.BudgetMonthlyCents != 5000 || updated.SpentMonthlyCents != 123 || !updated.RequireBoardApproval || updated.MaxDelegationDepth != 4 {
		t.Errorf("partial update did not preserve org fields: %+v", updated)
	}
	if updated.ContainerConfig == nil || updated.ContainerConfig.Image != "runtime:latest" {
		t.Errorf("ContainerConfig not preserved: %+v", updated.ContainerConfig)
	}
}

func TestUpdateOrganization_BudgetSchedule(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus int
		want       service.BudgetSchedule
	}{
		{
			name:       "valid weekly schedule",
			body:       `{"budget_period":"weekly","budget_reset_day":5,"budget_reset_time":"17:30","budget_timezone":"America/Los_Angeles"}`,
			wantStatus: http.StatusOK,
			want: service.BudgetSchedule{
				BudgetPeriod: service.BudgetPeriodWeekly, BudgetResetDay: 5,
				BudgetResetTime: "17:30", BudgetTimezone: "America/Los_Angeles",
			},
		},
		{
			name:       "invalid timezone",
			body:       `{"budget_timezone":"not/a-zone"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid legacy reset timestamp",
			body:       `{"budget_reset_at":"tomorrow"}`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orgStore := &mockOrganizationStoreForAgentTests{orgs: map[string]*service.Organization{
				"org1": {ID: "org1", Name: "Org", BudgetMonthlyCents: 1000},
			}}
			s := &Server{organizationStore: orgStore}
			req := httptest.NewRequest(http.MethodPut, "/api/v1/organizations/org1", strings.NewReader(tt.body))
			req.SetPathValue("id", "org1")
			w := httptest.NewRecorder()

			s.UpdateOrganizationAPI(w, req)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantStatus == http.StatusOK && orgStore.orgs["org1"].BudgetSchedule != tt.want {
				t.Errorf("schedule = %+v, want %+v", orgStore.orgs["org1"].BudgetSchedule, tt.want)
			}
		})
	}
}

func TestUpdateOrganization_BudgetSchedulePartialPreservesLegacyAnchor(t *testing.T) {
	orgStore := &mockOrganizationStoreForAgentTests{orgs: map[string]*service.Organization{
		"org1": {
			ID: "org1", Name: "Org", BudgetMonthlyCents: 1000,
			BudgetResetAt: "2026-06-15T09:30:00Z",
		},
	}}
	s := &Server{organizationStore: orgStore}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organizations/org1", strings.NewReader(`{"budget_timezone":"Europe/Istanbul"}`))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()

	s.UpdateOrganizationAPI(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	got := orgStore.orgs["org1"]
	if got.BudgetPeriod != service.BudgetPeriodMonthly || got.BudgetResetDay != 15 || got.BudgetResetTime != "09:30" || got.BudgetTimezone != "Europe/Istanbul" {
		t.Fatalf("schedule = %+v", got.BudgetSchedule)
	}
	if got.BudgetResetAt != "" {
		t.Fatalf("legacy budget_reset_at = %q, want cleared", got.BudgetResetAt)
	}
}
