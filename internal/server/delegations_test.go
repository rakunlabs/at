package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestTryRegisterDelegationConcurrent(t *testing.T) {
	s := &Server{}
	const attempts = 32

	start := make(chan struct{})
	release := make(chan struct{})
	results := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, cleanup, err := s.tryRegisterDelegation(context.Background(), "task-1", "agent-1", "org-1")
			results <- err
			if err == nil {
				<-release
				cleanup()
			}
		}()
	}

	close(start)
	successes := 0
	for range attempts {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		if !errors.Is(err, errDelegationAlreadyRunning) {
			t.Fatalf("unexpected registration error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful registrations = %d, want 1", successes)
	}

	close(release)
	wg.Wait()
	if s.isDelegationActive("task-1") {
		t.Fatal("registration remained active after cleanup")
	}
}

func TestDelegationCleanupDoesNotDeleteNewerRegistration(t *testing.T) {
	s := &Server{}
	oldCtx, oldCleanup, err := s.tryRegisterDelegation(context.Background(), "task-1", "agent-old", "org-1")
	if err != nil {
		t.Fatalf("register old delegation: %v", err)
	}
	oldValue, _ := s.activeDelegations.Load("task-1")
	if !s.activeDelegations.CompareAndDelete("task-1", oldValue) {
		t.Fatal("remove old registration")
	}

	newCtx, newCleanup, err := s.tryRegisterDelegation(context.Background(), "task-1", "agent-new", "org-1")
	if err != nil {
		t.Fatalf("register new delegation: %v", err)
	}
	defer newCleanup()
	newValue, _ := s.activeDelegations.Load("task-1")

	oldCleanup()
	if got, ok := s.activeDelegations.Load("task-1"); !ok || got != newValue {
		t.Fatal("old cleanup removed the newer registration")
	}
	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("old cleanup did not cancel its own context")
	}
	select {
	case <-newCtx.Done():
		t.Fatal("old cleanup cancelled the newer context")
	default:
	}
}

func TestStartDelegationRunPersistsFailureBeforeCallback(t *testing.T) {
	task := service.Task{ID: "task-1", Status: service.TaskStatusTodo}
	taskStore := &mockTaskStoreForDelegation{tasks: []service.Task{task}}
	s := &Server{ctx: context.Background(), taskStore: taskStore}
	installRuntimeFixture(t, s)
	org := &service.Organization{ID: "org-1"}

	type completion struct {
		traceID string
		err     error
		status  string
		result  string
	}
	done := make(chan completion, 1)
	err := s.startDelegationRun(s.ctx, org, &task, "agent-1", 0, func(ctx context.Context, runErr error) {
		updated, _ := taskStore.GetTask(ctx, task.ID)
		done <- completion{
			traceID: orgTraceIDFromContext(ctx),
			err:     runErr,
			status:  updated.Status,
			result:  updated.Result,
		}
	})
	if err != nil {
		t.Fatalf("start delegation: %v", err)
	}

	select {
	case got := <-done:
		if got.err == nil || !strings.Contains(got.err.Error(), "required stores not configured") {
			t.Fatalf("run error = %v, want required stores error", got.err)
		}
		if got.traceID == "" {
			t.Fatal("completion context has no trace ID")
		}
		if got.status != service.TaskStatusCancelled {
			t.Fatalf("status = %q, want %q", got.status, service.TaskStatusCancelled)
		}
		if !strings.Contains(got.result, "delegation failed:") {
			t.Fatalf("result = %q, want delegation failure", got.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for delegation completion")
	}
}

func TestProcessTaskRejectsDuplicateDelegation(t *testing.T) {
	task := service.Task{
		ID:              "task-1",
		OrganizationID:  "org-1",
		AssignedAgentID: "agent-1",
		Status:          service.TaskStatusTodo,
	}
	org := &service.Organization{ID: "org-1", HeadAgentID: "agent-1"}
	s := &Server{
		ctx:               context.Background(),
		taskStore:         &mockTaskStoreForDelegation{tasks: []service.Task{task}},
		organizationStore: &mockOrgStoreForDelegation{orgs: map[string]*service.Organization{"org-1": org}},
		orgAgentStore: &mockOrgAgentStoreForDelegation{agents: []service.OrganizationAgent{
			{OrganizationID: "org-1", AgentID: "agent-1", Status: "active"},
		}},
	}
	_, cleanup, err := s.tryRegisterDelegation(s.ctx, task.ID, org.HeadAgentID, org.ID)
	if err != nil {
		t.Fatalf("register existing delegation: %v", err)
	}
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/task-1/process", nil)
	req.SetPathValue("id", task.ID)
	w := httptest.NewRecorder()
	s.ProcessTaskAPI(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d: %s", w.Code, http.StatusConflict, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), errDelegationAlreadyRunning.Error()) {
		t.Fatalf("response %q does not contain duplicate-start error", w.Body.String())
	}
	if !s.isDelegationActive(task.ID) {
		t.Fatal("duplicate start disturbed the existing registration")
	}
}

func TestExecTaskProcessRejectsDuplicateDelegation(t *testing.T) {
	task := service.Task{
		ID:              "task-1",
		OrganizationID:  "org-1",
		AssignedAgentID: "agent-1",
		Status:          service.TaskStatusTodo,
	}
	org := &service.Organization{ID: "org-1", HeadAgentID: "agent-1"}
	s := &Server{
		ctx:               context.Background(),
		taskStore:         &mockTaskStoreForDelegation{tasks: []service.Task{task}},
		organizationStore: &mockOrgStoreForDelegation{orgs: map[string]*service.Organization{"org-1": org}},
	}
	_, cleanup, err := s.tryRegisterDelegation(s.ctx, task.ID, task.AssignedAgentID, org.ID)
	if err != nil {
		t.Fatalf("register existing delegation: %v", err)
	}
	defer cleanup()

	_, err = s.execTaskProcess(context.Background(), map[string]any{"id": task.ID})
	if !errors.Is(err, errDelegationAlreadyRunning) {
		t.Fatalf("execTaskProcess error = %v, want duplicate delegation", err)
	}
	if !s.isDelegationActive(task.ID) {
		t.Fatal("duplicate builtin start disturbed the existing registration")
	}
}

func TestExecOrgTaskIntakeRejectsDuplicateDelegation(t *testing.T) {
	org := &service.Organization{ID: "org-1", HeadAgentID: "agent-1", IssuePrefix: "ORG"}
	s := &Server{
		ctx:               context.Background(),
		taskStore:         &mockTaskStoreForDelegation{},
		organizationStore: &mockOrgStoreForDelegation{orgs: map[string]*service.Organization{"org-1": org}},
		orgAgentStore: &mockOrgAgentStoreForDelegation{agents: []service.OrganizationAgent{
			{OrganizationID: org.ID, AgentID: org.HeadAgentID, Status: "active"},
		}},
	}
	_, cleanup, err := s.tryRegisterDelegation(s.ctx, "task-1", org.HeadAgentID, org.ID)
	if err != nil {
		t.Fatalf("register existing delegation: %v", err)
	}
	defer cleanup()

	_, err = s.execOrgTaskIntake(context.Background(), map[string]any{
		"organization_id": org.ID,
		"title":           "Duplicate task",
	})
	if !errors.Is(err, errDelegationAlreadyRunning) {
		t.Fatalf("execOrgTaskIntake error = %v, want duplicate delegation", err)
	}
	if !s.isDelegationActive("task-1") {
		t.Fatal("duplicate org intake disturbed the existing registration")
	}
}

func TestResumeBotTaskRejectsDuplicateBeforeStatusReset(t *testing.T) {
	task := service.Task{
		ID:             "task-1",
		Identifier:     "ORG-1",
		OrganizationID: "org-1",
		Status:         service.TaskStatusBlocked,
		Result:         "saved result",
	}
	org := &service.Organization{ID: "org-1", HeadAgentID: "agent-1"}
	taskStore := &mockTaskStoreForDelegation{tasks: []service.Task{task}}
	s := &Server{
		ctx:               context.Background(),
		taskStore:         taskStore,
		organizationStore: &mockOrgStoreForDelegation{orgs: map[string]*service.Organization{"org-1": org}},
	}
	_, cleanup, err := s.tryRegisterDelegation(s.ctx, task.ID, org.HeadAgentID, org.ID)
	if err != nil {
		t.Fatalf("register existing delegation: %v", err)
	}
	defer cleanup()

	err = s.resumeBotTask(installRuntimeFixture(t, s), &task)
	if !errors.Is(err, errDelegationAlreadyRunning) {
		t.Fatalf("resumeBotTask error = %v, want duplicate delegation", err)
	}
	stored, getErr := taskStore.GetTask(context.Background(), task.ID)
	if getErr != nil || stored == nil {
		t.Fatalf("get task after duplicate resume: task=%v error=%v", stored, getErr)
	}
	if stored.Status != service.TaskStatusBlocked || stored.Result != "saved result" {
		t.Fatalf("duplicate resume mutated task: status=%q result=%q", stored.Status, stored.Result)
	}
}

func TestRunOrgDelegationRejectsRegistrationOwnedByAnotherContext(t *testing.T) {
	task := service.Task{ID: "task-1", OrganizationID: "org-1", Status: service.TaskStatusTodo}
	org := &service.Organization{ID: "org-1", HeadAgentID: "agent-1", MaxDelegationDepth: 1}
	taskStore := &mockTaskStoreForDelegation{tasks: []service.Task{task}}
	s := &Server{
		taskStore:     taskStore,
		agentStore:    &mockAgentStoreForDelegation{agents: map[string]*service.Agent{}},
		orgAgentStore: &mockOrgAgentStoreForDelegation{},
	}
	_, cleanup, err := s.tryRegisterDelegation(context.Background(), task.ID, org.HeadAgentID, org.ID)
	if err != nil {
		t.Fatalf("register existing delegation: %v", err)
	}
	defer cleanup()

	err = s.runOrgDelegation(installRuntimeFixture(t, s), org, &task, org.HeadAgentID, 1)
	if !errors.Is(err, errDelegationAlreadyRunning) {
		t.Fatalf("runOrgDelegation error = %v, want duplicate delegation", err)
	}
	stored, getErr := taskStore.GetTask(context.Background(), task.ID)
	if getErr != nil || stored == nil {
		t.Fatalf("get task after recursive duplicate: task=%v error=%v", stored, getErr)
	}
	if stored.Status != service.TaskStatusTodo {
		t.Fatalf("recursive duplicate mutated task status to %q", stored.Status)
	}
}
