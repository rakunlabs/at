package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type organizationChatTestStore struct {
	*fakeChatSessionStore
	tasks *mockTaskStoreForDelegation
}

func (f *organizationChatTestStore) CreateOrganizationChatTask(ctx context.Context, id string, task service.Task, newTask bool) (*service.Task, bool, error) {
	created, err := f.tasks.CreateTask(ctx, task)
	if err == nil {
		f.session.Config.ActiveTaskID = created.ID
	}
	return created, true, err
}

func TestOrganizationChatStartsOriginalProductionAgent(t *testing.T) {
	s, tasks, provider, _ := consultationFixture(t, &service.LLMResponse{Content: "Produced", Finished: true})
	s.organizationStore.(*mockOrgStoreForDelegation).orgs["org1"].HeadAgentID = "writer"
	head := s.agentStore.(*mockAgentStoreForDelegation).agents["writer"]
	head.Config.SystemPrompt = "PRODUCTION ROLE: use the required specialists."
	head.Config.BuiltinTools = []string{"bash_execute"}
	session := service.ChatSession{ID: "chat", OrganizationID: "org1", Config: service.ChatSessionConfig{OrganizationChat: true}}
	s.chatSessionStore = &organizationChatTestStore{fakeChatSessionStore: &fakeChatSessionStore{session: session}, tasks: tasks}
	result, err := s.executeOrganizationChatTool(s.ctx, &session, "organization_start_task", map[string]any{"title": "Video", "brief": "Produce the selected topic as a short."})
	if err != nil || session.Config.ActiveTaskID == "" || !strings.Contains(result, session.Config.ActiveTaskID) {
		t.Fatalf("start: %s %v", result, err)
	}
	for range 200 {
		if !s.isDelegationActive(session.Config.ActiveTaskID) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s.isDelegationActive(session.Config.ActiveTaskID) {
		t.Fatal("production did not finish")
	}
	updated, _ := tasks.GetTask(s.ctx, session.Config.ActiveTaskID)
	if updated.Status != service.TaskStatusDone {
		t.Fatalf("production failed: %+v", updated)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.requests) != 1 || !strings.Contains(provider.requests[0][0].Content.(string), "PRODUCTION ROLE") {
		t.Fatal("background job did not use the original head agent")
	}
	for _, tool := range provider.tools[0] {
		if tool.Name == "bash_execute" {
			return
		}
	}
	t.Fatal("production lost its real tools")
}

func TestCreateOrganizationConversation(t *testing.T) {
	s, tasks, _, _ := consultationFixture(t)
	s.organizationStore.(*mockOrgStoreForDelegation).orgs["org1"].HeadAgentID = "writer"
	s.chatSessionStore = &fakeChatSessionStore{}
	r := httptest.NewRequestWithContext(s.ctx, http.MethodPost, "/api/v1/chat/sessions", strings.NewReader(`{"organization_id":"org1","agent_id":"editor","config":{"organization_chat":true,"active_task_id":"foreign"}}`))
	w := httptest.NewRecorder()
	s.CreateChatSessionAPI(w, r)
	if w.Code != http.StatusCreated {
		t.Fatalf("create conversation: %d %s", w.Code, w.Body)
	}
	var session service.ChatSession
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	if session.AgentID != "writer" || session.Config.ActiveTaskID != "" || session.TaskID != "" || !session.Config.OrganizationChat {
		t.Fatalf("organization target or task link spoofed: %+v", session)
	}
	if len(tasks.tasks) != 0 {
		t.Fatal("starting a conversation created work")
	}
}

func TestOrganizationConversationIsNotProduction(t *testing.T) {
	s, tasks, provider, _ := consultationFixture(t, &service.LLMResponse{Content: "Here are three ideas.", Finished: true})
	s.organizationStore.(*mockOrgStoreForDelegation).orgs["org1"].HeadAgentID = "writer"
	head := s.agentStore.(*mockAgentStoreForDelegation).agents["writer"]
	head.Config.SystemPrompt = "Always delegate production to three specialists."
	head.Config.BuiltinTools = []string{"bash_execute", "task_create", "org_task_intake"}
	s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "chat", AgentID: "editor", OrganizationID: "org1", Config: service.ChatSessionConfig{OrganizationChat: true, DisableTaskResultSync: true}}}
	if err := s.RunAgenticLoop(s.ctx, "chat", "What could we make this week?", func(AgenticEvent) {}); err != nil {
		t.Fatal(err)
	}
	if len(tasks.tasks) != 0 {
		t.Fatal("idea discussion created a task")
	}
	if len(provider.tools) != 1 || len(provider.tools[0]) != 3 {
		t.Fatalf("expected scoped conversation tools: %+v", provider.tools)
	}
	for _, tool := range provider.tools[0] {
		if !isOrganizationChatTool(tool.Name) {
			t.Fatalf("production tool leaked into conversation: %s", tool.Name)
		}
	}
	if len(head.Config.BuiltinTools) != 3 || head.Config.SystemPrompt != "Always delegate production to three specialists." {
		t.Fatal("conversation rewrote the production agent")
	}
}

func TestOrganizationChatStatusAndFeedbackDoNotRestart(t *testing.T) {
	s, tasks, provider, _ := consultationFixture(t)
	s.organizationStore.(*mockOrgStoreForDelegation).orgs["org1"].HeadAgentID = "writer"
	task, _ := tasks.CreateTask(s.ctx, service.Task{Title: "Video", OrganizationID: "org1", Status: service.TaskStatusDone, Result: "/workspace/final.mp4"})
	session := service.ChatSession{ID: "chat", OrganizationID: "org1", Config: service.ChatSessionConfig{OrganizationChat: true, ActiveTaskID: task.ID}}
	s.chatSessionStore = &fakeChatSessionStore{session: session}
	s.issueCommentStore = &fakeIssueCommentStore{}
	for _, tool := range []string{"organization_task_status", "organization_start_task", "organization_task_feedback"} {
		result, err := s.executeOrganizationChatTool(s.ctx, &session, tool, map[string]any{"feedback": "Please shorten the title."})
		if err != nil || !strings.Contains(result, task.ID) {
			t.Fatalf("%s: %s %v", tool, result, err)
		}
	}
	if len(tasks.tasks) != 1 || provider.calls != 0 {
		t.Fatal("follow-up restarted production")
	}
	comments, _ := s.issueCommentStore.ListCommentsByTask(s.ctx, task.ID)
	if len(comments) != 1 || comments[0].Body != "Please shorten the title." {
		t.Fatalf("feedback not attached to existing task: %+v", comments)
	}
	updated, _ := tasks.GetTask(s.ctx, task.ID)
	if updated.Status != service.TaskStatusDone || updated.Result != task.Result {
		t.Fatal("discussion overwrote the production result")
	}
}
