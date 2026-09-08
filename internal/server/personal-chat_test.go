package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/password"
	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

type personalTestProvider struct {
	calls  atomic.Int32
	stream func(context.Context, string, []service.Message) (<-chan service.StreamChunk, error)
}

func (p *personalTestProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, errors.New("unexpected nonstream call")
}
func (p *personalTestProvider) Proxy(http.ResponseWriter, *http.Request, string) error { return nil }
func (p *personalTestProvider) ChatStream(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	p.calls.Add(1)
	if len(tools) != 0 || opts != nil {
		return nil, nil, errors.New("unexpected options")
	}
	ch, err := p.stream(ctx, model, messages)
	return ch, nil, err
}

func personalHTTPFixture(t *testing.T, provider service.LLMProvider) (*Server, []string, []string) {
	t.Helper()
	p := postgrestest.New(t, nil)
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s, err := New(ctx, cfg, map[string]ProviderInfo{"test": {provider: provider, providerType: "openai", models: []string{"text-model"}, embeddingModels: []string{"secret-embedding"}}}, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	owners, tokens := []string{}, []string{}
	for _, name := range []string{"admin-a", "admin-b", "reader"} {
		u, err := p.CreateAuthUser(ctx, service.AuthUser{Username: name, Admin: name != "reader", PasswordHash: password.Dummy}, false)
		if err != nil {
			t.Fatal(err)
		}
		access, refresh, err := nativeCredentialPair()
		if err != nil {
			t.Fatal(err)
		}
		if err := p.CreateAuthSession(ctx, service.AuthSession{Hash: name, UserID: u.ID, Version: u.SessionVersion, Transport: "mobile", AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh), ExpiresAt: time.Now().Add(time.Hour), AccessExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		owners = append(owners, u.ID)
		tokens = append(tokens, access)
	}
	return s, owners, tokens
}

func personalRequest(s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/conversations"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	return w
}

func personalCreate(t *testing.T, s *Server, token string) service.PersonalConversation {
	t.Helper()
	w := personalRequest(s, token, "POST", "", `{"provider_key":"test","model":"text-model","system_prompt":"Be precise"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var c service.PersonalConversation
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPersonalChatHTTPContract(t *testing.T) {
	provider := &personalTestProvider{stream: func(_ context.Context, model string, messages []service.Message) (<-chan service.StreamChunk, error) {
		if model != "text-model" || len(messages) != 2 || messages[0].Role != "system" || messages[0].Content != "Be precise" || messages[1].Content != "hello" {
			t.Errorf("provider input: %s %+v", model, messages)
		}
		ch := make(chan service.StreamChunk, 3)
		ch <- service.StreamChunk{Content: "Hi"}
		ch <- service.StreamChunk{FinishReason: "stop"}
		ch <- service.StreamChunk{Usage: &service.Usage{PromptTokens: 4, CompletionTokens: 2, TotalTokens: 6}}
		close(ch)
		return ch, nil
	}}
	s, owners, tokens := personalHTTPFixture(t, provider)
	for _, tc := range []struct {
		token  string
		status int
	}{{"", 401}, {tokens[2], 403}} {
		w := personalRequest(s, tc.token, "GET", "/models", "")
		if w.Code != tc.status {
			t.Fatal(w.Code, w.Body)
		}
	}
	w := personalRequest(s, tokens[0], "GET", "/models", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `[{"provider_key":"test","model":"text-model"}]` {
		t.Fatal(w.Code, w.Body)
	}
	c := personalCreate(t, s, tokens[0])
	if c.OwnerUserID != owners[0] {
		t.Fatal(c)
	}
	other := personalCreate(t, s, tokens[1])
	w = personalRequest(s, tokens[0], "GET", "?limit=1", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), other.ID) || !strings.Contains(w.Body.String(), c.ID) {
		t.Fatal(w.Code, w.Body)
	}
	for _, body := range []string{`{"provider_key":"test","model":"secret-embedding"}`, `{"provider_key":"test","model":"text-model","tools":[]}`, `{"provider_key":"test","model":"text-model","owner_user_id":"stolen"}`} {
		if w := personalRequest(s, tokens[0], "POST", "", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	for _, body := range []string{`{"content":[{"type":"image_url"}],"request_id":"bad"}`, `{"content":"hello","request_id":"bad","messages":[]}`, `{"content":"hello","request_id":"bad","tools":[]}`, `{"content":"hello","request_id":"bad","model":"override"}`} {
		if w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", body); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	w = personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: accepted\n") || !strings.Contains(w.Body.String(), "event: delta\n") || !strings.Contains(w.Body.String(), "event: done\n") {
		t.Fatal(w.Code, w.Body)
	}
	store := s.store.(service.PersonalChatStorer)
	messages, err := store.ListPersonalMessages(t.Context(), owners[0], c.ID, "", 50)
	if err != nil || len(messages) != 2 {
		t.Fatal(messages, err)
	}
	m := messages[0]
	if m.Role != "assistant" || m.Status != "completed" || m.Content != "Hi" || m.Usage.TotalTokens != 6 || m.FinishReason != "stop" {
		t.Fatal(m)
	}
	encoded, _ := json.Marshal(m)
	if !strings.Contains(w.Body.String(), "event: done\ndata: "+string(encoded)) {
		t.Fatal("terminal differs from DB", w.Body, string(encoded))
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		calls, err := s.llmCallStore.ListLLMCalls(t.Context(), &query.Query{})
		if err != nil {
			t.Fatal(err)
		}
		if len(calls.Data) > 0 {
			call, err := s.llmCallStore.GetLLMCall(t.Context(), calls.Data[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if call.TraceID != m.ID || call.SessionID != c.ID || call.UserField != owners[0] || call.InputTokens != 4 || call.OutputTokens != 2 || call.RequestBody != "" || call.ResponseBody != "" || call.RequestRef != "" || call.ResponseRef != "" {
				t.Fatal(call)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("generation trace not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
	costs, err := s.costEventStore.ListCostEvents(t.Context(), &query.Query{})
	if err != nil || len(costs.Data) != 1 || costs.Data[0].AgentID != "personal:"+owners[0] || costs.Data[0].InputTokens != 4 {
		t.Fatal(costs, err)
	}
	for _, path := range []string{"/" + c.ID, "/" + c.ID + "/messages", "/" + c.ID + "/messages/" + m.ID, "/" + other.ID + "/messages/" + m.ID} {
		if w := personalRequest(s, tokens[1], "GET", path, ""); w.Code != 404 {
			t.Fatal(path, w.Code, w.Body)
		}
	}
	for _, tc := range []struct{ method, path, body string }{{"PATCH", "/" + c.ID, `{"title":"stolen"}`}, {"DELETE", "/" + c.ID, ""}, {"POST", "/" + c.ID + "/messages", `{"content":"hello","request_id":"one"}`}, {"POST", "/" + c.ID + "/messages/" + m.ID + "/cancel", ""}} {
		if w := personalRequest(s, tokens[1], tc.method, tc.path, tc.body); w.Code != 404 {
			t.Fatal(tc, w.Code, w.Body)
		}
	}
	w = personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"replay":true`) || !strings.Contains(w.Body.String(), "event: snapshot") || strings.Contains(w.Body.String(), "event: delta") || provider.calls.Load() != 1 {
		t.Fatal(w.Code, w.Body, provider.calls.Load())
	}
	if w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"changed","request_id":"one"}`); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	// Browser and mobile credentials see exactly the same owner-scoped database history.
	cookie := nativeLoginCookie(t, s.server, "admin-a")
	w = nativeRequest(s.server, "GET", "/at/api/v1/conversations/"+c.ID+"/messages", "", "", cookie)
	if w.Code != 200 || !strings.Contains(w.Body.String(), m.ID) {
		t.Fatal(w.Code, w.Body)
	}
	if w := personalRequest(s, tokens[0], "PATCH", "/"+c.ID, `{"title":"Saved","system_prompt":"Changed"}`); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	if w := personalRequest(s, tokens[0], "DELETE", "/"+c.ID, ""); w.Code != 204 {
		t.Fatal(w.Code, w.Body)
	}
}

func TestPersonalChatHTTPFailures(t *testing.T) {
	provider := &personalTestProvider{}
	s, owners, tokens := personalHTTPFixture(t, provider)
	for _, tc := range []struct {
		name   string
		chunks []service.StreamChunk
		err    error
		code   string
	}{
		{"setup", nil, errors.New("Bearer upstream-super-secret"), "provider_error"},
		{"midstream", []service.StreamChunk{{Content: "partial"}, {Error: errors.New("upstream-super-secret")}}, nil, "provider_error"},
		{"missing finish", []service.StreamChunk{{Content: "partial"}}, nil, "incomplete_stream"},
		{"tools", []service.StreamChunk{{ToolCalls: []service.ToolCall{{Name: "unsafe"}}}}, nil, "unsupported_output"},
		{"image", []service.StreamChunk{{InlineImages: []service.InlineImage{{Data: "secret"}}}}, nil, "unsupported_output"},
		{"output cap", []service.StreamChunk{{Content: strings.Repeat("x", personalOutputLimit+1)}}, nil, "output_limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.stream = func(context.Context, string, []service.Message) (<-chan service.StreamChunk, error) {
				ch := make(chan service.StreamChunk, len(tc.chunks))
				for _, c := range tc.chunks {
					ch <- c
				}
				close(ch)
				return ch, tc.err
			}
			c := personalCreate(t, s, tokens[0])
			w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "upstream-super-secret") {
				t.Fatal(w.Code, w.Body)
			}
			messages, err := s.store.(service.PersonalChatStorer).ListPersonalMessages(t.Context(), owners[0], c.ID, "", 50)
			if err != nil || messages[0].Status != "failed" || messages[0].Error != tc.code {
				t.Fatal(messages, err)
			}
		})
	}
}

func TestPersonalChatDisconnectAndSharedCancel(t *testing.T) {
	for _, disconnect := range []bool{true, false} {
		t.Run(map[bool]string{true: "disconnect", false: "cross replica cancel"}[disconnect], func(t *testing.T) {
			stopped := make(chan struct{})
			provider := &personalTestProvider{stream: func(ctx context.Context, _ string, _ []service.Message) (<-chan service.StreamChunk, error) {
				ch := make(chan service.StreamChunk)
				go func() {
					defer close(ch)
					defer close(stopped)
					select {
					case ch <- service.StreamChunk{Content: "partial"}:
					case <-ctx.Done():
						return
					}
					<-ctx.Done()
					// Model adapters that already buffered input and use blocking sends.
					for range 128 {
						ch <- service.StreamChunk{Content: "buffered after cancellation"}
					}
				}()
				return ch, nil
			}}
			s, owners, tokens := personalHTTPFixture(t, provider)
			c := personalCreate(t, s, tokens[0])
			httpServer := httptest.NewServer(s.server)
			defer httpServer.Close()
			r, _ := http.NewRequest("POST", httpServer.URL+"/at/api/v1/conversations/"+c.ID+"/messages", strings.NewReader(`{"content":"hello","request_id":"one"}`))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+tokens[0])
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			scanner := bufio.NewScanner(resp.Body)
			for scanner.Scan() {
				if scanner.Text() == "event: delta" {
					break
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			store := s.store.(service.PersonalChatStorer)
			messages, err := store.ListPersonalMessages(t.Context(), owners[0], c.ID, "", 50)
			if err != nil {
				t.Fatal(err)
			}
			mid := messages[0].ID
			if w := personalRequest(s, tokens[0], "DELETE", "/"+c.ID, ""); w.Code != 409 {
				t.Fatal(w.Code, w.Body)
			}
			if w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"other","request_id":"two"}`); w.Code != 409 {
				t.Fatal(w.Code, w.Body)
			}
			w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "event: snapshot") || strings.Contains(w.Body.String(), "event: done") || provider.calls.Load() != 1 {
				t.Fatal(w.Code, w.Body)
			}
			if disconnect {
				resp.Body.Close()
			} else {
				// A different Server has no process-local cancellation state.
				other := &Server{store: s.store, nativeAuth: s.nativeAuth}
				request := httptest.NewRequest("POST", "/cancel", nil)
				request.SetPathValue("id", c.ID)
				request.SetPathValue("message_id", mid)
				request.Header.Set("Authorization", "Bearer "+tokens[0])
				recorder := httptest.NewRecorder()
				s.nativeAuth.require(true)(http.HandlerFunc(other.PersonalChatAPI)).ServeHTTP(recorder, request)
				if recorder.Code != 200 {
					t.Fatal(recorder.Code, recorder.Body)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
			}
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("upstream not cancelled")
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				m, err := store.GetPersonalMessage(t.Context(), owners[0], c.ID, mid)
				if err != nil {
					t.Fatal(err)
				}
				if m.Status == "cancelled" {
					if disconnect && m.Content != "partial" {
						t.Fatal(m)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("not durably cancelled", m)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestPersonalChatNativeOff(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.PersonalChatModelsAPI(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "requires native authentication") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestPersonalChatStoredWindow(t *testing.T) {
	provider := &personalTestProvider{stream: func(_ context.Context, _ string, messages []service.Message) (<-chan service.StreamChunk, error) {
		if messages[0].Role != "system" || messages[0].Content != "Be precise" || messages[len(messages)-1].Content != "current" {
			t.Errorf("lost stored settings/current message: %+v", messages)
		}
		size := 0
		for _, m := range messages {
			size += len(m.Content.(string))
		}
		if size > 32768*4 || len(messages) >= 18 {
			t.Errorf("unbounded prompt: %d bytes, %d messages", size, len(messages))
		}
		ch := make(chan service.StreamChunk, 1)
		ch <- service.StreamChunk{Content: "answer", FinishReason: "stop"}
		close(ch)
		return ch, nil
	}}
	s, owners, tokens := personalHTTPFixture(t, provider)
	c := personalCreate(t, s, tokens[0])
	store := s.store.(service.PersonalChatStorer)
	for i := range 8 {
		turn, err := store.BeginPersonalTurn(t.Context(), owners[0], c.ID, string(rune('a'+i)), strings.Repeat("u", personalTextLimit))
		if err != nil {
			t.Fatal(err)
		}
		turn.Assistant.Content = strings.Repeat("a", personalTextLimit)
		turn.Assistant.Status = "completed"
		if _, err := store.CheckpointPersonalTurn(t.Context(), owners[0], c.ID, turn.LeaseToken, turn.Assistant); err != nil {
			t.Fatal(err)
		}
	}
	w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"current","request_id":"current"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") {
		t.Fatal(w.Code, w.Body)
	}
	messages, err := store.ListPersonalMessages(t.Context(), owners[0], c.ID, "", 100)
	if err != nil || len(messages) != 18 {
		t.Fatal(len(messages), err)
	}
	page, err := store.ListPersonalMessages(t.Context(), owners[0], c.ID, messages[0].ID, 1)
	if err != nil || len(page) != 1 || page[0].ID != messages[1].ID {
		t.Fatal(page, err)
	}
}

type personalFailTerminalStore struct {
	service.ProviderStorer
	service.PersonalChatStorer
}

func (p personalFailTerminalStore) CheckpointPersonalTurn(ctx context.Context, owner, id, token string, m service.PersonalChatMessage) (*service.PersonalChatMessage, error) {
	if !m.Active() {
		return nil, errors.New("database password must not leak")
	}
	return p.PersonalChatStorer.CheckpointPersonalTurn(ctx, owner, id, token, m)
}

func TestPersonalChatNoTerminalBeforeCommit(t *testing.T) {
	provider := &personalTestProvider{stream: func(context.Context, string, []service.Message) (<-chan service.StreamChunk, error) {
		ch := make(chan service.StreamChunk, 1)
		ch <- service.StreamChunk{Content: "answer", FinishReason: "stop"}
		close(ch)
		return ch, nil
	}}
	s, owners, tokens := personalHTTPFixture(t, provider)
	c := personalCreate(t, s, tokens[0])
	store := s.store.(service.PersonalChatStorer)
	s.store = personalFailTerminalStore{ProviderStorer: s.store, PersonalChatStorer: store}
	w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "event: done") || strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "password") {
		t.Fatal(w.Code, w.Body)
	}
	messages, err := store.ListPersonalMessages(t.Context(), owners[0], c.ID, "", 100)
	if err != nil || !messages[0].Active() {
		t.Fatal(messages, err)
	}
}

func TestPersonalChatOpenAIAdapterFinish(t *testing.T) {
	for _, finish := range []bool{true, false} {
		t.Run(map[bool]string{true: "finish without DONE", false: "DONE without finish"}[finish], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Model    string            `json:"model"`
					Messages []service.Message `json:"messages"`
					Tools    []service.Tool    `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if req.Model != "text-model" || len(req.Tools) != 0 || len(req.Messages) != 2 || req.Messages[0].Content != "Be precise" || r.Header.Get("Authorization") != "Bearer upstream-private" {
					t.Errorf("upstream request: %+v", req)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Provider-Secret", "upstream-private")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"real adapter\"}}]}\n\n")
				if finish {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n")
				} else {
					_, _ = io.WriteString(w, "data: [DONE]\n\n")
				}
			}))
			defer upstream.Close()
			provider, err := openai.New("upstream-private", "text-model", upstream.URL, "", false, nil)
			if err != nil {
				t.Fatal(err)
			}
			s, owners, tokens := personalHTTPFixture(t, provider)
			c := personalCreate(t, s, tokens[0])
			w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
			if w.Code != 200 || w.Header().Get("X-Provider-Secret") != "" || strings.Contains(w.Body.String(), "upstream-private") {
				t.Fatal(w.Code, w.Body)
			}
			messages, err := s.store.(service.PersonalChatStorer).ListPersonalMessages(t.Context(), owners[0], c.ID, "", 50)
			if err != nil {
				t.Fatal(err)
			}
			if finish {
				if messages[0].Status != "completed" || messages[0].Usage.TotalTokens != 5 {
					t.Fatal(messages[0])
				}
			} else if messages[0].Status != "failed" || messages[0].Error != "incomplete_stream" {
				t.Fatal(messages[0])
			}
		})
	}
}

type personalSyncProvider struct{}

func (personalSyncProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return &service.LLMResponse{Content: "synchronous answer", Finished: true, FinishReason: "stop", Usage: service.Usage{PromptTokens: 2, CompletionTokens: 3}}, nil
}

func TestPersonalChatChatOnlyProvider(t *testing.T) {
	s, owners, tokens := personalHTTPFixture(t, personalSyncProvider{})
	c := personalCreate(t, s, tokens[0])
	w := personalRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"content":"hello","request_id":"one"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "event: done") || strings.Count(w.Body.String(), "event: delta") != 1 {
		t.Fatal(w.Code, w.Body)
	}
	messages, err := s.store.(service.PersonalChatStorer).ListPersonalMessages(t.Context(), owners[0], c.ID, "", 50)
	if err != nil || messages[0].Content != "synchronous answer" || messages[0].FinishReason != "stop" || messages[0].Usage.TotalTokens != 5 {
		t.Fatal(messages, err)
	}
}
