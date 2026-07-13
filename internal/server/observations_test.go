package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/query"
)

// ─── Fakes ───

// fakeObsProvider returns scripted responses in order.
type fakeObsProvider struct {
	mu        sync.Mutex
	responses []*service.LLMResponse
	requests  [][]service.Message
	calls     int
}

func (f *fakeObsProvider) Chat(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, append([]service.Message(nil), messages...))
	if f.calls >= len(f.responses) {
		return &service.LLMResponse{Content: "out of script", Finished: true}, nil
	}
	resp := f.responses[f.calls]
	f.calls++
	return resp, nil
}

type fakeIssueCommentStore struct {
	mu       sync.Mutex
	comments []service.IssueComment
}

func (f *fakeIssueCommentStore) ListCommentsByTask(_ context.Context, taskID string) ([]service.IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var comments []service.IssueComment
	for _, comment := range f.comments {
		if comment.TaskID == taskID {
			comments = append(comments, comment)
		}
	}
	return comments, nil
}

func (f *fakeIssueCommentStore) GetComment(_ context.Context, id string) (*service.IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.comments {
		if f.comments[i].ID == id {
			comment := f.comments[i]
			return &comment, nil
		}
	}
	return nil, nil
}

func (f *fakeIssueCommentStore) CreateComment(_ context.Context, comment service.IssueComment) (*service.IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.comments = append(f.comments, comment)
	return &comment, nil
}

func (f *fakeIssueCommentStore) UpdateComment(_ context.Context, id string, comment service.IssueComment) (*service.IssueComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.comments {
		if f.comments[i].ID == id {
			comment.ID = id
			f.comments[i] = comment
			return &comment, nil
		}
	}
	return nil, nil
}

func (f *fakeIssueCommentStore) DeleteComment(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.comments {
		if f.comments[i].ID == id {
			f.comments = append(f.comments[:i], f.comments[i+1:]...)
			break
		}
	}
	return nil
}

// fakeLLMCallStore captures recorded observations in memory.
type fakeLLMCallStore struct {
	mu          sync.Mutex
	calls       []service.LLMCall
	expireCalls []string // cutoffs passed to ExpireLLMCallBodiesBefore
	deleteCalls []string // cutoffs passed to DeleteLLMCallsBefore
}

func (f *fakeLLMCallStore) RecordLLMCall(_ context.Context, call service.LLMCall) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeLLMCallStore) ListLLMCalls(_ context.Context, _ *query.Query) (*service.ListResult[service.LLMCall], error) {
	return nil, nil
}

func (f *fakeLLMCallStore) GetLLMCall(_ context.Context, _ string) (*service.LLMCall, error) {
	return nil, nil
}

func (f *fakeLLMCallStore) ListLLMCallTraces(_ context.Context, _ *query.Query) (*service.ListResult[service.LLMCallTrace], error) {
	return nil, nil
}

func (f *fakeLLMCallStore) DeleteLLMCallsBefore(_ context.Context, cutoff string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls = append(f.deleteCalls, cutoff)
	return 0, nil
}

func (f *fakeLLMCallStore) ExpireLLMCallBodiesBefore(_ context.Context, cutoff string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.expireCalls = append(f.expireCalls, cutoff)
	return 0, nil
}

func (f *fakeLLMCallStore) snapshot() []service.LLMCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]service.LLMCall{}, f.calls...)
}

// waitForObservations polls until the store holds at least n rows (the
// recorder is fire-and-forget on a goroutine per observation).
func waitForObservations(t *testing.T, store *fakeLLMCallStore, n int) []service.LLMCall {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		obs := store.snapshot()
		if len(obs) >= n {
			return obs
		}
		time.Sleep(10 * time.Millisecond)
	}
	obs := store.snapshot()
	t.Fatalf("timed out waiting for %d observations, got %d: %+v", n, len(obs), obsNames(obs))
	return nil
}

func obsNames(obs []service.LLMCall) []string {
	out := make([]string, 0, len(obs))
	for _, o := range obs {
		out = append(out, o.ObservationType+":"+o.Name)
	}
	return out
}

// fakeFeatureStore pins a feature key to a fixed enabled state.
type fakeFeatureStore struct {
	key     string
	enabled bool
}

func (f *fakeFeatureStore) ListFeatureSettings(_ context.Context) ([]service.FeatureSetting, error) {
	return nil, nil
}

func (f *fakeFeatureStore) GetFeatureSetting(_ context.Context, key string) (*service.FeatureSetting, error) {
	if key == f.key {
		return &service.FeatureSetting{Key: key, Enabled: f.enabled}, nil
	}
	return nil, nil
}

func (f *fakeFeatureStore) UpsertFeatureSetting(_ context.Context, key string, enabled bool, _ string) (*service.FeatureSetting, error) {
	return &service.FeatureSetting{Key: key, Enabled: enabled}, nil
}

// newObsTestServer builds a Server with just enough wiring to run
// runOrgDelegation with a fake provider and capture observations.
func newObsTestServer(t *testing.T, provider *fakeObsProvider, obsStore *fakeLLMCallStore, agents map[string]*service.Agent, orgAgents []service.OrganizationAgent) (*Server, *mockTaskStoreForDelegation) {
	t.Helper()
	taskStore := &mockTaskStoreForDelegation{}
	s := &Server{
		agentStore:        &mockAgentStoreForDelegation{agents: agents},
		taskStore:         taskStore,
		orgAgentStore:     &mockOrgAgentStoreForDelegation{agents: orgAgents},
		organizationStore: &mockOrgStoreForDelegation{orgs: map[string]*service.Organization{"org1": {ID: "org1", IssuePrefix: "OBS"}}},
		llmCallStore:      obsStore,
		loopGov:           loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil),
		providers: map[string]ProviderInfo{
			"prov1": {provider: provider, providerType: "openai", defaultModel: "m1"},
		},
	}
	return s, taskStore
}

// ─── Tests ───

func TestOrgDelegation_RecordsObservations(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{
			ToolCalls: []service.ToolCall{{ID: "tc1", Name: "mystery_tool", Arguments: map[string]any{"x": 1}}},
			Usage:     service.Usage{PromptTokens: 100, CompletionTokens: 10},
		},
		{
			Content:  "all done",
			Finished: true,
			Usage:    service.Usage{PromptTokens: 150, CompletionTokens: 20},
		},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5}},
	}
	s, taskStore := newObsTestServer(t, provider, obsStore, agents, nil)

	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "trace me", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	org := &service.Organization{ID: "org1", IssuePrefix: "OBS"}
	if err := s.runOrgDelegation(context.Background(), org, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}

	// Expected: task_started event, generation, tool (unknown → error),
	// generation, task_completed event.
	obs := waitForObservations(t, obsStore, 5)

	var traceID string
	var gens, tools, events []service.LLMCall
	for _, o := range obs {
		if traceID == "" {
			traceID = o.TraceID
		}
		if o.TraceID != traceID {
			t.Fatalf("expected one trace, got %q and %q", traceID, o.TraceID)
		}
		if o.Source != "agent" {
			t.Fatalf("expected source agent, got %q (%+v)", o.Source, o)
		}
		if o.SessionID != task.ID {
			t.Fatalf("expected session %q (root task), got %q", task.ID, o.SessionID)
		}
		switch o.ObservationType {
		case service.ObservationGeneration:
			gens = append(gens, o)
		case service.ObservationTool:
			tools = append(tools, o)
		case service.ObservationEvent:
			events = append(events, o)
		}
	}

	if len(gens) != 2 {
		t.Fatalf("expected 2 generations, got %d: %v", len(gens), obsNames(obs))
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool observation, got %d: %v", len(tools), obsNames(obs))
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(events), obsNames(obs))
	}

	eventNames := map[string]bool{}
	for _, e := range events {
		eventNames[e.Name] = true
	}
	if !eventNames["task_started"] || !eventNames["task_completed"] {
		t.Fatalf("missing lifecycle events: %v", eventNames)
	}

	// Tool observation is parented to the generation that requested it.
	firstGen := gens[0]
	if gens[1].CreatedAt < firstGen.CreatedAt || (gens[1].Metadata != nil && firstGen.Metadata != nil && gens[1].Metadata["iteration"] == 0) {
		// Order gens by iteration metadata to be safe.
		if it, ok := gens[1].Metadata["iteration"].(int); ok && it == 0 {
			firstGen = gens[1]
		}
	}
	tool := tools[0]
	if tool.ParentObservationID != firstGen.ID {
		t.Fatalf("tool parent = %q, want first generation %q", tool.ParentObservationID, firstGen.ID)
	}
	if tool.Name != "mystery_tool" {
		t.Fatalf("tool name = %q", tool.Name)
	}
	if tool.Level != service.ObservationLevelError {
		t.Fatalf("unknown tool must record level=error, got %q", tool.Level)
	}
	if !strings.Contains(tool.Output, "unknown tool") {
		t.Fatalf("tool output should carry the error fed to the LLM, got %q", tool.Output)
	}
	if !strings.Contains(tool.Input, `"x"`) {
		t.Fatalf("tool input should carry the arguments, got %q", tool.Input)
	}

	// Feature store is nil → llm_audit defaults ON → bodies captured.
	for _, g := range gens {
		if g.RequestBody == "" || g.ResponseBody == "" {
			t.Fatalf("expected bodies captured with llm_audit default-on: %+v", g)
		}
		if !strings.Contains(g.RequestBody, "trace me") {
			t.Fatalf("request body should contain the windowed messages, got %q", g.RequestBody[:min(200, len(g.RequestBody))])
		}
	}
	// Skeleton fields present.
	if gens[0].InputTokens == 0 {
		t.Fatalf("generation tokens missing: %+v", gens[0])
	}
}

func TestOrgDelegation_SkeletonOnlyWhenAuditOff(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{Content: "done", Finished: true, Usage: service.Usage{PromptTokens: 42, CompletionTokens: 7}},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 3}},
	}
	s, taskStore := newObsTestServer(t, provider, obsStore, agents, nil)
	s.featureStore = &fakeFeatureStore{key: service.FeatureLLMAudit, enabled: false}

	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "skeleton run", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	org := &service.Organization{ID: "org1", IssuePrefix: "OBS"}
	if err := s.runOrgDelegation(context.Background(), org, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}

	// task_started, generation, task_completed — skeletons are recorded
	// even with the flag off.
	obs := waitForObservations(t, obsStore, 3)

	var gen *service.LLMCall
	for i := range obs {
		if obs[i].ObservationType == service.ObservationGeneration {
			gen = &obs[i]
		}
	}
	if gen == nil {
		t.Fatalf("generation skeleton must be recorded with llm_audit off: %v", obsNames(obs))
	}
	if gen.RequestBody != "" || gen.ResponseBody != "" {
		t.Fatalf("bodies must be empty with llm_audit off: req=%q resp=%q", gen.RequestBody, gen.ResponseBody)
	}
	if gen.InputTokens != 42 || gen.OutputTokens != 7 {
		t.Fatalf("skeleton tokens missing: %+v", gen)
	}
}

func TestOrgDelegation_OutputLimitContinuesWithinIterationBudget(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{Content: "partial", Finished: true, FinishReason: "max_tokens"},
		{Content: "complete", Finished: true, FinishReason: "end_turn"},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 3}},
	}
	s, taskStore := newObsTestServer(t, provider, obsStore, agents, nil)
	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "finish output", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.runOrgDelegation(context.Background(), &service.Organization{ID: "org1", IssuePrefix: "OBS"}, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}
	got, err := taskStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if got.Status != service.TaskStatusCompleted || got.Result != "complete" {
		t.Fatalf("task = status %q result %q, want completed complete", got.Status, got.Result)
	}
}

func TestOrgDelegation_OutputLimitIsNotReportedAsIterationLimit(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{Finished: true, FinishReason: "max_tokens"},
		{Finished: true, FinishReason: "max_tokens"},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2}},
	}
	s, taskStore := newObsTestServer(t, provider, obsStore, agents, nil)
	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "too much output", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.runOrgDelegation(context.Background(), &service.Organization{ID: "org1", IssuePrefix: "OBS"}, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}
	got, err := taskStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != service.TaskStatusBlocked {
		t.Fatalf("status = %q, want blocked", got.Status)
	}
	if !strings.HasPrefix(got.Result, "[OUTPUT_LIMIT]") {
		t.Fatalf("result = %q, want OUTPUT_LIMIT", got.Result)
	}
	if strings.HasPrefix(got.Result, "[ITERATION_LIMIT]") {
		t.Fatalf("output limit was mislabeled as iteration limit: %q", got.Result)
	}
}

func TestOrgDelegation_ReportsIterationLimitOnlyAfterAllRounds(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "unknown_one"}}, FinishReason: "tool_calls"},
		{ToolCalls: []service.ToolCall{{ID: "tc2", Name: "unknown_two"}}, FinishReason: "tool_calls"},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2}},
	}
	s, taskStore := newObsTestServer(t, provider, obsStore, agents, nil)
	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "too many rounds", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.runOrgDelegation(context.Background(), &service.Organization{ID: "org1", IssuePrefix: "OBS"}, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}
	got, err := taskStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if provider.calls != 2 {
		t.Fatalf("provider calls = %d, want 2", provider.calls)
	}
	if got.Status != service.TaskStatusBlocked || !strings.HasPrefix(got.Result, "[ITERATION_LIMIT]") {
		t.Fatalf("task = status %q result %q, want blocked ITERATION_LIMIT", got.Status, got.Result)
	}
}

func TestOrgDelegation_ResumeGetsFreshIterationBudget(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "run1-tc1", Name: "unknown_one"}}, FinishReason: "tool_calls"},
		{ToolCalls: []service.ToolCall{{ID: "run1-tc2", Name: "unknown_two"}}, FinishReason: "tool_calls"},
		{ToolCalls: []service.ToolCall{{ID: "run2-tc1", Name: "unknown_three"}}, FinishReason: "tool_calls"},
		{Content: "completed after resume", Finished: true, FinishReason: "stop"},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {
			ID:   "agent-a",
			Name: "Alpha",
			Config: service.AgentConfig{
				Provider:      "prov1",
				Model:         "m1",
				SystemPrompt:  "FOLLOW THE RESUME RULES",
				MaxIterations: 2,
			},
		},
	}
	s, taskStore := newObsTestServer(t, provider, obsStore, agents, nil)
	s.issueCommentStore = &fakeIssueCommentStore{}
	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "resume me", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	org := &service.Organization{ID: "org1", IssuePrefix: "OBS"}

	if err := s.runOrgDelegation(context.Background(), org, task, "agent-a", 0); err != nil {
		t.Fatalf("first runOrgDelegation: %v", err)
	}
	paused, err := taskStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask after first run: %v", err)
	}
	if paused.Status != service.TaskStatusBlocked || !strings.HasPrefix(paused.Result, "[ITERATION_LIMIT]") {
		t.Fatalf("first run = status %q result %q, want blocked ITERATION_LIMIT", paused.Status, paused.Result)
	}

	if err := s.runOrgDelegation(context.Background(), org, paused, "agent-a", 0); err != nil {
		t.Fatalf("resumed runOrgDelegation: %v", err)
	}
	completed, err := taskStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask after resume: %v", err)
	}
	if provider.calls != 4 {
		t.Fatalf("provider calls = %d, want 4 (2 per run)", provider.calls)
	}
	if completed.Status != service.TaskStatusCompleted || completed.Result != "completed after resume" {
		t.Fatalf("resumed task = status %q result %q", completed.Status, completed.Result)
	}
	if len(provider.requests) < 3 || len(provider.requests[2]) == 0 {
		t.Fatalf("missing first resumed request: %#v", provider.requests)
	}
	firstResumedMessage := provider.requests[2][0]
	content, _ := firstResumedMessage.Content.(string)
	if firstResumedMessage.Role != "system" || !strings.Contains(content, "FOLLOW THE RESUME RULES") {
		t.Fatalf("first resumed message = %#v, want rebuilt system prompt", firstResumedMessage)
	}
	requestJSON, err := json.Marshal(provider.requests[2])
	if err != nil {
		t.Fatalf("marshal resumed request: %v", err)
	}
	if !strings.Contains(string(requestJSON), "fresh budget of 2 iterations") {
		t.Fatalf("resumed request does not explain fresh budget: %s", requestJSON)
	}
}

func TestOrgDelegation_BlockMarkerPersistsBlockedStatus(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{Content: "[BLOCKED] assemble_video dependency failed", Finished: true},
	}}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 2}},
	}
	s, taskStore := newObsTestServer(t, provider, &fakeLLMCallStore{}, agents, nil)
	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "blocked work", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := s.runOrgDelegation(context.Background(), &service.Organization{ID: "org1", IssuePrefix: "OBS"}, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}
	got, err := taskStore.GetTask(context.Background(), task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Status != service.TaskStatusBlocked {
		t.Fatalf("status = %q, want blocked; result = %q", got.Status, got.Result)
	}
}

func TestOrgDelegation_DelegationCrossLinksChildTrace(t *testing.T) {
	// Head agent Alpha delegates to report Bee; the provider script is
	// consumed sequentially: Alpha iter0 (delegate call) → Bee iter0
	// (final) → Alpha iter1 (final).
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "delegate_to_bee", Arguments: map[string]any{"task": "sub-work"}}}},
		{Content: "child done", Finished: true},
		{Content: "parent done", Finished: true},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5}},
		"agent-b": {ID: "agent-b", Name: "Bee", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5}},
	}
	orgAgents := []service.OrganizationAgent{
		{ID: "oa1", OrganizationID: "org1", AgentID: "agent-a", ParentAgentID: "", Status: "active"},
		{ID: "oa2", OrganizationID: "org1", AgentID: "agent-b", ParentAgentID: "agent-a", Status: "active"},
	}
	s, taskStore := newObsTestServer(t, provider, obsStore, agents, orgAgents)

	task, err := taskStore.CreateTask(context.Background(), service.Task{
		OrganizationID: "org1", Title: "parent work", Status: service.TaskStatusOpen, AssignedAgentID: "agent-a",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	org := &service.Organization{ID: "org1", IssuePrefix: "OBS", MaxDelegationDepth: 5}
	if err := s.runOrgDelegation(context.Background(), org, task, "agent-a", 0); err != nil {
		t.Fatalf("runOrgDelegation: %v", err)
	}

	// Parent: task_started, gen, tool(delegate), gen, task_completed (5)
	// Child:  task_delegated (parent trace), task_started, gen, task_completed (4)
	obs := waitForObservations(t, obsStore, 9)

	var parentTrace, childTrace string
	var delegateTool *service.LLMCall
	var delegatedEvent *service.LLMCall
	for i := range obs {
		o := obs[i]
		if o.ObservationType == service.ObservationEvent && o.Name == "task_started" {
			if o.TaskID == task.ID {
				parentTrace = o.TraceID
			} else {
				childTrace = o.TraceID
			}
		}
		if o.ObservationType == service.ObservationTool && strings.HasPrefix(o.Name, "delegate_to_") {
			delegateTool = &obs[i]
		}
		if o.ObservationType == service.ObservationEvent && o.Name == "task_delegated" {
			delegatedEvent = &obs[i]
		}
	}

	if parentTrace == "" || childTrace == "" {
		t.Fatalf("missing task_started events: %v", obsNames(obs))
	}
	if parentTrace == childTrace {
		t.Fatal("parent and child runs must have distinct trace IDs")
	}
	if delegateTool == nil {
		t.Fatalf("missing delegate tool observation: %v", obsNames(obs))
	}
	if delegateTool.TraceID != parentTrace {
		t.Fatalf("delegate tool must belong to the parent trace, got %q", delegateTool.TraceID)
	}
	if got, _ := delegateTool.Metadata["child_trace_id"].(string); got != childTrace {
		t.Fatalf("delegate tool metadata child_trace_id = %q, want %q", got, childTrace)
	}
	if delegatedEvent == nil || delegatedEvent.TraceID != parentTrace {
		t.Fatalf("task_delegated must be on the parent trace: %+v", delegatedEvent)
	}

	// All observations share one session (root task ID).
	for _, o := range obs {
		if o.SessionID != task.ID {
			t.Fatalf("expected shared session %q, got %q on %s:%s", task.ID, o.SessionID, o.ObservationType, o.Name)
		}
	}

	// Child result flows back into the delegate tool output.
	if !strings.Contains(delegateTool.Output, "child done") {
		t.Fatalf("delegate tool output should carry the child result, got %q", delegateTool.Output)
	}
}

// fakeChatSessionStore backs the chat-session loop with one in-memory
// session and a message log.
type fakeChatSessionStore struct {
	mu       sync.Mutex
	session  service.ChatSession
	messages []service.ChatMessage
}

func (f *fakeChatSessionStore) ListChatSessions(_ context.Context, _ *query.Query) (*service.ListResult[service.ChatSession], error) {
	return nil, nil
}

func (f *fakeChatSessionStore) GetChatSession(_ context.Context, id string) (*service.ChatSession, error) {
	if id == f.session.ID {
		cp := f.session
		return &cp, nil
	}
	return nil, nil
}

func (f *fakeChatSessionStore) GetChatSessionByPlatform(_ context.Context, _, _, _, _ string) (*service.ChatSession, error) {
	return nil, nil
}

func (f *fakeChatSessionStore) GetChatSessionByTaskID(_ context.Context, _ string) (*service.ChatSession, error) {
	return nil, nil
}

func (f *fakeChatSessionStore) CreateChatSession(_ context.Context, s service.ChatSession) (*service.ChatSession, error) {
	return &s, nil
}

func (f *fakeChatSessionStore) UpdateChatSession(_ context.Context, _ string, s service.ChatSession) (*service.ChatSession, error) {
	return &s, nil
}

func (f *fakeChatSessionStore) DeleteChatSession(_ context.Context, _ string) error { return nil }

func (f *fakeChatSessionStore) ListChatMessages(_ context.Context, sessionID string, _ int) ([]service.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []service.ChatMessage
	for _, m := range f.messages {
		if m.SessionID == sessionID {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeChatSessionStore) ListChatMessagesBefore(_ context.Context, sessionID, beforeID string, limit int) ([]service.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []service.ChatMessage
	for _, m := range f.messages {
		if m.SessionID == sessionID && m.ID < beforeID {
			out = append(out, m)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (f *fakeChatSessionStore) CreateChatMessage(_ context.Context, m service.ChatMessage) (*service.ChatMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, m)
	return &m, nil
}

func (f *fakeChatSessionStore) CreateChatMessages(_ context.Context, msgs []service.ChatMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, msgs...)
	return nil
}

func (f *fakeChatSessionStore) DeleteChatMessages(_ context.Context, _ string) error { return nil }

func TestChatSessionLoop_RecordsObservations(t *testing.T) {
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "mystery_tool", Arguments: map[string]any{"q": "z"}}}, Usage: service.Usage{PromptTokens: 30, CompletionTokens: 5}},
		{Content: "chat done", Finished: true, Usage: service.Usage{PromptTokens: 40, CompletionTokens: 8}},
	}}
	obsStore := &fakeLLMCallStore{}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5}},
	}
	s, _ := newObsTestServer(t, provider, obsStore, agents, nil)
	s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "sess-1", AgentID: "agent-a"}}

	if err := s.RunAgenticLoop(context.Background(), "sess-1", "hello there", func(AgenticEvent) {}); err != nil {
		t.Fatalf("RunAgenticLoop: %v", err)
	}

	// 2 generations + 1 tool observation.
	obs := waitForObservations(t, obsStore, 3)

	var gens, tools []service.LLMCall
	for _, o := range obs {
		if o.Source != "chat" {
			t.Fatalf("expected source chat, got %q", o.Source)
		}
		if o.SessionID != "sess-1" {
			t.Fatalf("expected session sess-1, got %q", o.SessionID)
		}
		switch o.ObservationType {
		case service.ObservationGeneration:
			gens = append(gens, o)
		case service.ObservationTool:
			tools = append(tools, o)
		}
	}
	if len(gens) != 2 || len(tools) != 1 {
		t.Fatalf("expected 2 generations + 1 tool, got %v", obsNames(obs))
	}
	if tools[0].ParentObservationID == "" || tools[0].ParentObservationID != gens[0].ID {
		// gens order from the fake store is append order (recording is
		// async); accept either generation as parent, but it must be one.
		if tools[0].ParentObservationID != gens[1].ID {
			t.Fatalf("tool must be parented to a generation: %+v", tools[0])
		}
	}
	if tools[0].Name != "mystery_tool" || tools[0].Level != service.ObservationLevelError {
		t.Fatalf("tool obs wrong: %+v", tools[0])
	}
	// One trace per turn.
	if gens[0].TraceID == "" || gens[0].TraceID != gens[1].TraceID || tools[0].TraceID != gens[0].TraceID {
		t.Fatalf("all observations of one turn must share a trace: %v", obsNames(obs))
	}
}

func TestLLMAuditJanitor_TwoPhaseSweep(t *testing.T) {
	obsStore := &fakeLLMCallStore{}
	s := &Server{llmCallStore: obsStore}

	s.sweepLLMAuditOnce(context.Background())

	obsStore.mu.Lock()
	defer obsStore.mu.Unlock()
	if len(obsStore.expireCalls) != 1 || len(obsStore.deleteCalls) != 1 {
		t.Fatalf("expected one expire + one delete, got %d/%d", len(obsStore.expireCalls), len(obsStore.deleteCalls))
	}

	bodyCutoff, err := time.Parse(time.RFC3339, obsStore.expireCalls[0])
	if err != nil {
		t.Fatalf("parse body cutoff: %v", err)
	}
	rowCutoff, err := time.Parse(time.RFC3339, obsStore.deleteCalls[0])
	if err != nil {
		t.Fatalf("parse row cutoff: %v", err)
	}
	// Body window (7d) must be far more recent than the row window (90d).
	if !bodyCutoff.After(rowCutoff.Add(30 * 24 * time.Hour)) {
		t.Fatalf("body cutoff %s should be much newer than row cutoff %s", bodyCutoff, rowCutoff)
	}
	wantBody := time.Now().UTC().Add(-service.LLMCallRetention)
	if d := bodyCutoff.Sub(wantBody); d < -time.Minute || d > time.Minute {
		t.Fatalf("body cutoff %s not ~%s", bodyCutoff, wantBody)
	}
	wantRow := time.Now().UTC().Add(-service.ObservationRetention)
	if d := rowCutoff.Sub(wantRow); d < -time.Minute || d > time.Minute {
		t.Fatalf("row cutoff %s not ~%s", rowCutoff, wantRow)
	}
}

// tracesFakeStore returns canned trace aggregates and remembers the query.
type tracesFakeStore struct {
	fakeLLMCallStore
	traces   []service.LLMCallTrace
	lastQ    *query.Query
	tracesMu sync.Mutex
}

func (f *tracesFakeStore) ListLLMCallTraces(_ context.Context, q *query.Query) (*service.ListResult[service.LLMCallTrace], error) {
	f.tracesMu.Lock()
	defer f.tracesMu.Unlock()
	f.lastQ = q
	return &service.ListResult[service.LLMCallTrace]{
		Data: f.traces,
		Meta: service.ListMeta{Total: uint64(len(f.traces))},
	}, nil
}

func TestListLLMCallTracesAPI(t *testing.T) {
	store := &tracesFakeStore{traces: []service.LLMCallTrace{
		{TraceID: "tr-1", Source: "agent", Name: "task_started", TaskID: "task-1", ObservationCount: 6, GenerationCount: 2, InputTokens: 300, OutputTokens: 40, CostCents: 1.2, ErrorCount: 1, StartedAt: "2026-07-12T10:00:00Z", EndedAt: "2026-07-12T10:01:00Z"},
	}}
	s := &Server{llmCallStore: store}

	req := httptest.NewRequest("GET", "/api/v1/llm-calls/traces?source=agent", nil)
	rec := httptest.NewRecorder()
	s.ListLLMCallTracesAPI(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var out service.ListResult[service.LLMCallTrace]
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Meta.Total != 1 || len(out.Data) != 1 {
		t.Fatalf("expected 1 trace, got %+v", out)
	}
	got := out.Data[0]
	if got.TraceID != "tr-1" || got.ObservationCount != 6 || got.GenerationCount != 2 || got.ErrorCount != 1 {
		t.Fatalf("unexpected trace row: %+v", got)
	}
	if store.lastQ == nil {
		t.Fatal("query not forwarded to the store")
	}
}

func TestListLLMCallTracesAPI_NoStore(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "/api/v1/llm-calls/traces", nil)
	rec := httptest.NewRecorder()
	s.ListLLMCallTracesAPI(rec, req)
	if rec.Code != 503 {
		t.Fatalf("expected 503 without store, got %d", rec.Code)
	}
}

func TestRecordObservationFunc_MapsWorkflowObservation(t *testing.T) {
	obsStore := &fakeLLMCallStore{}
	s := &Server{llmCallStore: obsStore}

	fn := s.recordObservationFunc()
	if fn == nil {
		t.Fatal("recordObservationFunc must not be nil when a store is wired")
	}

	id := fn(context.Background(), service.LLMCall{
		ObservationType:     service.ObservationTool,
		ParentObservationID: "gen-9",
		Source:              "workflow",
		Name:                "wf_tool",
		TraceID:             "wf-trace",
		AgentID:             "agent-w",
		Input:               `{"a":1}`,
		Output:              "ok",
		Level:               service.ObservationLevelDefault,
		Metadata:            map[string]any{"iteration": 2},
	})
	if id == "" {
		t.Fatal("expected non-empty observation ID")
	}

	obs := waitForObservations(t, obsStore, 1)
	got := obs[0]
	if got.ID != id {
		t.Fatalf("recorded ID %q != returned %q", got.ID, id)
	}
	if got.ObservationType != service.ObservationTool || got.ParentObservationID != "gen-9" {
		t.Fatalf("hierarchy not mapped: %+v", got)
	}
	if got.Source != "workflow" || got.TraceID != "wf-trace" || got.Name != "wf_tool" {
		t.Fatalf("attribution not mapped: %+v", got)
	}
	if got.Input == "" || got.Output != "ok" {
		t.Fatalf("IO not mapped: %+v", got)
	}
}
