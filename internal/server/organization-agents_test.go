package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// --- Mock stores for hierarchy tests ---

type mockOrgAgentStore struct {
	agents []service.OrganizationAgent
}

func (m *mockOrgAgentStore) ListOrganizationAgents(_ context.Context, _ string) ([]service.OrganizationAgent, error) {
	return m.agents, nil
}

func (m *mockOrgAgentStore) ListAgentOrganizations(_ context.Context, _ string) ([]service.OrganizationAgent, error) {
	return nil, nil
}

func (m *mockOrgAgentStore) GetOrganizationAgent(_ context.Context, id string) (*service.OrganizationAgent, error) {
	for _, a := range m.agents {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, nil
}

func (m *mockOrgAgentStore) GetOrganizationAgentByPair(_ context.Context, orgID, agentID string) (*service.OrganizationAgent, error) {
	for _, a := range m.agents {
		if a.OrganizationID == orgID && a.AgentID == agentID {
			return &a, nil
		}
	}
	return nil, nil
}

func (m *mockOrgAgentStore) CreateOrganizationAgent(_ context.Context, agent service.OrganizationAgent) (*service.OrganizationAgent, error) {
	agent.ID = "new-id"
	m.agents = append(m.agents, agent)
	return &agent, nil
}

func (m *mockOrgAgentStore) UpdateOrganizationAgent(_ context.Context, id string, agent service.OrganizationAgent) (*service.OrganizationAgent, error) {
	for i, a := range m.agents {
		if a.ID == id {
			m.agents[i].Role = agent.Role
			m.agents[i].Title = agent.Title
			m.agents[i].ParentAgentID = agent.ParentAgentID
			m.agents[i].Status = agent.Status
			m.agents[i].HeartbeatSchedule = agent.HeartbeatSchedule
			return &m.agents[i], nil
		}
	}
	return nil, nil
}

func (m *mockOrgAgentStore) DeleteOrganizationAgent(_ context.Context, _ string) error {
	return nil
}

func (m *mockOrgAgentStore) DeleteOrganizationAgentByPair(_ context.Context, orgID, agentID string) error {
	for i, a := range m.agents {
		if a.OrganizationID == orgID && a.AgentID == agentID {
			m.agents = append(m.agents[:i], m.agents[i+1:]...)
			return nil
		}
	}
	return nil
}

type mockOrganizationStoreForAgentTests struct {
	orgs map[string]*service.Organization
}

func (m *mockOrganizationStoreForAgentTests) ListOrganizations(_ context.Context, _ *query.Query) (*service.ListResult[service.Organization], error) {
	return nil, nil
}

func (m *mockOrganizationStoreForAgentTests) GetOrganization(_ context.Context, id string) (*service.Organization, error) {
	if org, ok := m.orgs[id]; ok {
		cp := *org
		return &cp, nil
	}
	return nil, nil
}

func (m *mockOrganizationStoreForAgentTests) CreateOrganization(_ context.Context, org service.Organization) (*service.Organization, error) {
	return &org, nil
}

func (m *mockOrganizationStoreForAgentTests) UpdateOrganization(_ context.Context, id string, org service.Organization) (*service.Organization, error) {
	if m.orgs == nil {
		m.orgs = map[string]*service.Organization{}
	}
	org.ID = id
	m.orgs[id] = &org
	return &org, nil
}

func (m *mockOrganizationStoreForAgentTests) DeleteOrganization(_ context.Context, _ string) error {
	return nil
}

func (m *mockOrganizationStoreForAgentTests) IncrementIssueCounter(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

type mockApprovalStoreForAgentTests struct {
	approvals []service.Approval
}

func (m *mockApprovalStoreForAgentTests) ListApprovals(_ context.Context, _ *query.Query) (*service.ListResult[service.Approval], error) {
	return nil, nil
}

func (m *mockApprovalStoreForAgentTests) GetApproval(_ context.Context, id string) (*service.Approval, error) {
	for _, approval := range m.approvals {
		if approval.ID == id {
			cp := approval
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *mockApprovalStoreForAgentTests) CreateApproval(_ context.Context, approval service.Approval) (*service.Approval, error) {
	approval.ID = fmt.Sprintf("approval-%d", len(m.approvals)+1)
	m.approvals = append(m.approvals, approval)
	return &approval, nil
}

func (m *mockApprovalStoreForAgentTests) UpdateApproval(_ context.Context, id string, approval service.Approval) (*service.Approval, error) {
	for i := range m.approvals {
		if m.approvals[i].ID == id {
			approval.ID = id
			m.approvals[i] = approval
			return &m.approvals[i], nil
		}
	}
	return nil, nil
}

func (m *mockApprovalStoreForAgentTests) ListPendingApprovals(_ context.Context, orgID string) ([]service.Approval, error) {
	var out []service.Approval
	for _, approval := range m.approvals {
		if approval.Status == service.ApprovalStatusPending && (orgID == "" || approval.OrganizationID == orgID) {
			out = append(out, approval)
		}
	}
	return out, nil
}

// --- Hierarchy validation tests ---

func TestValidateHierarchy_RootNode(t *testing.T) {
	s := &Server{
		orgAgentStore: &mockOrgAgentStore{
			agents: []service.OrganizationAgent{
				{ID: "1", OrganizationID: "org1", AgentID: "A"},
			},
		},
	}

	err := s.validateHierarchy(context.Background(), "org1", "B", "")
	if err != nil {
		t.Fatalf("empty parent should always succeed, got: %v", err)
	}
}

func TestValidateHierarchy_ValidParent(t *testing.T) {
	s := &Server{
		orgAgentStore: &mockOrgAgentStore{
			agents: []service.OrganizationAgent{
				{ID: "1", OrganizationID: "org1", AgentID: "A"},
				{ID: "2", OrganizationID: "org1", AgentID: "B"},
			},
		},
	}

	err := s.validateHierarchy(context.Background(), "org1", "B", "A")
	if err != nil {
		t.Fatalf("parent A is in org, should succeed, got: %v", err)
	}
}

func TestValidateHierarchy_ParentNotInOrg(t *testing.T) {
	s := &Server{
		orgAgentStore: &mockOrgAgentStore{
			agents: []service.OrganizationAgent{
				{ID: "1", OrganizationID: "org1", AgentID: "A"},
			},
		},
	}

	err := s.validateHierarchy(context.Background(), "org1", "A", "X")
	if err == nil {
		t.Fatal("parent X is not in org, should fail")
	}
	if !strings.Contains(err.Error(), "not a member") {
		t.Fatalf("expected 'not a member' error, got: %v", err)
	}
}

func TestValidateHierarchy_SelfReference(t *testing.T) {
	s := &Server{
		orgAgentStore: &mockOrgAgentStore{
			agents: []service.OrganizationAgent{
				{ID: "1", OrganizationID: "org1", AgentID: "A"},
			},
		},
	}

	err := s.validateHierarchy(context.Background(), "org1", "A", "A")
	if err == nil {
		t.Fatal("self-referencing parent should fail")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected 'cycle' error, got: %v", err)
	}
}

func TestValidateHierarchy_DirectCycle(t *testing.T) {
	// A's parent is B, now try to set B's parent to A → cycle
	s := &Server{
		orgAgentStore: &mockOrgAgentStore{
			agents: []service.OrganizationAgent{
				{ID: "1", OrganizationID: "org1", AgentID: "A", ParentAgentID: "B"},
				{ID: "2", OrganizationID: "org1", AgentID: "B"},
			},
		},
	}

	err := s.validateHierarchy(context.Background(), "org1", "B", "A")
	if err == nil {
		t.Fatal("A→B→A cycle should fail")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected 'cycle' error, got: %v", err)
	}
}

func TestValidateHierarchy_DeeperCycle(t *testing.T) {
	// A is root, B→A, C→B. Now try to set A's parent to C → A→C→B→A cycle
	s := &Server{
		orgAgentStore: &mockOrgAgentStore{
			agents: []service.OrganizationAgent{
				{ID: "1", OrganizationID: "org1", AgentID: "A", ParentAgentID: ""},
				{ID: "2", OrganizationID: "org1", AgentID: "B", ParentAgentID: "A"},
				{ID: "3", OrganizationID: "org1", AgentID: "C", ParentAgentID: "B"},
			},
		},
	}

	err := s.validateHierarchy(context.Background(), "org1", "A", "C")
	if err == nil {
		t.Fatal("A→C→B→A cycle should fail")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected 'cycle' error, got: %v", err)
	}
}

func TestAddAgentToOrg_HierarchyValidation(t *testing.T) {
	store := &mockOrgAgentStore{
		agents: []service.OrganizationAgent{
			{ID: "1", OrganizationID: "org1", AgentID: "A"},
		},
	}
	s := &Server{orgAgentStore: store}

	// Try to add agent B with parent X (not in org) — should fail 400
	body := `{"agent_id":"B","parent_agent_id":"X"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/agents", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.AddAgentToOrganizationAPI(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp responseMessage
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp.Message, "hierarchy") {
		t.Fatalf("expected hierarchy error, got: %s", resp.Message)
	}
}

func TestUpdateOrgAgent_HierarchyValidation(t *testing.T) {
	store := &mockOrgAgentStore{
		agents: []service.OrganizationAgent{
			{ID: "1", OrganizationID: "org1", AgentID: "A", ParentAgentID: "B", Status: "active"},
			{ID: "2", OrganizationID: "org1", AgentID: "B", Status: "active"},
		},
	}
	s := &Server{orgAgentStore: store}

	// Try to set B's parent to A (creating A→B→A cycle) — should fail 400
	body := `{"parent_agent_id":"A"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organizations/org1/agents/B", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	req.SetPathValue("agent_id", "B")
	w := httptest.NewRecorder()
	s.UpdateOrganizationAgentAPI(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var resp responseMessage
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !strings.Contains(resp.Message, "hierarchy") {
		t.Fatalf("expected hierarchy error, got: %s", resp.Message)
	}
}

func TestUpdateOrgAgent_PartialPreservesFieldsAndClearsParent(t *testing.T) {
	store := &mockOrgAgentStore{
		agents: []service.OrganizationAgent{
			{
				ID:                "1",
				OrganizationID:    "org1",
				AgentID:           "A",
				Role:              "reviewer",
				Title:             "Lead",
				ParentAgentID:     "B",
				Status:            "paused",
				HeartbeatSchedule: "*/5 * * * *",
			},
			{ID: "2", OrganizationID: "org1", AgentID: "B", Status: "active"},
		},
	}
	s := &Server{orgAgentStore: store}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/organizations/org1/agents/A", strings.NewReader(`{"parent_agent_id":""}`))
	req.SetPathValue("id", "org1")
	req.SetPathValue("agent_id", "A")
	w := httptest.NewRecorder()
	s.UpdateOrganizationAgentAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	updated := store.agents[0]
	if updated.ParentAgentID != "" {
		t.Errorf("ParentAgentID: got %q, want empty", updated.ParentAgentID)
	}
	if updated.Role != "reviewer" || updated.Title != "Lead" || updated.Status != "paused" || updated.HeartbeatSchedule != "*/5 * * * *" {
		t.Errorf("fields not preserved: %+v", updated)
	}
}

func TestRemoveAgentFromOrg_ClearsHeadAgent(t *testing.T) {
	orgStore := &mockOrganizationStoreForAgentTests{orgs: map[string]*service.Organization{
		"org1": {ID: "org1", Name: "Org", HeadAgentID: "A"},
	}}
	agentStore := &mockOrgAgentStore{agents: []service.OrganizationAgent{
		{ID: "1", OrganizationID: "org1", AgentID: "A", Status: "active"},
	}}
	s := &Server{orgAgentStore: agentStore, organizationStore: orgStore}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/organizations/org1/agents/A", nil)
	req.SetPathValue("id", "org1")
	req.SetPathValue("agent_id", "A")
	w := httptest.NewRecorder()
	s.RemoveAgentFromOrganizationAPI(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if orgStore.orgs["org1"].HeadAgentID != "" {
		t.Errorf("HeadAgentID: got %q, want empty", orgStore.orgs["org1"].HeadAgentID)
	}
	if len(agentStore.agents) != 0 {
		t.Errorf("membership was not removed: %+v", agentStore.agents)
	}
}

func TestAddAgentToOrg_RequiresApproval(t *testing.T) {
	orgStore := &mockOrganizationStoreForAgentTests{orgs: map[string]*service.Organization{
		"org1": {ID: "org1", Name: "Org", RequireBoardApproval: true},
	}}
	agentStore := &mockOrgAgentStore{agents: []service.OrganizationAgent{}}
	approvalStore := &mockApprovalStoreForAgentTests{}
	s := &Server{orgAgentStore: agentStore, organizationStore: orgStore, approvalStore: approvalStore}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/agents", strings.NewReader(`{"agent_id":"A","role":"worker"}`))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.AddAgentToOrganizationAPI(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
	if len(agentStore.agents) != 0 {
		t.Fatalf("membership should wait for approval, got %+v", agentStore.agents)
	}
	if len(approvalStore.approvals) != 1 {
		t.Fatalf("expected 1 approval, got %d", len(approvalStore.approvals))
	}
	approval := approvalStore.approvals[0]
	if approval.Type != service.ApprovalTypeHireAgent || approval.Status != service.ApprovalStatusPending {
		t.Errorf("approval: got type=%q status=%q", approval.Type, approval.Status)
	}
	if stringArg(approval.RequestDetails, "agent_id") != "A" || stringArg(approval.RequestDetails, "role") != "worker" {
		t.Errorf("approval details: %+v", approval.RequestDetails)
	}
}

func TestAddAgentToOrg_ValidParent(t *testing.T) {
	store := &mockOrgAgentStore{
		agents: []service.OrganizationAgent{
			{ID: "1", OrganizationID: "org1", AgentID: "A"},
		},
	}
	s := &Server{orgAgentStore: store}

	// Add agent B with parent A (valid) — should succeed 201
	body := `{"agent_id":"B","parent_agent_id":"A"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/agents", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.AddAgentToOrganizationAPI(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

// Silence the "imported and not used" warning
var _ = fmt.Sprintf
