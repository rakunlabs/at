package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// countingStreamProvider fails its stream open a configurable number of times.
type countingStreamProvider struct {
	name      string
	failWith  error
	opens     atomic.Int32
	chunks    []service.StreamChunk
	midStream error
}

func (p *countingStreamProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	if p.failWith != nil {
		return nil, p.failWith
	}

	return &service.LLMResponse{Content: p.name, FinishReason: "stop"}, nil
}

func (p *countingStreamProvider) Proxy(http.ResponseWriter, *http.Request, string) error { return nil }

func (p *countingStreamProvider) ChatStream(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (<-chan service.StreamChunk, http.Header, error) {
	p.opens.Add(1)
	if p.failWith != nil {
		return nil, nil, p.failWith
	}

	chunks := p.chunks
	if chunks == nil {
		chunks = []service.StreamChunk{{Content: p.name}, {FinishReason: "stop"}}
	}

	ch := make(chan service.StreamChunk, len(chunks)+1)
	for _, c := range chunks {
		ch <- c
	}
	if p.midStream != nil {
		ch <- service.StreamChunk{Error: p.midStream}
	}
	close(ch)

	return ch, http.Header{"anthropic-ratelimit-tokens-remaining": []string{"100"}}, nil
}

// fastGatewayRetry collapses the production retry backoff for tests that
// deliberately trigger it. Without it each 429 attempt sleeps 10s then 20s, and
// these few tests alone added ~2 minutes to the suite.
func fastGatewayRetry(t *testing.T) {
	t.Helper()
	t.Setenv("AT_GATEWAY_MIN_BACKOFF_MS", "1")
}

func postChatStream(t *testing.T, s *Server, token, body string) *httptest.ResponseRecorder {
	t.Helper()

	r := httptest.NewRequest(http.MethodPost, "/at/gateway/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)

	return w
}

// The change this test exists for: streaming used to take chain[0] and return,
// so at_fallbacks was inert for every coding CLI (they all stream).
func TestStreamingFallsBackBeforeFirstByte(t *testing.T) {
	fastGatewayRetry(t)

	primary := &countingStreamProvider{name: "primary", failWith: &service.RateLimitError{StatusCode: 429, Provider: "alpha"}}
	backup := &countingStreamProvider{name: "backup"}

	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"alpha": {provider: primary, providerType: "openai", defaultModel: "m", models: []string{"m"}},
		"beta":  {provider: backup, providerType: "openai", defaultModel: "m", models: []string{"m"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_stream_fallback_tok", service.APIToken{})

	w := postChatStream(t, s, token, `{
		"model":"alpha/m","stream":true,"at_fallbacks":["beta/m"],
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "backup") {
		t.Fatalf("stream was not served by the fallback: %s", w.Body)
	}
	if got := w.Header().Get("x-at-model-used"); got != "beta/m" {
		t.Fatalf("x-at-model-used = %q want beta/m", got)
	}
	if backup.opens.Load() != 1 {
		t.Fatalf("backup opened %d times", backup.opens.Load())
	}
}

// A malformed request is not fixed by a different model, so the chain must not
// advance and burn a second provider's quota.
func TestStreamingDoesNotFallBackOnNonRetryableError(t *testing.T) {
	primary := &countingStreamProvider{name: "primary", failWith: &service.UpstreamError{StatusCode: 400, Message: "bad request"}}
	backup := &countingStreamProvider{name: "backup"}

	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"alpha": {provider: primary, providerType: "openai", defaultModel: "m", models: []string{"m"}},
		"beta":  {provider: backup, providerType: "openai", defaultModel: "m", models: []string{"m"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_stream_nonretry_tok", service.APIToken{})

	w := postChatStream(t, s, token, `{
		"model":"alpha/m","stream":true,"at_fallbacks":["beta/m"],
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d want 400: %s", w.Code, w.Body)
	}
	if backup.opens.Load() != 0 {
		t.Fatal("a non-retryable error must not advance the chain")
	}
	if strings.Contains(w.Body.String(), "data:") {
		t.Fatalf("no partial stream may be emitted: %s", w.Body)
	}
}

// When everything fails the client gets a real status code and no half-stream.
func TestStreamingAllTargetsFailSurfacesStatus(t *testing.T) {
	fastGatewayRetry(t)

	primary := &countingStreamProvider{name: "primary", failWith: &service.RateLimitError{StatusCode: 429, Provider: "alpha"}}
	backup := &countingStreamProvider{name: "backup", failWith: &service.RateLimitError{StatusCode: 429, Provider: "beta"}}

	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"alpha": {provider: primary, providerType: "openai", defaultModel: "m", models: []string{"m"}},
		"beta":  {provider: backup, providerType: "openai", defaultModel: "m", models: []string{"m"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_stream_allfail_tokn", service.APIToken{})

	w := postChatStream(t, s, token, `{
		"model":"alpha/m","stream":true,"at_fallbacks":["beta/m"],
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d want 429: %s", w.Code, w.Body)
	}
	if backup.opens.Load() == 0 {
		t.Fatal("the chain should have advanced to the backup")
	}
	if strings.Contains(w.Body.String(), "chat.completion.chunk") {
		t.Fatalf("no partial stream may be emitted: %s", w.Body)
	}
}

// A single-target streaming request must behave exactly as before the change.
func TestStreamingSingleTargetUnchanged(t *testing.T) {
	only := &countingStreamProvider{name: "only"}

	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"alpha": {provider: only, providerType: "openai", defaultModel: "m", models: []string{"m"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_stream_single_tokn2", service.APIToken{})

	w := postChatStream(t, s, token, `{"model":"alpha/m","stream":true,"messages":[{"role":"user","content":"hi"}]}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, "only") || !strings.HasSuffix(body, "data: [DONE]\n\n") {
		t.Fatalf("stream = %s", body)
	}
	if got := w.Header().Get("x-at-model-used"); got != "" {
		t.Fatalf("a single-target request must not report a substitute model: %q", got)
	}
}

// Once bytes are out, the chain must stop: there is no way to start a different
// response behind them.
func TestStreamingDoesNotFallBackAfterCommitment(t *testing.T) {
	primary := &countingStreamProvider{
		name:      "primary",
		chunks:    []service.StreamChunk{{Content: "partial"}},
		midStream: &service.UpstreamError{StatusCode: 500, Message: "upstream died"},
	}
	backup := &countingStreamProvider{name: "backup"}

	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"alpha": {provider: primary, providerType: "openai", defaultModel: "m", models: []string{"m"}},
		"beta":  {provider: backup, providerType: "openai", defaultModel: "m", models: []string{"m"}},
	})
	token := gatewayRoutingToken(t, ctx, p, "at_stream_committed_tk", service.APIToken{})

	w := postChatStream(t, s, token, `{
		"model":"alpha/m","stream":true,"at_fallbacks":["beta/m"],
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if backup.opens.Load() != 0 {
		t.Fatal("a mid-stream failure must not advance the chain")
	}
	body := w.Body.String()
	if !strings.Contains(body, "partial") {
		t.Fatalf("the committed content should still have reached the client: %s", body)
	}
	if !strings.Contains(body, "stream error") {
		t.Fatalf("the failure should be reported on the open stream: %s", body)
	}
}

// Headers staged by a failed attempt must not appear on the response the next
// target serves.
func TestStreamingClearsStagedHeadersBetweenAttempts(t *testing.T) {
	w := httptest.NewRecorder()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("anthropic-ratelimit-tokens-remaining", "0")
	w.Header().Set("x-ratelimit-remaining-requests", "0")
	w.Header().Set("x-at-routing-profile", "my-stack")

	clearStreamHeaders(w)

	for _, k := range []string{"Content-Type", "Cache-Control", "X-Accel-Buffering", "anthropic-ratelimit-tokens-remaining", "x-ratelimit-remaining-requests"} {
		if got := w.Header().Get(k); got != "" {
			t.Errorf("%s survived: %q", k, got)
		}
	}
	// AT's own routing metadata describes the request, not the failed attempt.
	if got := w.Header().Get("x-at-routing-profile"); got != "my-stack" {
		t.Errorf("routing profile header must survive: %q", got)
	}
}

// The Anthropic endpoint shares the behaviour rather than diverging from it.
func TestAnthropicStreamingFallsBack(t *testing.T) {
	fastGatewayRetry(t)

	primary := &countingStreamProvider{name: "primary", failWith: &service.RateLimitError{StatusCode: 429, Provider: "alpha"}}
	backup := &countingStreamProvider{name: "backup"}

	s, p, ctx := anthropicEndpointServer(t, map[string]ProviderInfo{
		"alpha": {provider: primary, providerType: "anthropic", defaultModel: "m", models: []string{"m"}},
		"beta":  {provider: backup, providerType: "anthropic", defaultModel: "m", models: []string{"m"}},
	})
	seedRoutingProfile(t, ctx, p, "stack", "alpha/m", "beta/m")
	token := gatewayRoutingToken(t, ctx, p, "at_anthropic_fallbackk", service.APIToken{})

	w := postAnthropic(t, s, token, `{
		"model":"stack","max_tokens":128,"stream":true,
		"messages":[{"role":"user","content":"hi"}]
	}`)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "backup") {
		t.Fatalf("stream was not served by the fallback: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), "message_stop") {
		t.Fatalf("stream did not terminate cleanly: %s", w.Body)
	}
}
