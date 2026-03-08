package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// --- Mock stores for task intake tests ---

type mockOrganizationStore struct {
	orgs map[string]*service.Organization
}

func (m *mockOrganizationStore) GetOrganization(_ context.Context, id string) (*service.Organization, error) {
	if org, ok := m.orgs[id]; ok {
		return org, nil
	}
	return nil, nil
}

func (m *mockOrganizationStore) IncrementIssueCounter(_ context.Context, orgID string) (int64, error) {
	if org, ok := m.orgs[orgID]; ok {
		org.IssueCounter++
		return org.IssueCounter, nil
	}
	return 0, nil
}

// Unused methods to satisfy the interface.
func (m *mockOrganizationStore) ListOrganizations(_ context.Context, _ *query.Query) (*service.ListResult[service.Organization], error) {
	return nil, nil
}
func (m *mockOrganizationStore) CreateOrganization(_ context.Context, _ service.Organization) (*service.Organization, error) {
	return nil, nil
}
func (m *mockOrganizationStore) UpdateOrganization(_ context.Context, _ string, _ service.Organization) (*service.Organization, error) {
	return nil, nil
}
func (m *mockOrganizationStore) DeleteOrganization(_ context.Context, _ string) error { return nil }

type mockTaskStore struct {
	tasks []service.Task
}

func (m *mockTaskStore) CreateTask(_ context.Context, task service.Task) (*service.Task, error) {
	task.ID = "task-" + task.Identifier
	m.tasks = append(m.tasks, task)
	return &task, nil
}

// Unused methods — stubs for interface compliance.
func (m *mockTaskStore) ListTasks(_ context.Context, _ *query.Query) (*service.ListResult[service.Task], error) {
	return nil, nil
}
func (m *mockTaskStore) GetTask(_ context.Context, _ string) (*service.Task, error) { return nil, nil }
func (m *mockTaskStore) UpdateTask(_ context.Context, _ string, _ service.Task) (*service.Task, error) {
	return nil, nil
}
func (m *mockTaskStore) DeleteTask(_ context.Context, _ string) error      { return nil }
func (m *mockTaskStore) CheckoutTask(_ context.Context, _, _ string) error { return nil }
func (m *mockTaskStore) ReleaseTask(_ context.Context, _ string) error     { return nil }
func (m *mockTaskStore) ListTasksByAgent(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}
func (m *mockTaskStore) ListTasksByGoal(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}
func (m *mockTaskStore) ListChildTasks(_ context.Context, _ string) ([]service.Task, error) {
	return nil, nil
}
func (m *mockTaskStore) UpdateTaskStatus(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

// --- Intake endpoint tests ---

func TestIntakeTask_ValidOrgAndHeadAgent(t *testing.T) {
	orgStore := &mockOrganizationStore{
		orgs: map[string]*service.Organization{
			"org1": {
				ID:          "org1",
				HeadAgentID: "agent-head",
				IssuePrefix: "PAP",
			},
		},
	}
	agentStore := &mockOrgAgentStore{
		agents: []service.OrganizationAgent{
			{ID: "m1", OrganizationID: "org1", AgentID: "agent-head", Status: "active"},
		},
	}
	taskStore := &mockTaskStore{}
	s := &Server{
		organizationStore: orgStore,
		orgAgentStore:     agentStore,
		taskStore:         taskStore,
	}

	body := `{"title":"Fix login bug","description":"Login fails on Safari"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/tasks", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.IntakeTaskAPI(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp intakeTaskResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if resp.Identifier != "PAP-1" {
		t.Fatalf("expected identifier PAP-1, got %q", resp.Identifier)
	}
	if resp.Status != service.TaskStatusOpen {
		t.Fatalf("expected status %q, got %q", service.TaskStatusOpen, resp.Status)
	}

	// Verify task was created with correct fields.
	if len(taskStore.tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(taskStore.tasks))
	}
	task := taskStore.tasks[0]
	if task.OrganizationID != "org1" {
		t.Fatalf("expected org org1, got %q", task.OrganizationID)
	}
	if task.AssignedAgentID != "agent-head" {
		t.Fatalf("expected assigned to agent-head, got %q", task.AssignedAgentID)
	}
	if task.RequestDepth != 0 {
		t.Fatalf("expected request_depth 0, got %d", task.RequestDepth)
	}
}

func TestIntakeTask_NonExistentOrg(t *testing.T) {
	orgStore := &mockOrganizationStore{orgs: map[string]*service.Organization{}}
	s := &Server{
		organizationStore: orgStore,
		orgAgentStore:     &mockOrgAgentStore{},
		taskStore:         &mockTaskStore{},
	}

	body := `{"title":"Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/missing/tasks", strings.NewReader(body))
	req.SetPathValue("id", "missing")
	w := httptest.NewRecorder()
	s.IntakeTaskAPI(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIntakeTask_NoHeadAgent(t *testing.T) {
	orgStore := &mockOrganizationStore{
		orgs: map[string]*service.Organization{
			"org1": {ID: "org1", HeadAgentID: "", IssuePrefix: "X"},
		},
	}
	s := &Server{
		organizationStore: orgStore,
		orgAgentStore:     &mockOrgAgentStore{},
		taskStore:         &mockTaskStore{},
	}

	body := `{"title":"Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/tasks", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.IntakeTaskAPI(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIntakeTask_HeadAgentInactive(t *testing.T) {
	orgStore := &mockOrganizationStore{
		orgs: map[string]*service.Organization{
			"org1": {ID: "org1", HeadAgentID: "agent-head", IssuePrefix: "X"},
		},
	}
	agentStore := &mockOrgAgentStore{
		agents: []service.OrganizationAgent{
			{ID: "m1", OrganizationID: "org1", AgentID: "agent-head", Status: "inactive"},
		},
	}
	s := &Server{
		organizationStore: orgStore,
		orgAgentStore:     agentStore,
		taskStore:         &mockTaskStore{},
	}

	body := `{"title":"Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/tasks", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.IntakeTaskAPI(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}

func TestIntakeTask_IdentifierFormat(t *testing.T) {
	orgStore := &mockOrganizationStore{
		orgs: map[string]*service.Organization{
			"org1": {ID: "org1", HeadAgentID: "agent-head", IssuePrefix: "PAP", IssueCounter: 41},
		},
	}
	agentStore := &mockOrgAgentStore{
		agents: []service.OrganizationAgent{
			{ID: "m1", OrganizationID: "org1", AgentID: "agent-head", Status: "active"},
		},
	}
	s := &Server{
		organizationStore: orgStore,
		orgAgentStore:     agentStore,
		taskStore:         &mockTaskStore{},
	}

	body := `{"title":"Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/tasks", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.IntakeTaskAPI(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var resp intakeTaskResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Identifier != "PAP-42" {
		t.Fatalf("expected identifier PAP-42 (counter was 41, incremented to 42), got %q", resp.Identifier)
	}
}

func TestIntakeTask_HeadAgentNotMember(t *testing.T) {
	orgStore := &mockOrganizationStore{
		orgs: map[string]*service.Organization{
			"org1": {ID: "org1", HeadAgentID: "agent-gone", IssuePrefix: "X"},
		},
	}
	// agent-gone is not in the agent store.
	agentStore := &mockOrgAgentStore{agents: []service.OrganizationAgent{}}
	s := &Server{
		organizationStore: orgStore,
		orgAgentStore:     agentStore,
		taskStore:         &mockTaskStore{},
	}

	body := `{"title":"Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations/org1/tasks", strings.NewReader(body))
	req.SetPathValue("id", "org1")
	w := httptest.NewRecorder()
	s.IntakeTaskAPI(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
}
