package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type userUsageProvider struct {
	fail       bool
	streamFail bool
}

func (p userUsageProvider) Proxy(http.ResponseWriter, *http.Request, string) error {
	return errors.New("unused proxy")
}

var _ service.LLMStreamProvider = userUsageProvider{}

func (p userUsageProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	if p.fail {
		return nil, errors.New("upstream failed")
	}
	return &service.LLMResponse{Content: "reply", Finished: true, Usage: service.Usage{PromptTokens: 20, CompletionTokens: 5, CacheReadTokens: 10}}, nil
}

func (p userUsageProvider) ChatStream(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	if p.fail {
		return nil, nil, errors.New("upstream failed")
	}
	ch := make(chan service.StreamChunk, 2)
	ch <- service.StreamChunk{Content: "reply", Usage: &service.Usage{PromptTokens: 20, CompletionTokens: 5, CacheReadTokens: 10}}
	if p.streamFail {
		ch <- service.StreamChunk{Error: errors.New("stream interrupted")}
	} else {
		ch <- service.StreamChunk{FinishReason: "stop"}
	}
	close(ch)
	return ch, nil, nil
}

func TestChatsUserUsage(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		stream, fail, midstream bool
		status                  string
	}{
		{"sync", false, false, false, "ok"},
		{"stream", true, false, false, "ok"},
		{"sync error", false, true, false, "error"},
		{"stream open error", true, true, false, "error"},
		{"partial stream error", true, false, true, "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, costs, calls := meteredProxyServer(t, userUsageProvider{fail: tc.fail, streamFail: tc.midstream}, "")
			body := `{"model":"anthropic/claude-3-5-sonnet","user":"forged-user","metadata":{"user_id":"forged-user"},"messages":[{"role":"user","content":"hello"}]`
			if tc.stream {
				body += `,"stream":true`
			}
			body += `}`
			ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: "real-user", WorkspaceID: "workspace", PlatformAdmin: true})
			r := httptest.NewRequest("POST", "/at/api/v1/chats/completions", strings.NewReader(body)).WithContext(ctx)
			w := httptest.NewRecorder()
			s.AdminChatCompletions(w, r)
			if tc.fail && w.Code != http.StatusBadGateway {
				t.Fatalf("failure returned %d: %s", w.Code, w.Body)
			}
			events, _ := waitForRecords(t, costs, calls, 1, 0)
			if len(events) != 1 {
				t.Fatalf("expected one billable call, got %+v", events)
			}
			e := events[0]
			if e.UserID != "real-user" || e.Source != "chats" || e.Status != tc.status || e.AgentID != "" {
				t.Fatalf("wrong attribution: %+v", e)
			}
			if !tc.fail && (e.InputTokens != 20 || e.OutputTokens != 5 || e.CacheReadTokens != 10) {
				t.Fatalf("usage lost: %+v", e)
			}
		})
	}
}

func TestSessionUserUsage(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "missing_tool"}}, Usage: service.Usage{PromptTokens: 30, CompletionTokens: 5}},
		{Content: "done", Finished: true, Usage: service.Usage{PromptTokens: 40, CompletionTokens: 8}},
	}}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, map[string]*service.Agent{
		"agent": {ID: "agent", Name: "Agent", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5}},
	}, nil)
	costs := &recordingCostStore{}
	s.costEventStore = costs
	s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "session", AgentID: "agent", OwnerUserID: "session-owner"}}
	if err := s.RunAgenticLoop(s.ctx, "session", "hello", func(AgenticEvent) {}); err != nil {
		t.Fatal(err)
	}
	events := costs.snapshot()
	if len(events) != 2 {
		t.Fatalf("each iteration must be counted: %+v", events)
	}
	for _, e := range events {
		if e.UserID != "session-owner" || e.Source != "sessions" || e.AgentID != "agent" {
			t.Fatalf("wrong session attribution: %+v", e)
		}
	}
}

func TestCancelledUsageRetainsAttribution(t *testing.T) {
	costs := &liveContextCostStore{}
	s := &Server{costEventStore: costs}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.recordUsageFunc()(ctx, workflow.UsageEvent{UserID: "owner", Source: "sessions", Status: "error"}); err != nil {
		t.Fatal(err)
	}
	if events := costs.snapshot(); len(events) != 1 || events[0].UserID != "owner" {
		t.Fatalf("lost cancelled usage: %+v", events)
	}
}

type liveContextCostStore struct{ recordingCostStore }

func (s *liveContextCostStore) RecordCostEvent(ctx context.Context, event service.CostEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("accounting write has no deadline")
	}
	return s.recordingCostStore.RecordCostEvent(ctx, event)
}

func TestUsageUserSourceFilter(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/usage/grouped?user_id=one&user_id=two&source=chats&source=sessions", nil)
	f := parseUsageFilter(r)
	if len(f.UserIDs) != 2 || f.UserIDs[1] != "two" || len(f.Sources) != 2 || f.Sources[1] != "sessions" {
		t.Fatalf("filter lost: %+v", f)
	}
}
