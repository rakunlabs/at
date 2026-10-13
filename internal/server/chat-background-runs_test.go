package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// chatRunProvider answers with a scripted sequence and can hold one call open
// until released, so a test can observe the run while it is live.
type chatRunProvider struct {
	mu        sync.Mutex
	responses []*service.LLMResponse
	requests  [][]service.Message
	tools     [][]service.Tool
	gate      chan struct{}
	entered   chan struct{}
}

func (p *chatRunProvider) Chat(ctx context.Context, _ string, messages []service.Message, tools []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	p.mu.Lock()
	call := len(p.requests)
	p.requests = append(p.requests, messages)
	p.tools = append(p.tools, tools)
	gate, entered := p.gate, p.entered
	p.mu.Unlock()
	if call == 0 && gate != nil {
		close(entered)
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if call >= len(p.responses) {
		return &service.LLMResponse{Content: "out of script", Finished: true}, nil
	}
	return p.responses[call], nil
}

func (p *chatRunProvider) Proxy(http.ResponseWriter, *http.Request, string) error { return nil }

func chatRunRequest(s *Server, token, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/chats/conversations"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	return w
}

func chatRunFixture(t *testing.T, p *chatRunProvider) (*Server, []string, service.PlaygroundConversation) {
	t.Helper()
	s, _, tokens := playgroundFixture(t)
	s.providerMu.Lock()
	s.providers = map[string]ProviderInfo{"runprov": {provider: p, providerType: "openai", models: []string{"model"}}}
	s.providerMu.Unlock()
	// The workspace policy decides which tools a run may use; allow built-ins.
	policyRequest := func(method, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/at/api/v1/workspaces/"+service.DefaultWorkspaceID+"/execution-policy", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+tokens[0])
		r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)
		return w
	}
	var current struct {
		Policy service.ExecutionPolicy `json:"policy"`
	}
	if w := policyRequest(http.MethodGet, ""); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &current) != nil {
		t.Fatalf("read policy: %d %s", w.Code, w.Body)
	}
	if w := policyRequest(http.MethodPut, fmt.Sprintf(`{"mode":"trusted_host","version":%d,"allow_all_tools":true}`, current.Policy.Version)); w.Code != 200 {
		t.Fatalf("policy: %d %s", w.Code, w.Body)
	}
	w := playgroundRequest(s, tokens[0], "POST", "", `{"title":"Run","system_prompt":"be brief","provider_key":"runprov","model":"model","config":{"background":true,"builtin_tools":["current_time","todo_write"],"frontend_tools":["todo_write","todo_read","question"]}}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var c service.PlaygroundConversation
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"user","data":{"content":"what time is it?"}}]}`); w.Code != 201 {
		t.Fatalf("append: %d %s", w.Code, w.Body)
	}
	return s, tokens, c
}

func TestChatBackgroundRunAnswersSavedTranscript(t *testing.T) {
	p := &chatRunProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "call-1", Name: "current_time", Arguments: map[string]any{}}}, FinishReason: "tool_calls", Usage: service.Usage{PromptTokens: 10, CompletionTokens: 2}},
		{Content: "It is now.", FinishReason: "stop", Finished: true, Usage: service.Usage{PromptTokens: 20, CompletionTokens: 3}},
	}}
	s, tokens, c := chatRunFixture(t, p)

	w := chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{"trace_id":"chats-turn-test"}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0001"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("run: %d %s", w.Code, w.Body)
	}
	for _, event := range []string{`"type":"status"`, `"type":"tool_start"`, `"type":"message"`, `"content":"It is now."`} {
		if !strings.Contains(w.Body.String(), event) {
			t.Fatalf("missing %s: %s", event, w.Body)
		}
	}

	// The model saw the system prompt and the saved question. The browser todo
	// list replaces the server todo built-in; the question prompt is offered.
	encoded, _ := json.Marshal(p.requests[0])
	for _, want := range []string{"be brief", "what time is it?"} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("request missing %q: %s", want, encoded)
		}
	}
	names := map[string]int{}
	for _, tool := range p.tools[0] {
		names[tool.Name]++
	}
	if names["current_time"] != 1 || names["todo_write"] != 1 || names["todo_read"] != 1 || names["question"] != 1 {
		t.Fatalf("offered tools: %v", names)
	}
	if second, _ := json.Marshal(p.requests[1]); !strings.Contains(string(second), "call-1") {
		t.Fatalf("tool result not sent back: %s", second)
	}

	stored := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", ""))
	roles := []string{}
	for _, m := range stored {
		roles = append(roles, m.Role)
	}
	if strings.Join(roles, ",") != "user,assistant,tool,assistant" {
		t.Fatalf("stored roles: %v", roles)
	}
	if stored[3].Data["content"] != "It is now." || stored[1].Data["at_usage"] == nil || stored[2].Data["tool_call_id"] != "call-1" {
		t.Fatalf("stored transcript: %+v", stored)
	}

	// The receipt is released once the run finishes.
	status := chatRunRequest(s, tokens[0], "GET", "/"+c.ID+"/run", "", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"run":null`) {
		t.Fatalf("status after run: %d %s", status.Code, status.Body)
	}
	// Another account cannot start a run on the conversation.
	if w := chatRunRequest(s, tokens[1], "POST", "/"+c.ID+"/run", `{}`, nil); w.Code != 404 {
		t.Fatalf("foreign run: %d %s", w.Code, w.Body)
	}
}

func TestChatBackgroundRunOutlivesThePage(t *testing.T) {
	p := &chatRunProvider{
		responses: []*service.LLMResponse{{Content: "finished without you", FinishReason: "stop", Finished: true}},
		gate:      make(chan struct{}), entered: make(chan struct{}),
	}
	s, tokens, c := chatRunFixture(t, p)

	// The page posts the run and then goes away.
	pageCtx, leave := context.WithCancel(t.Context())
	r := httptest.NewRequest("POST", "/at/api/v1/chats/conversations/"+c.ID+"/run", strings.NewReader(`{}`)).WithContext(pageCtx)
	r.Header.Set("Authorization", "Bearer "+tokens[0])
	r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
	r.Header.Set("X-AT-Stream-ID", "chat-run-stream-0002")
	done := make(chan struct{})
	go func() { s.server.ServeHTTP(httptest.NewRecorder(), r); close(done) }()
	<-p.entered
	leave()
	<-done

	// While it runs the transcript belongs to it and the page can find it.
	status := chatRunRequest(s, tokens[0], "GET", "/"+c.ID+"/run", "", nil)
	if !strings.Contains(status.Body.String(), `"stream_id":"chat-run-stream-0002"`) {
		t.Fatalf("live run not discoverable: %s", status.Body)
	}
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"user","data":{"content":"interleaved"}}]}`); w.Code != 409 {
		t.Fatalf("browser append during run: %d %s", w.Code, w.Body)
	}
	if w := chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0003"}); w.Code != 409 {
		t.Fatalf("second run: %d %s", w.Code, w.Body)
	}

	close(p.gate)
	deadline := time.Now().Add(10 * time.Second)
	for {
		stored := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", ""))
		if len(stored) == 2 && stored[1].Data["content"] == "finished without you" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish after the page left: %+v", stored)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Replay of the finished run is still available to the owner.
	replay := chatRunRequest(s, tokens[0], "GET", "/"+c.ID+"/streams/chat-run-stream-0002?offset=0", "", nil)
	if replay.Code != 200 || !strings.Contains(replay.Body.String(), `"type":"done"`) {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body)
	}
	if foreign := chatRunRequest(s, tokens[1], "GET", "/"+c.ID+"/streams/chat-run-stream-0002?offset=0", "", nil); foreign.Code != 404 {
		t.Fatalf("foreign replay: %d %s", foreign.Code, foreign.Body)
	}
}

func TestChatBackgroundRunStop(t *testing.T) {
	p := &chatRunProvider{gate: make(chan struct{}), entered: make(chan struct{})}
	s, tokens, c := chatRunFixture(t, p)
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0004"})
	}()
	<-p.entered
	// A Stop aimed at an earlier run leaves this one going.
	if w := chatRunRequest(s, tokens[0], "DELETE", "/"+c.ID+"/run?run_id=an-earlier-run", "", nil); w.Code != 204 {
		t.Fatalf("stale stop: %d %s", w.Code, w.Body)
	}
	if status := chatRunRequest(s, tokens[0], "GET", "/"+c.ID+"/run", "", nil); !strings.Contains(status.Body.String(), `"cancel_requested":false`) {
		t.Fatalf("stale stop reached the live run: %s", status.Body)
	}
	if w := chatRunRequest(s, tokens[0], "DELETE", "/"+c.ID+"/run", "", nil); w.Code != 204 {
		t.Fatalf("stop: %d %s", w.Code, w.Body)
	}
	w := <-result
	if !strings.Contains(w.Body.String(), `"cancelled":true`) {
		t.Fatalf("stopped run: %s", w.Body)
	}
	if status := chatRunRequest(s, tokens[0], "GET", "/"+c.ID+"/run", "", nil); !strings.Contains(status.Body.String(), `"run":null`) {
		t.Fatalf("receipt kept after stop: %s", status.Body)
	}
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"user","data":{"content":"again"}}]}`); w.Code != 201 {
		t.Fatalf("append after stop: %d %s", w.Code, w.Body)
	}
}

func TestChatBackgroundRunFeatureRoute(t *testing.T) {
	for _, path := range []string{"/at/api/v1/chats/conversations/c1/run", "/at/api/v1/chats/conversations/c1/streams/s1"} {
		if got := featureKeyForRoute(path, "GET", "/at"); got != service.FeatureChatBackgroundRuns {
			t.Errorf("%s feature = %q", path, got)
		}
	}
	if got := featureKeyForRoute("/at/api/v1/chats/conversations/c1/messages", "GET", "/at"); got != service.FeaturePlayground {
		t.Errorf("messages feature = %q", got)
	}
}

func TestChatBackgroundRunClosesInterruptedCalls(t *testing.T) {
	p := &chatRunProvider{responses: []*service.LLMResponse{{Content: "ok", FinishReason: "stop", Finished: true}}}
	s, tokens, c := chatRunFixture(t, p)
	// An interrupted browser turn: two calls, one answered, then the reader
	// wrote again before anything closed the other call.
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"assistant","data":{"content":"","tool_calls":[{"id":"a","type":"function","function":{"name":"current_time","arguments":"{}"}},{"id":"b","type":"function","function":{"name":"current_time","arguments":"{}"}}]}},{"role":"tool","data":{"content":"done","tool_call_id":"a"}},{"role":"user","data":{"content":"continue"}}]}`); w.Code != 201 {
		t.Fatalf("append: %d %s", w.Code, w.Body)
	}
	if w := chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0005"}); !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("run: %s", w.Body)
	}
	// Every call is answered before the next user message.
	encoded, _ := json.Marshal(p.requests[0])
	if !strings.Contains(string(encoded), "Interrupted by the user") || strings.Index(string(encoded), `"b"`) > strings.Index(string(encoded), "continue") {
		t.Fatalf("interrupted call not closed before the next message: %s", encoded)
	}
}

func TestChatBackgroundRunWaitsForAnswer(t *testing.T) {
	p := &chatRunProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{
			{ID: "t1", Name: "current_time", Arguments: map[string]any{}},
			{ID: "q1", Name: "question", Arguments: map[string]any{"question": "Which zone?", "options": []any{map[string]any{"label": "UTC"}}}},
		}, FinishReason: "tool_calls"},
		{Content: "UTC it is.", FinishReason: "stop", Finished: true},
	}}
	s, tokens, c := chatRunFixture(t, p)

	w := chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0006"})
	if !strings.Contains(w.Body.String(), `"type":"question"`) || !strings.Contains(w.Body.String(), `"waiting":true`) {
		t.Fatalf("run did not stop at the question: %s", w.Body)
	}
	if len(p.requests) != 1 {
		t.Fatalf("model called again before the answer: %d", len(p.requests))
	}
	// The other call of the step ran; the question is the open call.
	stored := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", ""))
	if len(stored) != 3 || stored[2].Data["tool_call_id"] != "t1" {
		t.Fatalf("transcript while waiting: %+v", stored)
	}
	// Nothing holds the conversation while it waits: any device can answer.
	if status := chatRunRequest(s, tokens[0], "GET", "/"+c.ID+"/run", "", nil); !strings.Contains(status.Body.String(), `"run":null`) {
		t.Fatalf("receipt held while waiting: %s", status.Body)
	}
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"tool","data":{"content":"{\"answer\":\"UTC\"}","tool_call_id":"q1"}}]}`); w.Code != 201 {
		t.Fatalf("answer: %d %s", w.Code, w.Body)
	}
	w = chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0007"})
	if !strings.Contains(w.Body.String(), `"content":"UTC it is."`) || strings.Contains(w.Body.String(), `"waiting":true`) {
		t.Fatalf("continuation: %s", w.Body)
	}
	if second, _ := json.Marshal(p.requests[1]); !strings.Contains(string(second), `{\"answer\":\"UTC\"}`) || strings.Contains(string(second), "Interrupted by the user") {
		t.Fatalf("answer not delivered as the question's result: %s", second)
	}
}

func TestChatBackgroundRunUnansweredQuestionIsClosed(t *testing.T) {
	p := &chatRunProvider{responses: []*service.LLMResponse{{Content: "ok", FinishReason: "stop", Finished: true}}}
	s, tokens, c := chatRunFixture(t, p)
	// The run stopped at a question and the reader wrote something else.
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"assistant","data":{"content":"","tool_calls":[{"id":"q1","type":"function","function":{"name":"question","arguments":"{}"}}]}},{"role":"user","data":{"content":"never mind"}}]}`); w.Code != 201 {
		t.Fatalf("append: %d %s", w.Code, w.Body)
	}
	if w := chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0008"}); !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("run: %s", w.Body)
	}
	if encoded, _ := json.Marshal(p.requests[0]); !strings.Contains(string(encoded), "did not answer this question") {
		t.Fatalf("unanswered question not closed: %s", encoded)
	}
}

func TestChatBackgroundRunTrailingQuestionsAndHistoryLimit(t *testing.T) {
	p := &chatRunProvider{responses: []*service.LLMResponse{{Content: "ok", FinishReason: "stop", Finished: true}}}
	s, tokens, c := chatRunFixture(t, p)
	// Two questions in one step; only the first was answered.
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"assistant","data":{"content":"","tool_calls":[{"id":"q1","type":"function","function":{"name":"question","arguments":"{}"}},{"id":"q2","type":"function","function":{"name":"question","arguments":"{}"}}]}},{"role":"tool","data":{"content":"{\"answer\":\"A\"}","tool_call_id":"q1"}}]}`); w.Code != 201 {
		t.Fatalf("append: %d %s", w.Code, w.Body)
	}
	if w := chatRunRequest(s, tokens[0], "POST", "/"+c.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0009"}); !strings.Contains(w.Body.String(), `"type":"done"`) {
		t.Fatalf("run: %s", w.Body)
	}
	if encoded, _ := json.Marshal(p.requests[0]); !strings.Contains(string(encoded), "did not answer this question") || strings.Contains(string(encoded), "Interrupted by the user") {
		t.Fatalf("second question closed with the wrong result: %s", encoded)
	}

	// 2001 uncompacted rows are refused, as browser completions refuse them.
	big := service.PlaygroundConversation{}
	w := playgroundRequest(s, tokens[0], "POST", "", `{"title":"Big","provider_key":"runprov","model":"model","config":{"background":true}}`)
	if err := json.Unmarshal(w.Body.Bytes(), &big); err != nil {
		t.Fatal(err)
	}
	var rows []string
	for i := 0; i < 200; i++ {
		rows = append(rows, `{"role":"user","data":{"content":"m"}}`)
	}
	batch := `{"messages":[` + strings.Join(rows, ",") + `]}`
	for i := 0; i < 10; i++ {
		if w := playgroundRequest(s, tokens[0], "POST", "/"+big.ID+"/messages", batch); w.Code != 201 {
			t.Fatalf("seed: %d %s", w.Code, w.Body)
		}
	}
	if w := playgroundRequest(s, tokens[0], "POST", "/"+big.ID+"/messages", `{"messages":[{"role":"user","data":{"content":"one more"}}]}`); w.Code != 201 {
		t.Fatalf("seed: %d", w.Code)
	}
	w = chatRunRequest(s, tokens[0], "POST", "/"+big.ID+"/run", `{}`, map[string]string{"X-AT-Stream-ID": "chat-run-stream-0010"})
	if !strings.Contains(w.Body.String(), "run /compact first") {
		t.Fatalf("2001 rows were not refused: %s", w.Body)
	}
}

func TestChatBackgroundRunsArePerOwnerBounded(t *testing.T) {
	s := &Server{}
	s.chatStreams.streams = map[string]*chatStream{}
	for i := 0; i < chatRunMaxPerOwner; i++ {
		s.chatStreams.streams[fmt.Sprint("live-", i)] = &chatStream{kind: replayChatRun, owner: "a", workspace: "w"}
	}
	s.chatStreams.streams["done"] = &chatStream{kind: replayChatRun, owner: "a", workspace: "w", done: true}
	s.chatStreams.streams["other"] = &chatStream{kind: replayChatRun, owner: "b", workspace: "w"}
	s.chatStreams.streams["chat"] = &chatStream{kind: replayChat, owner: "a", workspace: "w"}
	if got := s.liveChatRuns("a", "w"); got != chatRunMaxPerOwner {
		t.Fatalf("live runs = %d", got)
	}
	if got := s.liveChatRuns("b", "w"); got != 1 {
		t.Fatalf("other owner = %d", got)
	}
}
