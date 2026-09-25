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
// Streaming falls back too, bounded by the commitment boundary. An earlier
// revision disabled it on the grounds that SSE headers had already been flushed;
// that is not true at the point the decision is made. w.Header().Set only
// populates the header map, and the upstream stream is opened before any
// Write/Flush — so an open failure leaves the response completely unwritten and
// a different target can serve it invisibly. Once the first chunk is written the
// response is committed and the chain stops; handleStreamingChat reports that
// through its `committed` return, set at exactly one place.

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
	if errors.Is(err, service.ErrProviderBudgetExceeded) || errors.Is(err, service.ErrProviderUserLimit) || errors.Is(err, service.ErrProviderUserBlocked) {
		return true
	}
	var rle *service.RateLimitError
	if errors.As(err, &rle) {
		return true
	}
	var upstreamErr *service.UpstreamError
	if errors.As(err, &upstreamErr) {
		status := upstreamErr.StatusCode
		return status == http.StatusRequestTimeout ||
			status == http.StatusTooEarly ||
			status == http.StatusTooManyRequests ||
			status >= 500
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
func (s *Server) resolveModel(ctx context.Context, auth *authResult, fullModel string) (providerKey, actualModel string, info ProviderInfo, err error) {
	providerKey, actualModel, err = parseModelID(fullModel)
	if err != nil {
		return "", "", ProviderInfo{}, err
	}
	if !auth.isModelAllowed(providerKey, fullModel) {
		return "", "", ProviderInfo{}, fmt.Errorf("token does not have access to model %q", fullModel)
	}
	pInfo, ok := s.getProviderInfo(providerKey)
	if !ok {
		if routes, routeOK := s.store.(service.ProviderRouteStorer); routeOK && auth != nil && auth.token != nil {
			route, routeErr := routes.ResolveGatewayProviderRoute(ctx, auth.token.WorkspaceID, auth.token.OwnerUserID, providerKey, actualModel)
			if routeErr == nil && route != nil && s.providerFactory != nil {
				provider, createErr := s.cachedWorkspaceProvider(&route.Record)
				if createErr != nil {
					return "", "", ProviderInfo{}, createErr
				}
				provider = s.providerForRoute(route, provider, auth.token.OwnerUserID)
				pInfo = NewProviderInfo(provider, route.Record.Config).WithProviderID(route.Record.ID)
				pInfo.providerType = "virtual"
				pInfo.defaultModel = actualModel
				pInfo.models = []string{actualModel}
				return providerKey, actualModel, pInfo, nil
			}
		}
		return "", "", ProviderInfo{}, s.providerUnavailableError(providerKey)
	}
	if len(pInfo.models) > 0 && !pInfo.hasModel(actualModel) {
		return "", "", ProviderInfo{}, fmt.Errorf("model %q is not available for provider %q", actualModel, providerKey)
	}
	if pInfo.providerID != "" {
		ownerUserID := ""
		if auth != nil && auth.token != nil {
			ownerUserID = auth.token.OwnerUserID
		}
		route := &service.ProviderRoute{Record: service.ProviderRecord{ID: pInfo.providerID, Key: providerKey}, ActualModel: actualModel}
		pInfo.provider = s.providerForRoute(route, pInfo.provider, ownerUserID)
	}
	return providerKey, actualModel, pInfo, nil
}

// resolveRoutingProfile expands a bare model name into a stored chain.
//
// It only runs when the request supplied no at_fallbacks (explicit beats
// implicit) and the model carries no "/" — a qualified model is never looked up,
// so no request that works today can change meaning. A miss returns no targets
// and the caller falls through to the usual "model must be provider/model"
// rejection.
//
// The workspace comes from the presented token, not from a session principal;
// see service.GatewayRoutingProfileStorer.
func (s *Server) resolveRoutingProfile(ctx context.Context, auth *authResult, model string, fallbacks []string) (name string, targets []string) {
	if len(fallbacks) > 0 || model == "" || strings.Contains(model, "/") {
		return "", nil
	}
	store, ok := s.routingProfileStore.(service.GatewayRoutingProfileStorer)
	if !ok || auth == nil || auth.token == nil || auth.token.WorkspaceID == "" {
		return "", nil
	}

	profile, err := store.GetGatewayRoutingProfile(ctx, model, auth.token.WorkspaceID)
	if err != nil {
		// A lookup failure must not turn a routable request into a 500: the
		// caller still rejects an unqualified model, which is the pre-change
		// behaviour, and the error is visible in logs.
		slog.Error("routing profile lookup failed", "model", model, "error", err)
		return "", nil
	}
	if profile == nil || len(profile.Targets) == 0 {
		return "", nil
	}

	return profile.Name, profile.Targets
}

// expandRoutingProfileTargets returns an effective (primary, fallbacks) pair for
// handlers whose validation is structured around a single primary model rather
// than a chain — the Responses and Anthropic endpoints.
//
// Targets this token cannot use are dropped rather than left in front, so a
// denied first entry does not fail a request the chat endpoint would serve.
// ok is false when the name matched a profile whose every target is unusable;
// the caller reports that against the profile, not the individual models.
func (s *Server) expandRoutingProfileTargets(ctx context.Context, auth *authResult, model string, fallbacks []string) (name, primary string, rest []string, ok bool) {
	profileName, targets := s.resolveRoutingProfile(ctx, auth, model, fallbacks)
	if profileName == "" {
		return "", model, fallbacks, true
	}

	usable := make([]string, 0, len(targets))
	for _, m := range targets {
		if _, _, _, err := s.resolveModel(ctx, auth, m); err != nil {
			slog.Warn("routing profile: skipping unusable target",
				"profile", profileName, "model", m, "error", err.Error())
			continue
		}
		usable = append(usable, m)
	}
	if len(usable) == 0 {
		return profileName, "", nil, false
	}

	return profileName, usable[0], usable[1:], true
}

// chatCallChain returns the ordered list of (full model, providerKey,
// actualModel, info) to try, beginning with the primary and then each
// fallback. Entries that fail validation are skipped (with a warning
// log) so a single bad fallback doesn't break the whole chain.
//
// When the model names a routing profile, the profile's targets become the whole
// chain. Every target still goes through resolveModel, so a profile grants
// routing and never authorization.
func (s *Server) chatCallChain(ctx context.Context, auth *authResult, primary string, fallbacks []string) ([]chatCallTarget, string) {
	profileName, targets := s.resolveRoutingProfile(ctx, auth, primary, fallbacks)
	if profileName != "" {
		out := make([]chatCallTarget, 0, len(targets))
		for _, m := range targets {
			pKey, actual, info, err := s.resolveModel(ctx, auth, m)
			if err != nil {
				slog.Warn("routing profile: skipping unusable target",
					"profile", profileName, "model", m, "error", err.Error())
				continue
			}
			out = append(out, chatCallTarget{
				fullModel:   m,
				providerKey: pKey,
				actualModel: actual,
				info:        info,
			})
		}
		if len(out) == 0 {
			// Every target was denied or unavailable. Report it against the
			// profile rather than the individual models, which the caller may
			// not be permitted to enumerate.
			return []chatCallTarget{{
				fullModel: primary,
				err:       fmt.Errorf("routing profile %q has no usable target for this token", profileName),
			}}, profileName
		}

		return out, profileName
	}

	out := make([]chatCallTarget, 0, 1+len(fallbacks))
	for _, m := range append([]string{primary}, fallbacks...) {
		pKey, actual, info, err := s.resolveModel(ctx, auth, m)
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
	return out, ""
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
