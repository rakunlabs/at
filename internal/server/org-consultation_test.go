package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
)

func consultationFixture(t *testing.T, responses ...*service.LLMResponse) (*Server, *mockTaskStoreForDelegation, *fakeObsProvider, *fakeLLMCallStore) {
	t.Helper()
	provider := &fakeObsProvider{responses: responses}
	observations := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"writer": {ID: "writer", Name: "Writer", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 3}},
		"editor": {ID: "editor", Name: "Editor", Config: service.AgentConfig{Provider: "prov1", Model: "m1", SystemPrompt: "You review drafts."}},
	}
	members := []service.OrganizationAgent{
		{OrganizationID: "org1", AgentID: "writer", ParentAgentID: "head", Status: "active"},
		{OrganizationID: "org1", AgentID: "editor", ParentAgentID: "head", Status: "active"},
	}
	s, tasks := newObsTestServer(t, provider, observations, agents, members)
	return s, tasks, provider, observations
}

func TestOrgConsultationKeepsOneTask(t *testing.T) {
	s, tasks, provider, observations := consultationFixture(t,
		&service.LLMResponse{ToolCalls: []service.ToolCall{{ID: "ask", Name: consultAgentTool, Arguments: map[string]any{"agent_id": "editor", "question": "Review this", "context": "draft text"}}}},
		&service.LLMResponse{Content: "Clarify the conclusion.", Finished: true},
		&service.LLMResponse{Content: "Reviewed and revised the conclusion.", Finished: true},
	)
	task, _ := tasks.CreateTask(s.ctx, service.Task{OrganizationID: "org1", AssignedAgentID: "writer", Title: "Write a draft"})
	if err := s.runOrgDelegation(s.ctx, &service.Organization{ID: "org1"}, task, "writer", 0); err != nil {
		t.Fatal(err)
	}
	children, _ := tasks.ListChildTasks(s.ctx, task.ID)
	if len(children) != 0 {
		t.Fatalf("consultation created %d child tasks", len(children))
	}
	updated, _ := tasks.GetTask(s.ctx, task.ID)
	if updated.Status != service.TaskStatusDone || !strings.Contains(updated.Result, "revised") {
		t.Fatalf("task owner did not deliver: %+v", updated)
	}
	if provider.calls != 3 || len(provider.tools[1]) != 0 {
		t.Fatalf("consultation must be one tool-free call, got calls=%d tools=%v", provider.calls, provider.tools)
	}
	if !strings.Contains(provider.requests[1][1].Content.(string), "draft text") {
		t.Fatal("consultant did not receive the supplied context")
	}
	if !strings.Contains(provider.requests[2][len(provider.requests[2])-1].Content.([]service.ContentBlock)[0].ContentText(), "Clarify") {
		t.Fatal("owner did not receive the advice")
	}
	for range 200 {
		for _, obs := range observations.snapshot() {
			if obs.ObservationType == service.ObservationGeneration && obs.Name == consultAgentTool {
				if obs.AgentID != "editor" || obs.TaskID != task.ID || obs.SessionID != task.ID || obs.ParentObservationID == "" {
					t.Fatalf("consultation attribution lost: %+v", obs)
				}
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("consultation generation not recorded")
}

func TestOrgConsultationBoundsAndMembership(t *testing.T) {
	s, _, provider, _ := consultationFixture(t)
	ctx := withConsultationBudget(s.ctx)
	org, task := &service.Organization{ID: "org1"}, &service.Task{ID: "task"}
	call := func(ctx context.Context, target, question string) error {
		_, err := s.consultOrgAgent(ctx, org, task, "writer", map[string]any{"agent_id": target, "question": question}, agentloop.ObservationContext{}, "")
		return err
	}
	for _, target := range []string{"writer", "outsider"} {
		if call(ctx, target, "Review") == nil {
			t.Fatalf("accepted invalid target %s", target)
		}
	}
	if call(ctx, "editor", strings.Repeat("x", consultationInputBytes+1)) == nil {
		t.Fatal("accepted unbounded input")
	}
	if len(provider.requests) != 0 {
		t.Fatal("rejected consultation reached provider")
	}
	if call(withConsultationBudget(context.Background()), "editor", "Review") == nil {
		t.Fatal("consultation bypassed execution authority")
	}
	for range maxOrgConsultations {
		// A descendant shares its parent's budget instead of getting a new one.
		if err := call(withConsultationBudget(ctx), "editor", "Review"); err != nil {
			t.Fatal(err)
		}
	}
	if call(ctx, "editor", "Again") == nil || len(provider.requests) != maxOrgConsultations {
		t.Fatal("consultation budget did not bound provider calls")
	}
	s.orgAgentStore.(*mockOrgAgentStoreForDelegation).agents[1].Status = "inactive"
	if call(withConsultationBudget(s.ctx), "editor", "Review") == nil {
		t.Fatal("inactive teammate was admitted")
	}
}

func TestOrgConsultationRejectsIncompleteAnswers(t *testing.T) {
	for _, response := range []*service.LLMResponse{
		{Content: "partial", FinishReason: "length"},
		{Content: "partial", FinishReason: "max_tokens"},
		{Refusal: "Cannot review"},
		{},
		{ToolCalls: []service.ToolCall{{Name: "task_create"}}},
	} {
		s, tasks, _, _ := consultationFixture(t, response)
		_, err := s.consultOrgAgent(withConsultationBudget(s.ctx), &service.Organization{ID: "org1"}, &service.Task{ID: "task"}, "writer", map[string]any{"agent_id": "editor", "question": "Review"}, agentloop.ObservationContext{}, "")
		if err == nil {
			t.Fatalf("accepted incomplete answer: %+v", response)
		}
		if len(tasks.tasks) != 0 {
			t.Fatal("consultant executed a task tool")
		}
	}
}

func TestMissingTaskAgentOrProviderIsBlocked(t *testing.T) {
	for _, missing := range []string{"agent", "provider"} {
		t.Run(missing, func(t *testing.T) {
			s, tasks, _, _ := consultationFixture(t)
			if missing == "agent" {
				delete(s.agentStore.(*mockAgentStoreForDelegation).agents, "writer")
			} else {
				delete(s.providers, "prov1")
			}
			task, _ := tasks.CreateTask(s.ctx, service.Task{OrganizationID: "org1", AssignedAgentID: "writer", Title: "Write"})
			if err := s.runOrgDelegation(s.ctx, &service.Organization{ID: "org1"}, task, "writer", 0); err != nil {
				t.Fatal(err)
			}
			updated, _ := tasks.GetTask(s.ctx, task.ID)
			if updated.Status != service.TaskStatusBlocked {
				t.Fatalf("missing %s reported %s", missing, updated.Status)
			}
		})
	}
}

func TestDelegationRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitDelegationRetry(ctx, time.Hour) {
		t.Fatal("cancelled retry continued")
	}
}
