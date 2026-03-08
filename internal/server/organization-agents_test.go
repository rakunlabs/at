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
			if agent.Status != "" {
				m.agents[i].Status = agent.Status
			}
			if agent.ParentAgentID != "" {
				m.agents[i].ParentAgentID = agent.ParentAgentID
			}
			return &m.agents[i], nil
		}
	}
	return nil, nil
}

func (m *mockOrgAgentStore) DeleteOrganizationAgent(_ context.Context, _ string) error {
	return nil
}

func (m *mockOrgAgentStore) DeleteOrganizationAgentByPair(_ context.Context, _, _ string) error {
	return nil
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
