package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestChatSessionFinalReply(t *testing.T) {
	tool := func() *service.LLMResponse {
		return &service.LLMResponse{Finished: true, ToolCalls: []service.ToolCall{{ID: "call-1", Name: "unknown_tool", Arguments: map[string]any{}}}}
	}
	for _, tt := range []struct {
		name       string
		max        int
		responses  []*service.LLMResponse
		want       string
		calls      int
		answerOnly bool
	}{
		{"tools then reply", 4, []*service.LLMResponse{tool(), {Content: "İşlem tamamlandı.", Finished: true}}, "İşlem tamamlandı.", 2, false},
		{"empty reply recovery", 3, []*service.LLMResponse{{Finished: true}, {Content: "Here is the answer.", Finished: true}}, "Here is the answer.", 2, true},
		{"reserve final step", 2, []*service.LLMResponse{tool(), {Content: "I could not finish, but here are the results.", Finished: true}}, "I could not finish", 2, true},
		{"empty after recovery", 3, []*service.LLMResponse{{Finished: true}, {Finished: true}}, "[No final reply]", 2, true},
		{"single-step tool limit", 1, []*service.LLMResponse{tool()}, "[Run stopped]", 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider := &fakeObsProvider{responses: tt.responses}
			s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, map[string]*service.Agent{"a": {ID: "a", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: tt.max}}}, nil)
			store := &fakeChatSessionStore{session: service.ChatSession{ID: "session", AgentID: "a"}}
			s.chatSessionStore = store
			var events []AgenticEvent
			if err := s.RunAgenticLoop(s.ctx, "session", "Please explain the result", func(e AgenticEvent) { events = append(events, e) }); err != nil {
				t.Fatal(err)
			}
			if provider.calls != tt.calls {
				t.Fatalf("calls = %d", provider.calls)
			}
			if tt.answerOnly && len(provider.tools[len(provider.tools)-1]) != 0 {
				t.Fatal("final reply still offered tools")
			}
			if len(events) == 0 || events[len(events)-1].Type != "done" {
				t.Fatalf("events: %+v", events)
			}
			last := store.messages[len(store.messages)-1]
			text, _ := last.Data.Content.(string)
			if last.Role != "assistant" || !strings.Contains(text, tt.want) {
				t.Fatalf("persisted final reply = %+v", last)
			}
			found := false
			for _, e := range events {
				if e.Type == "content" && e.Final && e.Content == text {
					found = true
				}
			}
			if !found {
				t.Fatal("persisted final answer was not emitted")
			}
		})
	}
}

type failingReplyStore struct{ *fakeChatSessionStore }

func (s *failingReplyStore) CreateChatMessage(ctx context.Context, m service.ChatMessage) (*service.ChatMessage, error) {
	if m.Role == "assistant" {
		return nil, errors.New("database unavailable")
	}
	return s.fakeChatSessionStore.CreateChatMessage(ctx, m)
}

func TestChatSessionReplySaveFailureDoesNotSignalDone(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{{Content: "Keep this answer", Finished: true}}}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, map[string]*service.Agent{"a": {ID: "a", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 3}}}, nil)
	s.chatSessionStore = &failingReplyStore{&fakeChatSessionStore{session: service.ChatSession{ID: "session", AgentID: "a"}}}
	var events []AgenticEvent
	if err := s.RunAgenticLoop(s.ctx, "session", "hello", func(e AgenticEvent) { events = append(events, e) }); err == nil {
		t.Fatal("save failure hidden")
	}
	for _, e := range events {
		if e.Type == "done" {
			t.Fatal("unsaved answer signalled success")
		}
	}
	if events[len(events)-1].Type != "error" {
		t.Fatalf("events: %+v", events)
	}
}

func TestTaskChatImportsTerminalToolResultAsAssistant(t *testing.T) {
	for _, history := range []string{"none", "tools", "final"} {
		t.Run(history, func(t *testing.T) {
			result := "The finished document is ready at output/report.md."
			s, tasks := newObsTestServer(t, &fakeObsProvider{}, &fakeLLMCallStore{}, map[string]*service.Agent{"a": {ID: "a"}}, nil)
			task, err := tasks.CreateTask(s.ctx, service.Task{Title: "Write a report", Description: "Review the project", AssignedAgentID: "a", Status: service.TaskStatusDone, Result: result})
			if err != nil {
				t.Fatal(err)
			}
			chats := &fakeChatSessionStore{}
			s.chatSessionStore = chats
			if history != "none" {
				var content any = []map[string]any{{"type": "tool_use", "id": "done", "name": "task_complete", "input": map[string]any{"result": result}}}
				if history == "final" {
					content = result
				}
				data, err := json.Marshal([]service.Message{{Role: "assistant", Content: content}})
				if err != nil {
					t.Fatal(err)
				}
				s.issueCommentStore = &fakeIssueCommentStore{comments: []service.IssueComment{{ID: "state", TaskID: task.ID, Body: conversationStatePrefix + string(data)}}}
			}
			r := httptest.NewRequest("POST", "/api/v1/tasks/"+task.ID+"/chat", nil).WithContext(s.ctx)
			r.SetPathValue("id", task.ID)
			w := httptest.NewRecorder()
			s.CreateTaskChatAPI(w, r)
			if w.Code != 201 {
				t.Fatalf("task chat: %d %s", w.Code, w.Body)
			}
			matches := 0
			for _, message := range chats.messages {
				if message.Role == "assistant" && message.Data.Content == result {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("expected one visible assistant result, got %d: %+v", matches, chats.messages)
			}
		})
	}
}
