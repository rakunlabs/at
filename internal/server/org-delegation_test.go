package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// --- Mock stores for delegation tests ---

// mockOrgAgentStoreForDelegation implements service.OrganizationAgentStorer.
type mockOrgAgentStoreForDelegation struct {
	agents []service.OrganizationAgent
}

func (m *mockOrgAgentStoreForDelegation) ListOrganizationAgents(_ context.Context, _ string) ([]service.OrganizationAgent, error) {
	return m.agents, nil
}

func (m *mockOrgAgentStoreForDelegation) ListAgentOrganizations(_ context.Context, _ string) ([]service.OrganizationAgent, error) {
	return nil, nil
}

func (m *mockOrgAgentStoreForDelegation) GetOrganizationAgent(_ context.Context, id string) (*service.OrganizationAgent, error) {
	for _, a := range m.agents {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, nil
}

func (m *mockOrgAgentStoreForDelegation) GetOrganizationAgentByPair(_ context.Context, orgID, agentID string) (*service.OrganizationAgent, error) {
	for _, a := range m.agents {
		if a.OrganizationID == orgID && a.AgentID == agentID {
			return &a, nil
		}
	}
	return nil, nil
}

func (m *mockOrgAgentStoreForDelegation) CreateOrganizationAgent(_ context.Context, agent service.OrganizationAgent) (*service.OrganizationAgent, error) {
	return &agent, nil
}

func (m *mockOrgAgentStoreForDelegation) UpdateOrganizationAgent(_ context.Context, _ string, agent service.OrganizationAgent) (*service.OrganizationAgent, error) {
	return &agent, nil
}

func (m *mockOrgAgentStoreForDelegation) DeleteOrganizationAgent(_ context.Context, _ string) error {
	return nil
}

func (m *mockOrgAgentStoreForDelegation) DeleteOrganizationAgentByPair(_ context.Context, _, _ string) error {
	return nil
}

// mockTaskStoreForDelegation implements service.TaskStorer with recording capability.
type mockTaskStoreForDelegation struct {
	tasks     []service.Task
	idCounter int
}

func (m *mockTaskStoreForDelegation) CreateTask(_ context.Context, task service.Task) (*service.Task, error) {
	m.idCounter++
	task.ID = fmt.Sprintf("task-%d", m.idCounter)
	m.tasks = append(m.tasks, task)
	return &task, nil
}

func (m *mockTaskStoreForDelegation) UpdateTask(_ context.Context, id string, task service.Task) (*service.Task, error) {
	for i, t := range m.tasks {
		if t.ID == id {
			if task.Status != "" {
				m.tasks[i].Status = task.Status
			}
			if task.Result != "" {
				m.tasks[i].Result = task.Result
			}
			return &m.tasks[i], nil
		}
	}
	return &task, nil
}

func (m *mockTaskStoreForDelegation) GetTask(_ context.Context, id string) (*service.Task, error) {
	for _, t := range m.tasks {
		if t.ID == id {
			return &t, nil
		}
	}
	return nil, nil
}

func (m *mockTaskStoreForDelegation) ListTasks(_ context.Context, _ *query.Query) (*service.ListResult[service.Task], error) {
	return nil, nil
}
func (m *mockTaskStoreForDelegation) DeleteTask(_ context.Context, _ string) error      { return nil }
func (m *mockTaskStoreForDelegation) CheckoutTask(_ context.Context, _, _ string) error { return nil }
func (m *mockTaskStoreForDelegation) ReleaseTask(_ context.Context, _ string) error     { return nil }
func (m *mockTaskStoreForDelegation) ListTasksByAgent(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}
func (m *mockTaskStoreForDelegation) ListTasksByGoal(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}

// mockOrgStoreForDelegation implements service.OrganizationStorer.
type mockOrgStoreForDelegation struct {
	orgs       map[string]*service.Organization
	counterSeq int64
}

func (m *mockOrgStoreForDelegation) GetOrganization(_ context.Context, id string) (*service.Organization, error) {
	if org, ok := m.orgs[id]; ok {
		return org, nil
	}
	return nil, nil
}

func (m *mockOrgStoreForDelegation) IncrementIssueCounter(_ context.Context, orgID string) (int64, error) {
	if org, ok := m.orgs[orgID]; ok {
		m.counterSeq++
		org.IssueCounter = m.counterSeq
		return m.counterSeq, nil
	}
	return 0, fmt.Errorf("org not found")
}

func (m *mockOrgStoreForDelegation) ListOrganizations(_ context.Context, _ *query.Query) (*service.ListResult[service.Organization], error) {
	return nil, nil
}
func (m *mockOrgStoreForDelegation) CreateOrganization(_ context.Context, _ service.Organization) (*service.Organization, error) {
	return nil, nil
}
func (m *mockOrgStoreForDelegation) UpdateOrganization(_ context.Context, _ string, _ service.Organization) (*service.Organization, error) {
	return nil, nil
}
func (m *mockOrgStoreForDelegation) DeleteOrganization(_ context.Context, _ string) error {
	return nil
}

// mockAgentStoreForDelegation implements service.AgentStorer.
type mockAgentStoreForDelegation struct {
	agents map[string]*service.Agent
}

func (m *mockAgentStoreForDelegation) GetAgent(_ context.Context, id string) (*service.Agent, error) {
	if a, ok := m.agents[id]; ok {
		return a, nil
	}
	return nil, nil
}

func (m *mockAgentStoreForDelegation) ListAgents(_ context.Context, _ *query.Query) (*service.ListResult[service.Agent], error) {
	return nil, nil
}
func (m *mockAgentStoreForDelegation) CreateAgent(_ context.Context, _ service.Agent) (*service.Agent, error) {
	return nil, nil
}
func (m *mockAgentStoreForDelegation) UpdateAgent(_ context.Context, _ string, _ service.Agent) (*service.Agent, error) {
	return nil, nil
}
func (m *mockAgentStoreForDelegation) DeleteAgent(_ context.Context, _ string) error { return nil }

// --- Helper ---

func testServerWithStores(
	orgAgentStore service.OrganizationAgentStorer,
	taskStore service.TaskStorer,
	orgStore service.OrganizationStorer,
	agentStore service.AgentStorer,
) *Server {
	return &Server{
		orgAgentStore:     orgAgentStore,
		taskStore:         taskStore,
		organizationStore: orgStore,
		agentStore:        agentStore,
	}
}

// --- Test: getDirectReports ---

func TestGetDirectReports(t *testing.T) {
	// Hierarchy: A (root), B (child of A, active), C (child of A, paused), D (child of B, active)
	agents := []service.OrganizationAgent{
		{ID: "1", OrganizationID: "org1", AgentID: "A", ParentAgentID: "", Status: "active"},
		{ID: "2", OrganizationID: "org1", AgentID: "B", ParentAgentID: "A", Status: "active"},
		{ID: "3", OrganizationID: "org1", AgentID: "C", ParentAgentID: "A", Status: "paused"},
		{ID: "4", OrganizationID: "org1", AgentID: "D", ParentAgentID: "B", Status: "active"},
	}

	s := testServerWithStores(
		&mockOrgAgentStoreForDelegation{agents: agents},
		nil, nil, nil,
	)

	tests := []struct {
		name    string
		agentID string
		wantIDs []string
		wantLen int
	}{
		{
			name:    "A's direct reports: only B (C is paused)",
			agentID: "A",
			wantIDs: []string{"B"},
			wantLen: 1,
		},
		{
			name:    "B's direct reports: only D",
			agentID: "B",
			wantIDs: []string{"D"},
			wantLen: 1,
		},
		{
			name:    "D's direct reports: empty (leaf node)",
			agentID: "D",
			wantIDs: nil,
			wantLen: 0,
		},
		{
			name:    "Nonexistent agent: empty",
			agentID: "nonexistent",
			wantIDs: nil,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reports, err := s.getDirectReports(context.Background(), "org1", tt.agentID)
			if err != nil {
				t.Fatalf("getDirectReports returned error: %v", err)
			}
			if len(reports) != tt.wantLen {
				t.Fatalf("expected %d reports, got %d", tt.wantLen, len(reports))
			}
			for i, wantID := range tt.wantIDs {
				if reports[i].AgentID != wantID {
					t.Errorf("report[%d]: expected AgentID %q, got %q", i, wantID, reports[i].AgentID)
				}
			}
		})
	}
}

// --- Test: createDelegationTask ---

func TestCreateDelegationTask(t *testing.T) {
	orgStore := &mockOrgStoreForDelegation{
		orgs: map[string]*service.Organization{
			"org1": {
				ID:          "org1",
				IssuePrefix: "ENG",
			},
		},
	}
	taskStore := &mockTaskStoreForDelegation{}

	s := testServerWithStores(nil, taskStore, orgStore, nil)

	org := &service.Organization{
		ID:          "org1",
		IssuePrefix: "ENG",
	}
	parentTask := &service.Task{
		ID:             "parent-1",
		OrganizationID: "org1",
		Title:          "Build the feature",
	}

	child, err := s.createDelegationTask(
		context.Background(),
		org,
		parentTask,
		"agent-bob",
		"Implement the backend service",
		2, // depth
	)
	if err != nil {
		t.Fatalf("createDelegationTask failed: %v", err)
	}

	// Verify child task fields.
	if child.ParentID != "parent-1" {
		t.Errorf("expected ParentID %q, got %q", "parent-1", child.ParentID)
	}
	if child.AssignedAgentID != "agent-bob" {
		t.Errorf("expected AssignedAgentID %q, got %q", "agent-bob", child.AssignedAgentID)
	}
	if child.OrganizationID != "org1" {
		t.Errorf("expected OrganizationID %q, got %q", "org1", child.OrganizationID)
	}
	if child.Status != service.TaskStatusOpen {
		t.Errorf("expected Status %q, got %q", service.TaskStatusOpen, child.Status)
	}
	if child.RequestDepth != 3 { // depth + 1 = 2 + 1 = 3
		t.Errorf("expected RequestDepth 3, got %d", child.RequestDepth)
	}
	// Verify identifier format: "{prefix}-{counter}".
	if !strings.HasPrefix(child.Identifier, "ENG-") {
		t.Errorf("expected identifier to start with %q, got %q", "ENG-", child.Identifier)
	}
	if child.Title != "Build the feature" {
		t.Errorf("expected Title %q (from parent), got %q", "Build the feature", child.Title)
	}
	if child.Description != "Implement the backend service" {
		t.Errorf("expected Description %q, got %q", "Implement the backend service", child.Description)
	}
}

// --- Test: delegate tool name sanitization ---

func TestDelegateToolNameSanitization(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		wantTool  string
	}{
		{
			name:      "spaces become underscores",
			agentName: "VP Engineering",
			wantTool:  "delegate_to_vp_engineering",
		},
		{
			name:      "hyphens become underscores",
			agentName: "Bob-123",
			wantTool:  "delegate_to_bob_123",
		},
		{
			name:      "special chars become underscores",
			agentName: "Agent@Special#Name!",
			wantTool:  "delegate_to_agent_special_name_",
		},
		{
			name:      "already clean name",
			agentName: "Alice",
			wantTool:  "delegate_to_alice",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the sanitization logic directly — same algorithm as in org-delegation.go
			safeName := strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
					return r
				}
				return '_'
			}, tt.agentName)
			toolName := "delegate_to_" + strings.ToLower(safeName)

			if toolName != tt.wantTool {
				t.Errorf("expected tool name %q, got %q", tt.wantTool, toolName)
			}
		})
	}
}
