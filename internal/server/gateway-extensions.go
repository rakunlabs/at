package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── B1: Model fallbacks ───
//
// When a client supplies `at_fallbacks: ["provider/model", ...]`, the
// gateway tries the primary model first. If it fails with a retryable
// upstream error (a *service.RateLimitError, an HTTP 5xx, or context
// deadline exceeded), the gateway moves to the next entry. The actual
// model that produced the eventual response is reflected in the
// `x-at-model-used` header.
//
// Fallback is OFF for streaming today — once we've flushed SSE headers
// to the client we can't restart cleanly. Streaming clients should
// supply a single model.

// shouldFallback reports whether the given error is worth swapping the
// model for. RateLimitError, 5xx, timeouts, and connection errors all
// qualify. 4xx (other than 429) does NOT — the input is malformed and a
// different model won't help.
func shouldFallback(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var rle *service.RateLimitError
	if errors.As(err, &rle) {
		return true
	}
	// HTTP status classification falls back to the response-shaping logic:
	// status, _ := classifyGatewayError → use that to decide
	status, _ := classifyGatewayError(err)
	if status >= 500 {
		return true
	}
	// 408 Request Timeout, 425 Too Early — transient
	if status == http.StatusRequestTimeout || status == http.StatusTooEarly {
		return true
	}
	return false
}

// ─── B3: Mock response ───
//
// When MockResponse is set the gateway returns a synthesized response
// without calling any upstream. Useful for SDK / integration tests.

// buildMockChatResponse synthesises an OpenAI-shaped chat completion.
func buildMockChatResponse(model, content string) *ChatCompletionResponse {
	c := content
	msg := ChatCompletionMessage{
		Role:    "assistant",
		Content: &c,
	}
	return &ChatCompletionResponse{
		ID:      generateChatID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatCompletionChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: "stop",
		}},
		Usage: ChatCompletionUsage{
			PromptTokens:     0,
			CompletionTokens: len(content) / 4, // rough char→token estimate
			TotalTokens:      len(content) / 4,
		},
	}
}

// writeMockChatStream emits one role chunk, one content chunk, and a
// finish chunk (matching the real streaming path) so SDK clients see a
// well-formed mocked stream.
func writeMockChatStream(w http.ResponseWriter, model, content string, includeUsage bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "streaming not supported by this server",
				"type":    "server_error",
			},
		}, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("x-at-mock-response", "true")

	chatID := generateChatID()
	writeSSEChunk(w, flusher, ChatCompletionChunk{
		ID:     chatID,
		Object: "chat.completion.chunk",
		Model:  model,
		Choices: []ChunkChoice{{
			Index: 0,
			Delta: ChunkDelta{Role: "assistant"},
		}},
	})
	writeSSEChunk(w, flusher, ChatCompletionChunk{
		ID:     chatID,
		Object: "chat.completion.chunk",
		Model:  model,
		Choices: []ChunkChoice{{
			Index: 0,
			Delta: ChunkDelta{Content: content},
		}},
	})
	finishReason := "stop"
	writeSSEChunk(w, flusher, ChatCompletionChunk{
		ID:     chatID,
		Object: "chat.completion.chunk",
		Model:  model,
		Choices: []ChunkChoice{{
			Index:        0,
			Delta:        ChunkDelta{},
			FinishReason: &finishReason,
		}},
	})
	if includeUsage {
		writeSSEChunk(w, flusher, ChatCompletionChunk{
			ID:      chatID,
			Object:  "chat.completion.chunk",
			Model:   model,
			Choices: []ChunkChoice{},
			Usage: &ChatCompletionUsage{
				CompletionTokens: len(content) / 4,
				TotalTokens:      len(content) / 4,
			},
		})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// ─── B4: Idempotency ───
//
// When a request carries an `Idempotency-Key` header, the gateway
// remembers the first response for (token_id, key) for 5 minutes and
// replays it on subsequent identical-key requests. The actual request
// body is NOT compared; that's the OpenAI semantic — the header alone
// is the dedup key. Clients that send the same key with a different
// body get the original response back.

type idempotencyEntry struct {
	statusCode int
	headers    http.Header
	body       []byte
	expiresAt  time.Time
}

// idempotencyCache is a tiny in-memory cache. It lives on Server (see
// the field below) but we keep state here to keep the wiring minimal.
type idempotencyCache struct {
	mu      sync.Mutex
	entries map[string]idempotencyEntry
}

func newIdempotencyCache() *idempotencyCache {
	return &idempotencyCache{entries: make(map[string]idempotencyEntry)}
}

// get returns the cached entry for the key, expiring stale rows. The
// caller must hold no locks.
func (c *idempotencyCache) get(key string) (idempotencyEntry, bool) {
	if c == nil || key == "" {
		return idempotencyEntry{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return idempotencyEntry{}, false
	}
	if time.Now().After(e.expiresAt) {
		delete(c.entries, key)
		return idempotencyEntry{}, false
	}
	return e, true
}

func (c *idempotencyCache) put(key string, e idempotencyEntry) {
	if c == nil || key == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Light eviction: every put, sweep entries older than now.
	now := time.Now()
	for k, v := range c.entries {
		if now.After(v.expiresAt) {
			delete(c.entries, k)
		}
	}
	c.entries[key] = e
}

// idempotencyKey returns a token-scoped cache key, or "" when the request
// has no Idempotency-Key header.
func idempotencyKey(r *http.Request, auth *authResult) string {
	hdr := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if hdr == "" {
		return ""
	}
	tokenID := "anon"
	if auth != nil && auth.token != nil {
		tokenID = auth.token.ID
	}
	return tokenID + "|" + r.URL.Path + "|" + hdr
}

// captureResponseWriter buffers status + body + headers so we can store a
// completed response in the idempotency cache. We forward to the real
// writer only after the handler finishes.
type captureResponseWriter struct {
	hdr        http.Header
	body       []byte
	statusCode int
	written    bool
}

func newCaptureWriter() *captureResponseWriter {
	return &captureResponseWriter{hdr: make(http.Header), statusCode: http.StatusOK}
}

func (w *captureResponseWriter) Header() http.Header { return w.hdr }

func (w *captureResponseWriter) WriteHeader(code int) {
	if w.written {
		return
	}
	w.statusCode = code
	w.written = true
}

func (w *captureResponseWriter) Write(p []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	w.body = append(w.body, p...)
	return len(p), nil
}

// flushTo writes the captured response to the real writer.
func (w *captureResponseWriter) flushTo(real http.ResponseWriter) {
	for k, vals := range w.hdr {
		for _, v := range vals {
			real.Header().Add(k, v)
		}
	}
	if w.statusCode > 0 {
		real.WriteHeader(w.statusCode)
	}
	if len(w.body) > 0 {
		_, _ = real.Write(w.body)
	}
}

// ─── B6: Per-call timeout ───

// withRequestTimeout wraps ctx with a timeout when timeoutMs > 0 and
// returns the new ctx + a cancel func that the caller must defer.
// A zero/negative timeoutMs returns the original ctx with a no-op cancel.
func withRequestTimeout(ctx context.Context, timeoutMs int) (context.Context, context.CancelFunc) {
	if timeoutMs <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
}

// ─── Per-call provider resolution for fallback chains ───

// resolveModel parses a "provider/model" string, validates token access,
// looks up the provider, and confirms the model is in the provider's
// strict-list (if any). It does NOT write any HTTP response on failure;
// the caller decides how to surface the error (e.g. try the next
// fallback or 404 the request).
func (s *Server) resolveModel(auth *authResult, fullModel string) (providerKey, actualModel string, info ProviderInfo, err error) {
	providerKey, actualModel, err = parseModelID(fullModel)
	if err != nil {
		return "", "", ProviderInfo{}, err
	}
	if !auth.isModelAllowed(providerKey, fullModel) {
		return "", "", ProviderInfo{}, fmt.Errorf("token does not have access to model %q", fullModel)
	}
	pInfo, ok := s.getProviderInfo(providerKey)
	if !ok {
		return "", "", ProviderInfo{}, fmt.Errorf("provider %q not found", providerKey)
	}
	if len(pInfo.models) > 0 && !pInfo.hasModel(actualModel) {
		return "", "", ProviderInfo{}, fmt.Errorf("model %q is not available for provider %q", actualModel, providerKey)
	}
	return providerKey, actualModel, pInfo, nil
}

// chatCallChain returns the ordered list of (full model, providerKey,
// actualModel, info) to try, beginning with the primary and then each
// fallback. Entries that fail validation are skipped (with a warning
// log) so a single bad fallback doesn't break the whole chain.
func (s *Server) chatCallChain(auth *authResult, primary string, fallbacks []string) []chatCallTarget {
	out := make([]chatCallTarget, 0, 1+len(fallbacks))
	for _, m := range append([]string{primary}, fallbacks...) {
		pKey, actual, info, err := s.resolveModel(auth, m)
		if err != nil {
			if m != primary {
				slog.Warn("gateway fallback: skipping invalid entry",
					"model", m, "error", err.Error())
				continue
			}
			// Primary is invalid — leave it in so the caller can return
			// a 4xx with a useful message.
			out = append(out, chatCallTarget{fullModel: m, err: err})
			continue
		}
		out = append(out, chatCallTarget{
			fullModel:   m,
			providerKey: pKey,
			actualModel: actual,
			info:        info,
		})
	}
	return out
}

type chatCallTarget struct {
	fullModel   string
	providerKey string
	actualModel string
	info        ProviderInfo
	err         error // non-nil only when this target failed validation
}

// jsonClone returns a deep-clone of v via marshal+unmarshal. Used to
// keep extra_body fully detached when retried across fallback models.
func jsonClone(v map[string]any) map[string]any {
	if len(v) == 0 {
		return nil
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return v // best-effort
	}
	var out map[string]any
	if json.Unmarshal(buf, &out) != nil {
		return v
	}
	return out
}
