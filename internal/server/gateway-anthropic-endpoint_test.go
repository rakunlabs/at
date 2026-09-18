package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// scriptedProvider answers with a fixed response, recording what it was asked,
// so the test can assert the translation the endpoint performed.
type scriptedProvider struct {
	gotModel    string
	gotMessages []service.Message
	gotTools    []service.Tool
	resp        *service.LLMResponse
	chunks      []service.StreamChunk
	err         error
}

func (p *scriptedProvider) Chat(_ context.Context, model string, messages []service.Message, tools []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
	p.gotModel = model
	p.gotMessages = messages
	p.gotTools = tools
	if p.err != nil {
		return nil, p.err
	}

	return p.resp, nil
}

// Proxy is part of service.LLMStreamProvider; without it the double is not a
// stream provider and the endpoint correctly falls back to a non-streaming call.
func (p *scriptedProvider) Proxy(http.ResponseWriter, *http.Request, string) error { return nil }

func (p *scriptedProvider) ChatStream(_ context.Context, model string, messages []service.Message, tools []service.Tool, _ *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	p.gotModel = model
	p.gotMessages = messages
	p.gotTools = tools
	if p.err != nil {
		return nil, nil, p.err
	}

	ch := make(chan service.StreamChunk, len(p.chunks))
	for _, c := range p.chunks {
		ch <- c
	}
	close(ch)

	return ch, http.Header{}, nil
}

func anthropicEndpointServer(t *testing.T, providers map[string]ProviderInfo) (*Server, *postgres.Postgres, context.Context) {
	t.Helper()

	p := postgrestest.New(t, nil)
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	s, err := New(ctx, cfg, providers, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}

	return s, p, ctx
}

func postAnthropic(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/at/gateway/v1/messages", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// Anthropic-native clients authenticate with x-api-key, not a bearer token.
	r.Header.Set("x-api-key", token)
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)

	return w
}

// An unmodified Anthropic-native client must reach the endpoint and get an
// Anthropic-shaped answer, with a bare model name resolved via a profile.
func TestAnthropicEndpointServesUnmodifiedClient(t *testing.T) {
	provider := &scriptedProvider{resp: &service.LLMResponse{
		Content:      "hello there",
		FinishReason: "stop",
		Usage:        service.Usage{PromptTokens: 11, CompletionTokens: 3},
	}}
	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"anthropic": {provider: provider, providerType: "anthropic", defaultModel: "claude-sonnet", models: []string{"claude-sonnet"}},
	})
	seedRoutingProfile(t, ctx, p, "my-stack", "anthropic/claude-sonnet")
	token := gatewayRoutingToken(t, ctx, p, "at_anthropic_endpoint_t", service.APIToken{})

	w := postAnthropic(t, s, token, `{
		"model":"my-stack","max_tokens":256,
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if got := w.Header().Get("x-at-routing-profile"); got != "my-stack" {
		t.Fatalf("routing profile header = %q", got)
	}

	var body anthropicResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — %s", err, w.Body)
	}
	if body.Type != "message" || body.Role != "assistant" {
		t.Fatalf("envelope = %+v", body)
	}
	if len(body.Content) != 1 || body.Content[0]["text"] != "hello there" {
		t.Fatalf("content = %+v", body.Content)
	}
	if body.StopReason != "end_turn" {
		t.Fatalf("stop_reason = %q", body.StopReason)
	}
	if body.Usage.InputTokens != 11 || body.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", body.Usage)
	}
	if !strings.HasPrefix(body.ID, "msg_") {
		t.Fatalf("id = %q", body.ID)
	}
}

// The provider must receive native content blocks, not an OpenAI round trip.
func TestAnthropicEndpointKeepsScreenshotNative(t *testing.T) {
	provider := &scriptedProvider{resp: &service.LLMResponse{Content: "a cat", FinishReason: "stop"}}
	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"anthropic": {provider: provider, providerType: "anthropic", defaultModel: "claude-sonnet", models: []string{"claude-sonnet"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_anthropic_media_tok", service.APIToken{})

	w := postAnthropic(t, s, token, `{
		"model":"anthropic/claude-sonnet","max_tokens":256,
		"messages":[
			{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"screenshot","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","content":[
				{"type":"text","text":"captured"},
				{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}
			]}]}
		]
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}

	var toolResult service.ContentBlock
	for _, m := range provider.gotMessages {
		blocks, ok := m.Content.([]service.ContentBlock)
		if !ok {
			continue
		}
		for _, b := range blocks {
			if b.Type == "tool_result" {
				toolResult = b
			}
		}
	}
	if toolResult.Type != "tool_result" {
		t.Fatalf("provider never saw a tool_result: %+v", provider.gotMessages)
	}

	parts, structured := toolResult.ContentBlocks()
	if !structured || len(parts) != 2 {
		t.Fatalf("tool result content = %#v", toolResult.Content)
	}
	if parts[1].Type != "image" || parts[1].Source == nil || parts[1].Source.Data != "aGk=" {
		t.Fatalf("screenshot did not reach the provider: %+v", parts[1])
	}
}

func TestAnthropicEndpointStreamEventOrder(t *testing.T) {
	provider := &scriptedProvider{chunks: []service.StreamChunk{
		{Content: "he"},
		{Content: "llo"},
		{FinishReason: "stop", Usage: &service.Usage{PromptTokens: 9, CompletionTokens: 2}},
	}}
	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"anthropic": {provider: provider, providerType: "anthropic", defaultModel: "claude-sonnet", models: []string{"claude-sonnet"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_anthropic_stream_tk", service.APIToken{})

	w := postAnthropic(t, s, token, `{
		"model":"anthropic/claude-sonnet","max_tokens":256,"stream":true,
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type = %q", ct)
	}

	events := sseEventNames(w.Body.String())
	want := []string{
		"message_start",
		"content_block_start",
		"content_block_delta",
		"content_block_delta",
		"content_block_stop",
		"message_delta",
		"message_stop",
	}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Fatalf("events = %v\nwant    %v", events, want)
	}

	if !strings.Contains(w.Body.String(), `"output_tokens":2`) {
		t.Fatalf("message_delta should report output tokens: %s", w.Body)
	}
}

func TestAnthropicEndpointStreamsToolCallAsInputJSONDelta(t *testing.T) {
	provider := &scriptedProvider{chunks: []service.StreamChunk{
		{ToolCalls: []service.ToolCall{{ID: "toolu_1", Name: "screenshot", Arguments: map[string]any{"url": "a"}}}},
		{FinishReason: "tool_calls"},
	}}
	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"anthropic": {provider: provider, providerType: "anthropic", defaultModel: "claude-sonnet", models: []string{"claude-sonnet"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_anthropic_tool_strm", service.APIToken{})

	w := postAnthropic(t, s, token, `{
		"model":"anthropic/claude-sonnet","max_tokens":256,"stream":true,
		"messages":[{"role":"user","content":"hi"}]
	}`)

	body := w.Body.String()
	if !strings.Contains(body, `"type":"tool_use"`) {
		t.Fatalf("no tool_use block start: %s", body)
	}
	if !strings.Contains(body, `"type":"input_json_delta"`) {
		t.Fatalf("tool arguments must stream as input_json_delta: %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Fatalf("stop_reason should be tool_use: %s", body)
	}
}

// Errors must use the Anthropic envelope; the OpenAI shape would fall through a
// client's error handling to its generic branch.
func TestAnthropicEndpointErrorEnvelope(t *testing.T) {
	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"anthropic": {provider: &scriptedProvider{}, providerType: "anthropic", defaultModel: "claude-sonnet", models: []string{"claude-sonnet"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_anthropic_error_tok", service.APIToken{})

	t.Run("missing credential is an authentication_error", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/at/gateway/v1/messages", strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		s.server.ServeHTTP(w, r)

		assertAnthropicError(t, w, http.StatusUnauthorized, anthropicErrAuthentication)
	})

	t.Run("missing max_tokens is an invalid_request_error", func(t *testing.T) {
		w := postAnthropic(t, s, token, `{"model":"anthropic/claude-sonnet","messages":[{"role":"user","content":"hi"}]}`)
		assertAnthropicError(t, w, http.StatusBadRequest, anthropicErrInvalidRequest)
	})

	t.Run("unknown model is a not_found_error", func(t *testing.T) {
		w := postAnthropic(t, s, token, `{"model":"ghost/model","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
		assertAnthropicError(t, w, http.StatusNotFound, anthropicErrNotFound)
	})

	t.Run("unresolvable bare name is an invalid_request_error", func(t *testing.T) {
		w := postAnthropic(t, s, token, `{"model":"nonexistent-profile","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`)
		assertAnthropicError(t, w, http.StatusBadRequest, anthropicErrInvalidRequest)
	})
}

func assertAnthropicError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantType string) {
	t.Helper()

	if w.Code != wantStatus {
		t.Fatalf("status %d want %d: %s", w.Code, wantStatus, w.Body)
	}

	var body struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v — %s", err, w.Body)
	}
	if body.Type != "error" {
		t.Fatalf("envelope type = %q want error: %s", body.Type, w.Body)
	}
	if body.Error.Type != wantType {
		t.Fatalf("error type = %q want %q", body.Error.Type, wantType)
	}
	if body.Error.Message == "" {
		t.Fatal("error message must not be empty")
	}
}

// sseEventNames extracts the ordered `event:` names from an SSE body.
func sseEventNames(body string) []string {
	var names []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "event: ") {
			names = append(names, strings.TrimPrefix(line, "event: "))
		}
	}

	return names
}
