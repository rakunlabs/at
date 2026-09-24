package server

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
)

type blockingSubagentProvider struct {
	started chan struct{}
	done    chan struct{}
	once    sync.Once
}

func (p *blockingSubagentProvider) Chat(ctx context.Context, _ string, _ []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	p.once.Do(func() { close(p.started) })
	<-ctx.Done()
	close(p.done)
	return nil, ctx.Err()
}

func TestExecAgentRunForegroundIsolated(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{{
		Content:  "review complete",
		Finished: true,
	}}}
	observations := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"parent": {
			ID: "parent", Name: "Parent",
			Config: service.AgentConfig{Subagents: []string{"child"}},
		},
		"child": {
			ID: "child", Name: "Reviewer",
			Config: service.AgentConfig{
				Provider: "prov1", Model: "m1", MaxIterations: 2,
				SystemPrompt: "Review carefully.",
			},
		},
	}
	s, _ := newObsTestServer(t, provider, observations, agents, nil)
	ctx := contextWithAgentID(s.ctx, "parent")
	ctx = contextWithChatTraceID(ctx, "parent-trace")

	result, err := s.execAgentRun(ctx, map[string]any{
		"agent":   "child",
		"task":    "Review this patch.",
		"context": "Focus on authorization.",
	})
	if err != nil {
		t.Fatalf("execAgentRun: %v", err)
	}
	var payload struct {
		AgentID       string `json:"agent_id"`
		Result        string `json:"result"`
		TraceID       string `json:"trace_id"`
		ParentTraceID string `json:"parent_trace_id"`
	}
	if err := json.Unmarshal([]byte(result), &payload); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if payload.AgentID != "child" || payload.Result != "review complete" || payload.TraceID == "" || payload.ParentTraceID != "parent-trace" {
		t.Fatalf("unexpected result: %+v", payload)
	}

	if len(provider.requests) != 1 {
		t.Fatalf("expected one child provider call, got %d", len(provider.requests))
	}
	requestJSON, _ := json.Marshal(provider.requests[0])
	requestText := string(requestJSON)
	for _, want := range []string{"Review carefully.", "Review this patch.", "Focus on authorization."} {
		if !strings.Contains(requestText, want) {
			t.Fatalf("child request does not contain %q: %s", want, requestText)
		}
	}

	obs := waitForObservations(t, observations, 1)
	if obs[0].TraceID != payload.TraceID || obs[0].SessionID == "" || obs[0].AgentID != "child" {
		t.Fatalf("unexpected child observation identity: %+v", obs[0])
	}
	if obs[0].Metadata["subagent"] != true || obs[0].Metadata["parent_trace_id"] != "parent-trace" {
		t.Fatalf("missing subagent trace metadata: %#v", obs[0].Metadata)
	}
}

func TestExecAgentRunActivatesForkedSkillInChildOnly(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{{Content: "skill complete", Finished: true}}}
	agents := map[string]*service.Agent{
		"parent": {ID: "parent", Name: "Parent", Config: service.AgentConfig{Subagents: []string{"child"}}},
		"child": {
			ID: "child", Name: "Reviewer",
			Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2},
		},
	}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, agents, nil)
	s.skillStore = &fakeSkillStore{skills: map[string]*service.Skill{
		"skill-review": {
			ID: "skill-review", Name: "review-skill", Context: "fork", Agent: "child",
			SystemPrompt: "Apply the forked review checklist.",
		},
	}}
	ctx := contextWithAgentID(s.ctx, "parent")
	ctx = contextWithSubagentSkill(ctx, "skill-review")
	if _, err := s.execAgentRun(ctx, map[string]any{"agent": "child", "task": "Review it"}); err != nil {
		t.Fatalf("execAgentRun: %v", err)
	}
	requestJSON, _ := json.Marshal(provider.requests[0])
	if !strings.Contains(string(requestJSON), "Apply the forked review checklist.") {
		t.Fatalf("forked skill was not activated in child request: %s", requestJSON)
	}
}

func TestExecAgentRunGuards(t *testing.T) {
	agents := map[string]*service.Agent{
		"parent":  {ID: "parent", Name: "Parent", Config: service.AgentConfig{Subagents: []string{"allowed"}}},
		"allowed": {ID: "allowed", Name: "Allowed"},
		"other":   {ID: "other", Name: "Other"},
	}
	s, _ := newObsTestServer(t, &fakeObsProvider{}, &fakeLLMCallStore{}, agents, nil)
	base := contextWithAgentID(s.ctx, "parent")

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "not allowlisted", ref: "other", want: "not in"},
		{name: "self", ref: "parent", want: "cannot launch itself"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.execAgentRun(base, map[string]any{"agent": tt.ref, "task": "work"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}

	depthCtx := contextWithSubagentRuntime(base, subagentRuntimeContext{Depth: maxSubagentDepth})
	_, err := s.execAgentRun(depthCtx, map[string]any{"agent": "allowed", "task": "work"})
	if err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("expected depth limit error, got %v", err)
	}
}

func TestAgentRunToolMetadata(t *testing.T) {
	metadata := agentRunToolMetadata("agent_run", `{"agent_id":"child","agent_name":"Reviewer","trace_id":"trace-child"}`)
	if metadata["child_trace_id"] != "trace-child" || metadata["child_agent_id"] != "child" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if got := agentRunToolMetadata("other", `{}`); got != nil {
		t.Fatalf("non-agent tool produced metadata: %#v", got)
	}
}

func TestBackgroundAgentRunStatusAndCancel(t *testing.T) {
	provider := &blockingSubagentProvider{started: make(chan struct{}), done: make(chan struct{})}
	agents := map[string]*service.Agent{
		"parent": {ID: "parent", Name: "Parent", Config: service.AgentConfig{Subagents: []string{"child"}}},
		"child": {
			ID: "child", Name: "Child",
			Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2},
		},
	}
	s := &Server{
		agentStore: &mockAgentStoreForDelegation{agents: agents},
		loopGov:    loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil),
		providers: map[string]ProviderInfo{
			"prov1": {provider: provider, providerType: "openai", defaultModel: "m1"},
		},
	}
	installRuntimeFixture(t, s)
	ctx := contextWithAgentID(s.ctx, "parent")

	started, err := s.execAgentRun(ctx, map[string]any{"agent": "child", "task": "wait", "background": true})
	if err != nil {
		t.Fatalf("start background agent: %v", err)
	}
	var startPayload struct {
		RunID   string `json:"run_id"`
		TraceID string `json:"trace_id"`
	}
	if err := json.Unmarshal([]byte(started), &startPayload); err != nil || startPayload.RunID == "" || startPayload.TraceID == "" {
		t.Fatalf("unexpected start payload %q: %v", started, err)
	}
	select {
	case <-provider.started:
	case <-time.After(time.Second):
		t.Fatal("background provider did not start")
	}

	if _, err := s.execAgentRunCancel(ctx, map[string]any{"run_id": startPayload.RunID}); err != nil {
		t.Fatalf("cancel background agent: %v", err)
	}
	select {
	case <-provider.done:
	case <-time.After(time.Second):
		t.Fatal("background provider was not cancelled")
	}

	deadline := time.Now().Add(time.Second)
	for {
		status, err := s.execAgentRunStatus(ctx, map[string]any{"run_id": startPayload.RunID})
		if err != nil {
			t.Fatalf("background status: %v", err)
		}
		if strings.Contains(status, `"status":"cancelled"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not reach cancelled: %s", status)
		}
		time.Sleep(time.Millisecond)
	}
}
