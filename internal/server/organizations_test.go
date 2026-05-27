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
