package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type reasoningRetryProvider struct {
	options []*service.ChatOptions
}

func (p *reasoningRetryProvider) Chat(_ context.Context, _ string, _ []service.Message, _ []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	p.options = append(p.options, opts)
	if len(p.options) == 1 {
		return nil, errors.New("tool_call_id not found")
	}
	return &service.LLMResponse{Content: "done", Finished: true}, nil
}

func TestAgentReasoningEffortRetry(t *testing.T) {
	for _, loop := range []string{"org", "chat"} {
		for _, effort := range []string{"", "xhigh"} {
			t.Run(loop+"/"+effort, func(t *testing.T) {
				provider := &reasoningRetryProvider{}
				store := &fakeLLMCallStore{}
				agents := map[string]*service.Agent{
					"agent": {ID: "agent", Name: "Agent", Config: service.AgentConfig{Provider: "prov1", Model: "m1", ReasoningEffort: effort, MaxIterations: 2}},
				}
				s, taskStore := newObsTestServer(t, &fakeObsProvider{}, store, agents, nil)
				s.providers["prov1"] = ProviderInfo{provider: provider, providerType: "openai", defaultModel: "m1"}
				count := 1
				if loop == "org" {
					task, err := taskStore.CreateTask(context.Background(), service.Task{OrganizationID: "org1", Title: "reason", Status: service.TaskStatusTodo, AssignedAgentID: "agent"})
					if err != nil {
						t.Fatal(err)
					}
					if err := s.runOrgDelegation(s.ctx, &service.Organization{ID: "org1", IssuePrefix: "OBS"}, task, "agent", 0); err != nil {
						t.Fatal(err)
					}
					count = 3 // started, generation, completed
				} else {
					s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "session", AgentID: "agent"}}
					if err := s.RunAgenticLoop(s.ctx, "session", "reason", func(event AgenticEvent) {
						if event.Type == "error" {
							t.Errorf("chat error: %s", event.Error)
						}
					}); err != nil {
						t.Fatal(err)
					}
				}
				if len(provider.options) != 2 {
					t.Fatalf("calls = %d, want retry", len(provider.options))
				}
				for _, opts := range provider.options {
					if effort == "" {
						if opts != nil {
							t.Fatalf("default options = %+v", opts)
						}
					} else if opts == nil || opts.ReasoningEffort != effort || opts.MaxTokens != nil || opts.MaxCompletionTokens != nil {
						t.Fatalf("retry options = %+v", opts)
					}
				}
				generations := 0
				for _, obs := range waitForObservations(t, store, count) {
					if obs.ObservationType != service.ObservationGeneration {
						continue
					}
					generations++
					if strings.Contains(obs.RequestBody, `"reasoning_effort"`) != (effort != "") || (effort != "" && !strings.Contains(obs.RequestBody, `"reasoning_effort":"xhigh"`)) {
						t.Fatalf("request = %s", obs.RequestBody)
					}
				}
				if generations != 1 {
					t.Fatalf("generations = %d", generations)
				}
			})
		}
	}
}
