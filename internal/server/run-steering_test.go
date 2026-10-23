package server

import (
	"context"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestRunSteeringQueue(t *testing.T) {
	r := newRunSteering()
	if ok, _ := r.Push("k", "early"); ok {
		t.Fatal("push without a running turn must be refused")
	}
	end := r.Register("k")
	if _, err := r.Push("k", "  "); err == nil {
		t.Fatal("empty message must fail")
	}
	if ok, err := r.Push("k", "one"); !ok || err != nil {
		t.Fatalf("push: %v %v", ok, err)
	}
	if got := r.Drain("k"); strings.Join(got, ",") != "one" {
		t.Fatalf("drain = %v", got)
	}
	_, _ = r.Push("k", "late")
	if left := end(); strings.Join(left, ",") != "late" {
		t.Fatalf("leftover = %v", left)
	}
	if ok, _ := r.Push("k", "after"); ok {
		t.Fatal("push after the turn ended must be refused")
	}
	// A newer registration is not removed by an older end().
	endOld := r.Register("k")
	endNew := r.Register("k")
	endOld()
	if ok, _ := r.Push("k", "x"); !ok {
		t.Fatal("newer run lost its queue")
	}
	endNew()
}

// blockingObsProvider runs a callback before answering, so a test can steer
// a turn while the model is "thinking".
type blockingObsProvider struct {
	*fakeObsProvider
	before func(call int)
	calls  int
}

func (b *blockingObsProvider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	b.calls++
	if b.before != nil {
		b.before(b.calls)
	}
	return b.fakeObsProvider.Chat(ctx, model, messages, tools, opts)
}

// A message steered in while the model answers makes the turn continue with
// it, and it is saved as a user message.
func TestChatSessionSteeringContinuesTurn(t *testing.T) {
	inner := &fakeObsProvider{responses: []*service.LLMResponse{
		{Content: "first answer", Finished: true},
		{Content: "answer to the steer", Finished: true},
	}}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5}},
	}
	s, _ := newObsTestServer(t, inner, &fakeLLMCallStore{}, agents, nil)
	s.steering = newRunSteering()
	provider := &blockingObsProvider{fakeObsProvider: inner}
	provider.before = func(call int) {
		if call == 1 {
			if ok, err := s.steering.Push("session:sess-1", "also check the tests"); !ok || err != nil {
				t.Errorf("steer: %v %v", ok, err)
			}
		}
	}
	s.providers["prov1"] = ProviderInfo{provider: provider, providerType: "openai", defaultModel: "m1"}
	store := &fakeChatSessionStore{session: service.ChatSession{ID: "sess-1", AgentID: "agent-a"}}
	s.chatSessionStore = store

	var events []string
	if err := s.RunAgenticLoop(s.ctx, "sess-1", "hello", func(ev AgenticEvent) {
		if ev.Type == "user_message" || ev.Type == "content" {
			events = append(events, ev.Type+":"+ev.Content)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if provider.calls != 2 {
		t.Fatalf("model calls = %d, want 2", provider.calls)
	}
	want := "content:first answer|user_message:also check the tests|content:answer to the steer"
	if strings.Join(events, "|") != want {
		t.Fatalf("events = %v", events)
	}
	inner.mu.Lock()
	last := inner.requests[1]
	inner.mu.Unlock()
	if text, _ := last[len(last)-1].Content.(string); text != "also check the tests" {
		t.Fatalf("second request ends with %#v", last[len(last)-1])
	}
}
