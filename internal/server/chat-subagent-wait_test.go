package server

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// scriptedAgentProvider answers by the requesting agent's system prompt, so a
// parent turn and the subagent it launches can be scripted independently.
type scriptedAgentProvider struct {
	mu        sync.Mutex
	parent    []*service.LLMResponse
	child     *service.LLMResponse
	childGate chan struct{}
	requests  [][]service.Message
}

func (p *scriptedAgentProvider) Chat(ctx context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	isChild := len(messages) > 0 && messages[0].Role == "system" && strings.Contains(messages[0].Content.(string), "CHILD")
	if isChild {
		if p.childGate != nil {
			select {
			case <-p.childGate:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return p.child, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, append([]service.Message(nil), messages...))
	if len(p.parent) == 0 {
		return &service.LLMResponse{Content: "out of script", Finished: true}, nil
	}
	resp := p.parent[0]
	p.parent = p.parent[1:]
	return resp, nil
}

func subagentLoopServer(t *testing.T, provider *scriptedAgentProvider, toolTimeout int) (*Server, *fakeChatSessionStore) {
	t.Helper()
	agents := map[string]*service.Agent{
		"parent": {ID: "parent", Name: "Parent", Config: service.AgentConfig{
			Provider: "prov1", Model: "m1", MaxIterations: 6, ToolTimeout: toolTimeout,
			SystemPrompt: "PARENT", Subagents: []string{"child"}, BuiltinTools: []string{"agent_run"},
		}},
		"child": {ID: "child", Name: "Researcher", Config: service.AgentConfig{
			Provider: "prov1", Model: "m1", MaxIterations: 2, SystemPrompt: "CHILD",
		}},
	}
	s, _ := newObsTestServer(t, &fakeObsProvider{}, &fakeLLMCallStore{}, agents, nil)
	s.providers["prov1"] = ProviderInfo{provider: provider, providerType: "openai", defaultModel: "m1"}
	store := &fakeChatSessionStore{session: service.ChatSession{ID: "session", AgentID: "parent"}}
	s.chatSessionStore = store
	return s, store
}

func lastAssistantText(t *testing.T, store *fakeChatSessionStore) string {
	t.Helper()
	last := store.messages[len(store.messages)-1]
	text, _ := last.Data.Content.(string)
	if last.Role != "assistant" {
		t.Fatalf("last persisted message is %q, not the assistant answer", last.Role)
	}
	return text
}

func TestChatTurnWaitsForForegroundSubagentBeyondToolTimeout(t *testing.T) {
	gate := make(chan struct{})
	provider := &scriptedAgentProvider{
		parent: []*service.LLMResponse{
			{ToolCalls: []service.ToolCall{{ID: "call-1", Name: "agent_run", Arguments: map[string]any{"agent": "child", "task": "research"}}}},
			{Content: "Summary: findings received.", Finished: true},
		},
		child:     &service.LLMResponse{Content: "child findings", Finished: true},
		childGate: gate,
	}
	// A one-second per-tool deadline used to cancel the subagent mid-run.
	s, store := subagentLoopServer(t, provider, 1)
	time.AfterFunc(1500*time.Millisecond, func() { close(gate) })

	var mu sync.Mutex
	var events []AgenticEvent
	if err := s.RunAgenticLoop(s.ctx, "session", "go", func(e AgenticEvent) { mu.Lock(); events = append(events, e); mu.Unlock() }); err != nil {
		t.Fatal(err)
	}
	if got := lastAssistantText(t, store); got != "Summary: findings received." {
		t.Fatalf("final answer = %q", got)
	}
	var result string
	var progress bool
	for _, e := range events {
		if e.Type == "tool_result" && e.ToolID == "call-1" {
			result = e.Result
		}
		if e.Type == "tool_progress" && e.ToolID == "call-1" {
			progress = true
		}
	}
	if !strings.Contains(result, "child findings") {
		t.Fatalf("subagent result did not reach the turn: %q", result)
	}
	if !progress {
		t.Fatal("no subagent progress was streamed")
	}
}

func TestChatTurnWaitsForBackgroundSubagentResult(t *testing.T) {
	gate := make(chan struct{})
	provider := &scriptedAgentProvider{
		parent: []*service.LLMResponse{
			{ToolCalls: []service.ToolCall{{ID: "call-1", Name: "agent_run", Arguments: map[string]any{"agent": "child", "task": "research", "background": true}}}},
			{Content: "Started the research in the background.", Finished: true},
			{Content: "Final: child findings are in.", Finished: true},
		},
		child:     &service.LLMResponse{Content: "child findings", Finished: true},
		childGate: gate,
	}
	s, store := subagentLoopServer(t, provider, 60)
	time.AfterFunc(200*time.Millisecond, func() { close(gate) })

	if err := s.RunAgenticLoop(s.ctx, "session", "go", func(AgenticEvent) {}); err != nil {
		t.Fatal(err)
	}
	if got := lastAssistantText(t, store); got != "Final: child findings are in." {
		t.Fatalf("turn ended before the background result: %q", got)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	last := provider.requests[len(provider.requests)-1]
	report, _ := last[len(last)-1].Content.(string)
	if !strings.Contains(report, "child findings") || !strings.Contains(report, "completed") {
		t.Fatalf("background result was not given to the model: %q", report)
	}
}
