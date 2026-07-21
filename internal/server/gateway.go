package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	str2duration "github.com/xhit/go-str2duration/v2"

	"github.com/rakunlabs/at/internal/service"
)

// tokenLastUsedThreshold is the minimum interval between DB writes for a
// token's last_used_at field. Updates within this window are skipped.
const tokenLastUsedThreshold = 5 * time.Minute

// authResult holds the outcome of authenticating a request.
// A nil token means unrestricted access (config token with no restrictions).
type authResult struct {
	token *service.APIToken // nil = unrestricted access
}

// isModelAllowed checks whether the given "provider/model" is permitted by this token.
// Unrestricted tokens (token == nil) always have full access.
func (a *authResult) isModelAllowed(providerKey, fullModelID string) bool {
	if a.token == nil {
		return true // unrestricted access
	}

	providerMode := service.ResolveAccessMode(a.token.AllowedProvidersMode, a.token.AllowedProviders)
	modelMode := service.ResolveAccessMode(a.token.AllowedModelsMode, a.token.AllowedModels)

	// If both are "all", no restrictions.
	if providerMode == service.AccessModeAll && modelMode == service.AccessModeAll {
		return true
	}

	// If both are "none", deny everything.
	if providerMode == service.AccessModeNone && modelMode == service.AccessModeNone {
		return false
	}

	// Check provider-level access (OR logic with model-level).
	if providerMode == service.AccessModeList {
		for _, p := range a.token.AllowedProviders {
			if p == providerKey {
				return true
			}
		}
	} else if providerMode == service.AccessModeAll {
		// Provider is unrestricted — allowed by provider.
		return true
	}
	// providerMode == "none" falls through to model check.

	// Check model-level access.
	if modelMode == service.AccessModeList {
		for _, m := range a.token.AllowedModels {
			if m == fullModelID {
				return true
			}
		}
	} else if modelMode == service.AccessModeAll {
		return true
	}

	return false
}

// ChatCompletions handles POST /gateway/v1/chat/completions.
// It accepts an OpenAI-compatible request, routes it to the correct backend
// provider based on the model prefix (e.g., "anthropic/claude-haiku-4-5"),
// and returns an OpenAI-compatible response.
//
// Supports AT extensions: at_fallbacks, extra_body, mock_response, timeout_ms,
// and the Idempotency-Key header (litellm-style ergonomics).
func (s *Server) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Auth check
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": authErr,
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		}, http.StatusUnauthorized)
		return
	}

	// B4: idempotency-key replay BEFORE we read the body. Replays the
	// stored response verbatim.
	idempKey := idempotencyKey(r, auth)
	if idempKey != "" {
		if e, ok := s.idempotency.get(idempKey); ok {
			for k, vals := range e.headers {
				for _, v := range vals {
					w.Header().Add(k, v)
				}
			}
			w.Header().Set("x-at-idempotent-replay", "true")
			w.WriteHeader(e.statusCode)
			_, _ = w.Write(e.body)
			return
		}
	}

	// Wrap the real writer so we can capture for idempotency caching.
	var cap *captureResponseWriter
	respW := w
	if idempKey != "" {
		cap = newCaptureWriter()
		respW = cap
	}

	// Parse request. We read the raw body first so the LLM audit log can
	// persist the exact bytes the client sent (Langfuse-style), then
	// unmarshal from the captured buffer.
	rawBody, _ := io.ReadAll(r.Body)
	var req ChatCompletionRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		httpResponseJSON(respW, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("invalid request body: %v", err),
				"type":    "invalid_request_error",
			},
		}, http.StatusBadRequest)
		s.maybeStoreIdempotent(idempKey, cap, w)
		return
	}

	// Trace/session correlation for the audit log.
	traceID, sessionID := auditTraceInfo(r)

	// B3: mock_response short-circuit. No provider lookup, no auth model
	// access checks beyond the basic token guard — the whole point of
	// mock mode is to be free.
	if req.MockResponse != "" {
		if req.Stream {
			writeMockChatStream(respW, req.Model, req.MockResponse, req.StreamOptions != nil && req.StreamOptions.IncludeUsage)
			s.maybeStoreIdempotent(idempKey, cap, w)
			return
		}
		respW.Header().Set("x-at-mock-response", "true")
		httpResponseJSON(respW, buildMockChatResponse(req.Model, req.MockResponse), http.StatusOK)
		s.maybeStoreIdempotent(idempKey, cap, w)
		return
	}

	// Resolve the call chain: primary + at_fallbacks.
	chain := s.chatCallChain(auth, req.Model, req.AtFallbacks)
	if len(chain) == 0 {
		httpResponseJSON(respW, map[string]any{
			"error": map[string]any{
				"message": "model field is required",
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusBadRequest)
		s.maybeStoreIdempotent(idempKey, cap, w)
		return
	}

	// If the primary failed validation (provider missing, model not
	// allowed, …), surface that immediately — fallbacks don't rescue an
	// invalid primary request.
	if first := chain[0]; first.err != nil {
		status := http.StatusBadRequest
		code := "model_not_found"
		if strings.Contains(first.err.Error(), "not have access") {
			status = http.StatusForbidden
		} else if strings.Contains(first.err.Error(), "not found") || strings.Contains(first.err.Error(), "not available") {
			status = http.StatusNotFound
		}
		httpResponseJSON(respW, map[string]any{
			"error": map[string]any{
				"message": first.err.Error(),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    code,
			},
		}, status)
		s.maybeStoreIdempotent(idempKey, cap, w)
		return
	}

	// Token budget checks once (DB tokens only).
	if limitMessage, resetErr := s.checkTokenLimits(r.Context(), auth); resetErr != nil {
		slog.Error("token limit check failed", "error", resetErr)
	} else if limitMessage != "" {
		httpResponseJSON(respW, map[string]any{
			"error": map[string]any{
				"message": limitMessage,
				"type":    "tokens",
				"code":    "rate_limit_exceeded",
			},
		}, http.StatusTooManyRequests)
		s.maybeStoreIdempotent(idempKey, cap, w)
		return
	}

	// B6: timeout — applied uniformly across all fallback attempts.
	callCtx, cancel := withRequestTimeout(r.Context(), req.TimeoutMs)
	defer cancel()

	// Build per-request generation options once. extra_body is cloned per
	// attempt to avoid cross-attempt mutation.
	baseOpts := buildChatOptions(&req)

	if req.Stream {
		// Streaming path: no fallback. Use the primary only.
		target := chain[0]
		messages, tools := s.buildProviderMessages(target.info.providerType, req.Messages, req.Tools)
		opts := cloneChatOptions(baseOpts)
		audit := streamAuditCtx{
			auth: auth, endpoint: r.URL.Path,
			traceID: traceID, sessionID: sessionID, userField: req.User,
			requestBody: rawBody, requestedModel: req.Model,
		}
		s.handleStreamingChat(w, r.WithContext(callCtx), auth, target.info.provider, target.info.RetryAfterCap(),
			target.providerKey, target.actualModel, target.fullModel, messages, tools, req.StreamOptions, opts, audit)
		return
	}

	// Non-streaming with fallback chain.
	var (
		lastErr      error
		used         chatCallTarget
		resp         *service.LLMResponse
		totalLatency int64
	)
	for i, target := range chain {
		if target.err != nil {
			continue
		}
		messages, tools := s.buildProviderMessages(target.info.providerType, req.Messages, req.Tools)
		opts := cloneChatOptions(baseOpts)

		callStart := time.Now()
		r2, err := callWithGatewayRetry(callCtx, target.providerKey, target.actualModel,
			target.info.RetryAfterCap(),
			func(ctx context.Context) (*service.LLMResponse, error) {
				return target.info.provider.Chat(ctx, target.actualModel, messages, tools, opts)
			})
		totalLatency += time.Since(callStart).Milliseconds()
		if err == nil {
			resp = r2
			used = target
			break
		}
		lastErr = err
		slog.Warn("provider chat failed",
			"attempt", i, "provider", target.providerKey, "model", target.actualModel, "error", err)
		s.recordUsageAsync(r.Context(), auth, target.fullModel, service.Usage{}, totalLatency, "error", classifyHTTPError(err), err.Error())
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "gateway", endpoint: r.URL.Path,
			traceID: traceID, sessionID: sessionID, userField: req.User,
			requestBody: rawBody, requestedModel: req.Model, fullModel: target.fullModel,
			latencyMs: totalLatency, status: "error",
			errCode: classifyHTTPError(err), errMsg: err.Error(),
		})
		if !shouldFallback(err) {
			break
		}
	}

	if resp == nil {
		// All attempts exhausted (or non-retryable error on the primary).
		status, body := classifyGatewayError(lastErr)
		addGatewayRateLimitHeaders(respW, lastErr)
		httpResponseJSON(respW, body, status)
		s.maybeStoreIdempotent(idempKey, cap, w)
		return
	}

	// Forward provider headers (e.g. rate limits).
	for k, v := range resp.Header {
		for _, val := range v {
			respW.Header().Add(k, val)
		}
	}
	if used.fullModel != req.Model {
		respW.Header().Set("x-at-model-used", used.fullModel)
	}

	s.cacheThoughtSignatures(resp.ToolCalls)
	chatResp := buildOpenAIResponse(generateChatID(), used.fullModel, resp)
	if costCents := s.estimateGatewayUsageCostCents(r.Context(), used.providerKey, used.actualModel, used.fullModel, resp.Usage); costCents > 0 {
		respW.Header().Set("x-at-response-cost-cents", fmt.Sprintf("%.6f", costCents))
	}

	s.recordUsageAsync(r.Context(), auth, used.fullModel, resp.Usage, totalLatency, "ok", "", "")
	if respBody, err := json.Marshal(chatResp); err == nil {
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "gateway", endpoint: r.URL.Path,
			traceID: traceID, sessionID: sessionID, userField: req.User,
			requestBody: rawBody, responseBody: respBody,
			requestedModel: req.Model, fullModel: used.fullModel,
			usage: resp.Usage, latencyMs: totalLatency, status: "ok",
			finishReason: chatRespFinishReason(chatResp),
		})
	}
	httpResponseJSON(respW, chatResp, http.StatusOK)
	s.maybeStoreIdempotent(idempKey, cap, w)
}

// buildProviderMessages translates the OpenAI-shape messages + tools into
// the provider-flavoured service.Message and service.Tool slices.
func (s *Server) buildProviderMessages(providerType string, msgs []OpenAIMessage, tools []OpenAITool) ([]service.Message, []service.Tool) {
	tt := translateOpenAITools(tools)
	switch providerType {
	case "anthropic", "minimax", "bedrock":
		// Bedrock's Converse API uses an Anthropic-style content-block
		// shape, so we reuse the same translator. The bedrock adapter
		// converts service.ContentBlock to Converse blocks internally.
		systemPrompt, messages := translateOpenAIToAnthropic(msgs)
		if systemPrompt != "" {
			messages = append([]service.Message{{Role: "system", Content: systemPrompt}}, messages...)
		}
		return messages, tt
	default:
		return translateOpenAIMessages(msgs, s.lookupThoughtSignature), tt
	}
}

// cloneChatOptions shallow-clones a ChatOptions value so concurrent /
// sequential fallback attempts don't mutate one another's extra_body etc.
// Pointer fields and maps are independently cloned.
func cloneChatOptions(in *service.ChatOptions) *service.ChatOptions {
	if in == nil {
		return nil
	}
	out := *in
	if len(in.ExtraBody) > 0 {
		out.ExtraBody = jsonClone(in.ExtraBody)
	}
	if len(in.Metadata) > 0 {
		out.Metadata = jsonClone(in.Metadata)
	}
	return &out
}

// maybeStoreIdempotent flushes the captured response into the real writer
// and stores it in the idempotency cache when key != "" and the captured
// status code is one we want to dedup (2xx + 4xx; we explicitly skip 5xx
// so transient upstream failures don't get pinned to the key).
func (s *Server) maybeStoreIdempotent(key string, capW *captureResponseWriter, real http.ResponseWriter) {
	if key == "" || capW == nil {
		return
	}
	defer capW.flushTo(real)
	if capW.statusCode >= 500 {
		return
	}
	s.idempotency.put(key, idempotencyEntry{
		statusCode: capW.statusCode,
		headers:    capW.hdr.Clone(),
		body:       append([]byte(nil), capW.body...),
		expiresAt:  time.Now().Add(5 * time.Minute),
	})
}

// ProxyRequest handles generic requests to provider-native endpoints.
// Paths:
//   - /gateway/v1/providers/{provider}/*
//   - /gateway/proxy/{provider}/* (legacy)
func (s *Server) ProxyRequest(w http.ResponseWriter, r *http.Request) {
	// Auth check
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": authErr,
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		}, http.StatusUnauthorized)
		return
	}

	if limitMessage, limitErr := s.checkTokenLimits(r.Context(), auth); limitErr != nil {
		slog.Error("token limit check failed", "error", limitErr)
	} else if limitMessage != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": limitMessage,
				"type":    "tokens",
				"code":    "rate_limit_exceeded",
			},
		}, http.StatusTooManyRequests)
		return
	}

	providerKey := r.PathValue("provider")
	proxyPath := "/" + strings.TrimPrefix(r.PathValue("*"), "/")

	// Look up provider
	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q not found", providerKey),
				"type":    "invalid_request_error",
			},
		}, http.StatusNotFound)
		return
	}

	proxyModel, hasProxyModel, err := extractProxyBodyModel(r)
	if err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("failed to read request body: %v", err),
				"type":    "invalid_request_error",
			},
		}, http.StatusBadRequest)
		return
	}

	// Token-level access check.
	// Native provider calls keep the provider's wire format. When the JSON body
	// exposes a top-level model field, still enforce the same token/model policy
	// used by the OpenAI-compatible gateway; otherwise fall back to provider-level
	// access because endpoints like files/models may not name a model at all.
	accessModel := providerKey + "/*"
	if hasProxyModel {
		accessModel = providerKey + "/" + proxyModel
	}
	if !auth.isModelAllowed(providerKey, accessModel) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("token does not have access to %q", accessModel),
				"type":    "invalid_request_error",
				"code":    "model_not_found",
			},
		}, http.StatusForbidden)
		return
	}
	if hasProxyModel && len(info.models) > 0 && !info.hasModel(proxyModel) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("model %q is not available for provider %q; available models: %v", proxyModel, providerKey, info.models),
				"type":    "invalid_request_error",
				"code":    "model_not_found",
			},
		}, http.StatusNotFound)
		return
	}

	// Forward request
	if sender, ok := info.provider.(interface {
		Proxy(w http.ResponseWriter, r *http.Request, path string) error
	}); ok {
		if err := sender.Proxy(w, r, proxyPath); err != nil {
			slog.Error("proxy request failed", "provider", providerKey, "path", proxyPath, "error", err)
			httpResponseJSON(w, map[string]any{
				"error": map[string]any{
					"message": fmt.Sprintf("proxy error: %v", err),
					"type":    "server_error",
				},
			}, http.StatusBadGateway)
		}
		return
	}

	httpResponseJSON(w, map[string]any{
		"error": map[string]any{
			"message": "provider does not support proxying",
			"type":    "server_error",
		},
	}, http.StatusNotImplemented)
}

// ListModels handles GET /gateway/v1/models.
// It returns all configured provider/model combinations in OpenAI format.
// If a provider has a models list, each model is advertised. Otherwise,
// only the default model is shown.
// When authenticated via a DB token with restrictions, models are filtered.
func (s *Server) ListModels(w http.ResponseWriter, r *http.Request) {
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": authErr,
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		}, http.StatusUnauthorized)
		return
	}

	var models []ModelData
	s.providerMu.RLock()
	for key, info := range s.providers {
		seen := make(map[string]bool)
		add := func(m string) {
			if m == "" || seen[m] {
				return
			}
			seen[m] = true
			fullID := key + "/" + m
			if auth.isModelAllowed(key, fullID) {
				models = append(models, ModelData{
					ID:      fullID,
					Object:  "model",
					OwnedBy: key,
				})
			}
		}

		if len(info.models) > 0 {
			for _, m := range info.models {
				add(m)
			}
		} else {
			add(info.defaultModel)
		}

		// Embedding models are advertised alongside chat models.
		for _, m := range info.embeddingModels {
			add(m)
		}
	}
	s.providerMu.RUnlock()

	httpResponseJSON(w, ModelsResponse{
		Object: "list",
		Data:   models,
	}, http.StatusOK)
}

// ─── Helpers ───

// parseModelID splits "provider_key/actual_model" into its parts.
// For models like "github/openai/gpt-4.1", the provider key is "github"
// and the actual model is "openai/gpt-4.1".
func parseModelID(model string) (providerKey, actualModel string, err error) {
	if model == "" {
		return "", "", fmt.Errorf("model field is required")
	}

	idx := strings.Index(model, "/")
	if idx < 0 {
		return "", "", fmt.Errorf(
			"model %q must use format \"provider/model\" (e.g., \"openai/gpt-4o\", \"anthropic/claude-haiku-4-5\")",
			model,
		)
	}

	providerKey = model[:idx]
	actualModel = model[idx+1:]

	if providerKey == "" || actualModel == "" {
		return "", "", fmt.Errorf("model %q has empty provider or model name", model)
	}

	return providerKey, actualModel, nil
}

func extractProxyBodyModel(r *http.Request) (string, bool, error) {
	if r.Body == nil || r.Body == http.NoBody || !isJSONContentType(r.Header.Get("Content-Type")) {
		return "", false, nil
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return "", false, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))

	if len(bytes.TrimSpace(body)) == 0 {
		return "", false, nil
	}

	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Model == "" {
		return "", false, nil
	}

	return payload.Model, true, nil
}

func isJSONContentType(contentType string) bool {
	contentType = strings.ToLower(contentType)
	return strings.Contains(contentType, "json")
}

// authenticateRequest validates the Authorization header.
// Returns an authResult on success, or an error message string on failure.
// When no token store is configured, all requests are rejected — at
// least one API token must exist (managed through the UI).
func (s *Server) authenticateRequest(r *http.Request) (*authResult, string) {
	auth := r.Header.Get("Authorization")
	bearerToken := strings.TrimPrefix(auth, "Bearer ")
	apiKeyToken := firstNonEmptyHeader(r.Header, "x-api-key", "x-goog-api-key", "api-key")
	if bearerToken == "" && apiKeyToken != "" {
		bearerToken = apiKeyToken
	}

	// If no auth is configured at all, reject everything.
	if s.tokenStore == nil {
		return nil, "no authentication configured; add a token via the UI"
	}

	if bearerToken == "" {
		return nil, "missing Authorization Bearer or provider-native API key header"
	}

	// Check DB token.
	if s.tokenStore != nil {
		hash := sha256.Sum256([]byte(bearerToken))
		tokenHash := hex.EncodeToString(hash[:])

		token, err := s.tokenStore.GetAPITokenByHash(r.Context(), tokenHash)
		if err != nil {
			slog.Error("token lookup failed", "error", err)
			return nil, "internal error during authentication"
		}

		if token != nil {
			// Check expiry.
			if token.ExpiresAt.Valid && token.ExpiresAt.V.Time.Before(time.Now().UTC()) {
				return nil, "token has expired"
			}

			// Throttled update of last_used_at (fire-and-forget).
			if last, ok := s.tokenLastUsed.Load(token.ID); !ok || time.Since(last.(time.Time)) >= tokenLastUsedThreshold {
				s.tokenLastUsed.Store(token.ID, time.Now())

				v, _ := s.tokenLastUsedMu.LoadOrStore(token.ID, &sync.Mutex{})
				mu := v.(*sync.Mutex)

				go func() {
					if !mu.TryLock() {
						return // another goroutine is already updating this token
					}
					defer mu.Unlock()

					if err := s.tokenStore.UpdateLastUsed(context.WithoutCancel(r.Context()), token.ID); err != nil {
						slog.Error("failed to update token last_used_at", "id", token.ID, "error", err)
					}
				}()
			}

			r.Header.Del("Authorization")
			r.Header.Del("x-api-key")
			r.Header.Del("x-goog-api-key")
			r.Header.Del("api-key")
			return &authResult{token: token}, ""
		}
	}

	return nil, "invalid or missing gateway token"
}

func firstNonEmptyHeader(h http.Header, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

// getProviderInfo looks up a provider by key, returning the full ProviderInfo.
func (s *Server) getProviderInfo(key string) (ProviderInfo, bool) {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	info, ok := s.providers[key]
	return info, ok
}

// hasModel checks if a model is in the provider's models list.
func (p *ProviderInfo) hasModel(model string) bool {
	for _, m := range p.models {
		if m == model {
			return true
		}
	}
	return false
}

// availableProviderKeys returns all configured provider names.
func (s *Server) availableProviderKeys() []string {
	s.providerMu.RLock()
	defer s.providerMu.RUnlock()
	keys := make([]string, 0, len(s.providers))
	for k := range s.providers {
		keys = append(keys, k)
	}
	return keys
}

// ─── Streaming ───

// handleStreamingChat handles a streaming chat completion request.
// It checks if the provider supports true streaming (LLMStreamProvider interface),
// and falls back to fake streaming if not.
func (s *Server) handleStreamingChat(
	w http.ResponseWriter,
	r *http.Request,
	auth *authResult,
	provider service.LLMProvider,
	retryAfterCap time.Duration,
	providerKey, actualModel, fullModel string,
	messages []service.Message,
	tools []service.Tool,
	streamOpts *StreamOptions,
	opts *service.ChatOptions,
	audit streamAuditCtx,
) {
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

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	chatID := generateChatID()

	// Determine whether the client requested usage reporting in the stream.
	includeUsage := streamOpts != nil && streamOpts.IncludeUsage

	// Try true streaming if the provider supports it.
	if sp, ok := provider.(service.LLMStreamProvider); ok {
		slog.Debug("streaming via provider", "provider", providerKey, "model", actualModel)

		// Retry the upstream connect on 429/529. The retry only applies
		// to the initial open — once we start streaming chunks back to
		// the client, we can't restart without resending headers, so a
		// mid-stream rate-limit just surfaces as a chunk error. (In
		// practice, upstream returns 429/529 on the initial response,
		// not in the middle of a token stream.)
		streamStart := time.Now()
		type streamOpenResult struct {
			chunks  <-chan service.StreamChunk
			headers http.Header
		}
		opened, err := callWithGatewayRetry(r.Context(), providerKey, actualModel,
			retryAfterCap,
			func(ctx context.Context) (streamOpenResult, error) {
				ch, h, e := sp.ChatStream(ctx, actualModel, messages, tools, opts)
				return streamOpenResult{chunks: ch, headers: h}, e
			})
		if err != nil {
			// Record the failed call for the usage dashboard.
			s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{},
				time.Since(streamStart).Milliseconds(), "error", classifyHTTPError(err), err.Error())
			s.recordLLMCallAsync(r.Context(), llmAuditParams{
				auth: auth, source: audit.resolveSource(), endpoint: audit.endpoint,
				traceID: audit.traceID, sessionID: audit.sessionID, userField: audit.userField,
				requestBody: audit.requestBody, requestedModel: audit.requestedModel, fullModel: fullModel,
				latencyMs: time.Since(streamStart).Milliseconds(), streamed: true, status: "error",
				errCode: classifyHTTPError(err), errMsg: err.Error(),
			})
			slog.Error("provider stream failed", "provider", providerKey, "error", err)
			// SSE headers haven't been committed yet (only set on the
			// ResponseWriter, not flushed), so we can still emit a
			// JSON error with an upstream-faithful status code.
			status, body := classifyGatewayError(err)
			addGatewayRateLimitHeaders(w, err)
			httpResponseJSON(w, body, status)
			return
		}
		chunks := opened.chunks
		headers := opened.headers

		// Forward provider headers (e.g. rate limits)
		for k, v := range headers {
			for _, val := range v {
				w.Header().Add(k, val)
			}
		}

		// First chunk: send role
		writeSSEChunk(w, flusher, ChatCompletionChunk{
			ID:     chatID,
			Object: "chat.completion.chunk",
			Model:  fullModel,
			Choices: []ChunkChoice{{
				Index: 0,
				Delta: ChunkDelta{Role: "assistant"},
			}},
		})

		// Accumulate usage from stream chunks (providers emit it on the
		// final chunk or as a separate usage-only chunk).
		var streamUsage *service.Usage

		// Accumulate the reconstructed assistant turn for the audit log.
		var (
			auditContent   strings.Builder
			auditReasoning strings.Builder
			auditToolCalls []service.ToolCall
			auditFinish    string
			ttftMs         int64
		)

		for chunk := range chunks {
			if chunk.Error != nil {
				slog.Error("stream chunk error", "provider", providerKey, "error", chunk.Error)
				s.recordLLMCallAsync(r.Context(), llmAuditParams{
					auth: auth, source: audit.resolveSource(), endpoint: audit.endpoint,
					traceID: audit.traceID, sessionID: audit.sessionID, userField: audit.userField,
					requestBody: audit.requestBody, requestedModel: audit.requestedModel, fullModel: fullModel,
					responseBody: streamAuditResponseBody(chatID, fullModel, auditContent.String(), auditReasoning.String(), auditToolCalls, "error", streamUsage),
					usage:        usageOrZero(streamUsage), latencyMs: time.Since(streamStart).Milliseconds(), ttftMs: ttftMs,
					streamed: true, status: "error", errCode: "provider_error", errMsg: fmt.Sprintf("%v", chunk.Error),
				})
				writeSSEError(w, flusher, chatID, fullModel, fmt.Sprintf("stream error: %v", chunk.Error))
				return
			}

			// Capture usage if present (don't emit it yet).
			if chunk.Usage != nil {
				usage := *chunk.Usage
				streamUsage = &usage
			}

			// Accumulate the assistant turn for the audit reconstruction.
			if ttftMs == 0 && (chunk.Content != "" || chunk.ReasoningContent != "" || len(chunk.ToolCalls) > 0) {
				ttftMs = time.Since(streamStart).Milliseconds()
			}
			auditContent.WriteString(chunk.Content)
			auditReasoning.WriteString(chunk.ReasoningContent)
			if len(chunk.ToolCalls) > 0 {
				auditToolCalls = append(auditToolCalls, chunk.ToolCalls...)
			}
			if chunk.FinishReason != "" {
				auditFinish = mapStreamFinishReason(chunk.FinishReason, len(chunk.ToolCalls) > 0)
			}

			// Usage-only chunks (no content, no tool calls, no finish reason)
			// are just captured above — nothing to send to the client.
			if chunk.Content == "" && chunk.ReasoningContent == "" && len(chunk.InlineImages) == 0 && len(chunk.ToolCalls) == 0 && chunk.FinishReason == "" {
				continue
			}

			cc := ChatCompletionChunk{
				ID:     chatID,
				Object: "chat.completion.chunk",
				Model:  fullModel,
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: ChunkDelta{
						Content:          buildDeltaContent(chunk.Content, chunk.InlineImages),
						ReasoningContent: buildReasoningContent(chunk.ReasoningContent),
					},
				}},
			}

			// Add tool calls to the delta if present
			if len(chunk.ToolCalls) > 0 {
				// Cache thought_signatures for later restoration.
				s.cacheThoughtSignatures(chunk.ToolCalls)

				for i, tc := range chunk.ToolCalls {
					idx := i
					argsJSON, _ := json.Marshal(tc.Arguments)
					cc.Choices[0].Delta.ToolCalls = append(cc.Choices[0].Delta.ToolCalls, OpenAIToolCall{
						Index:            &idx,
						ID:               tc.ID,
						Type:             "function",
						ThoughtSignature: tc.ThoughtSignature,
						Function: OpenAIFunctionCall{
							Name:      tc.Name,
							Arguments: string(argsJSON),
						},
					})
				}
			}

			// Match OpenAI's wire format: tool call data and finish_reason
			// are sent in separate SSE chunks. Many clients (e.g. OpenCode)
			// depend on this ordering — they accumulate tool call deltas and
			// only finalize when finish_reason arrives in a subsequent chunk.
			hasData := len(chunk.ToolCalls) > 0 || chunk.Content != "" || chunk.ReasoningContent != "" || len(chunk.InlineImages) > 0
			if chunk.FinishReason != "" && hasData {
				// Send the data chunk first (without finish_reason).
				writeSSEChunk(w, flusher, cc)
				// Then send a separate chunk with just the finish_reason
				// (normalized to OpenAI's vocabulary).
				fr := mapStreamFinishReason(chunk.FinishReason, len(chunk.ToolCalls) > 0)
				writeSSEChunk(w, flusher, ChatCompletionChunk{
					ID:     chatID,
					Object: "chat.completion.chunk",
					Model:  fullModel,
					Choices: []ChunkChoice{{
						Index:        0,
						Delta:        ChunkDelta{},
						FinishReason: &fr,
					}},
				})
			} else {
				if chunk.FinishReason != "" {
					fr := mapStreamFinishReason(chunk.FinishReason, len(chunk.ToolCalls) > 0)
					cc.Choices[0].FinishReason = &fr
				}
				writeSSEChunk(w, flusher, cc)
			}
		}

		// If the client requested usage reporting, emit a final chunk
		// with empty choices and the accumulated usage object.
		if includeUsage && streamUsage != nil {
			writeSSEChunk(w, flusher, ChatCompletionChunk{
				ID:      chatID,
				Object:  "chat.completion.chunk",
				Model:   fullModel,
				Choices: []ChunkChoice{},
				Usage:   chatCompletionUsagePtrFromService(*streamUsage),
			})
		}

		// Fire-and-forget usage recording for DB tokens (true streaming).
		if streamUsage != nil {
			s.recordUsageAsync(r.Context(), auth, fullModel, *streamUsage, time.Since(streamStart).Milliseconds(), "ok", "", "")
		}
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: audit.resolveSource(), endpoint: audit.endpoint,
			traceID: audit.traceID, sessionID: audit.sessionID, userField: audit.userField,
			requestBody: audit.requestBody, requestedModel: audit.requestedModel, fullModel: fullModel,
			responseBody: streamAuditResponseBody(chatID, fullModel, auditContent.String(), auditReasoning.String(), auditToolCalls, auditFinish, streamUsage),
			usage:        usageOrZero(streamUsage), latencyMs: time.Since(streamStart).Milliseconds(), ttftMs: ttftMs,
			streamed: true, status: "ok", finishReason: auditFinish,
		})
	} else {
		// Fallback: fake streaming via non-streaming Chat call. Same
		// retry semantics as the non-streaming path above — bounded
		// retry on 429/529.
		slog.Debug("fake streaming (provider doesn't support streaming)", "provider", providerKey, "model", actualModel)

		callStart := time.Now()
		resp, err := callWithGatewayRetry(r.Context(), providerKey, actualModel,
			retryAfterCap,
			func(ctx context.Context) (*service.LLMResponse, error) {
				return provider.Chat(ctx, actualModel, messages, tools, opts)
			})
		fakeLatencyMs := time.Since(callStart).Milliseconds()
		if err != nil {
			s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, fakeLatencyMs, "error", classifyHTTPError(err), err.Error())
			s.recordLLMCallAsync(r.Context(), llmAuditParams{
				auth: auth, source: audit.resolveSource(), endpoint: audit.endpoint,
				traceID: audit.traceID, sessionID: audit.sessionID, userField: audit.userField,
				requestBody: audit.requestBody, requestedModel: audit.requestedModel, fullModel: fullModel,
				latencyMs: fakeLatencyMs, streamed: true, status: "error",
				errCode: classifyHTTPError(err), errMsg: err.Error(),
			})
			slog.Error("provider chat failed", "provider", providerKey, "error", err)
			status, body := classifyGatewayError(err)
			addGatewayRateLimitHeaders(w, err)
			httpResponseJSON(w, body, status)
			return
		}

		// Chunk 1: role
		writeSSEChunk(w, flusher, ChatCompletionChunk{
			ID:     chatID,
			Object: "chat.completion.chunk",
			Model:  fullModel,
			Choices: []ChunkChoice{{
				Index: 0,
				Delta: ChunkDelta{Role: "assistant"},
			}},
		})

		// Chunk 2: reasoning content (if any)
		if resp.ReasoningContent != "" {
			writeSSEChunk(w, flusher, ChatCompletionChunk{
				ID:     chatID,
				Object: "chat.completion.chunk",
				Model:  fullModel,
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: ChunkDelta{ReasoningContent: resp.ReasoningContent},
				}},
			})
		}

		// Chunk 3: content (if any)
		deltaContent := buildDeltaContent(resp.Content, resp.InlineImages)
		if deltaContent != nil {
			writeSSEChunk(w, flusher, ChatCompletionChunk{
				ID:     chatID,
				Object: "chat.completion.chunk",
				Model:  fullModel,
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: ChunkDelta{Content: deltaContent},
				}},
			})
		}

		// Chunk 3: tool calls (if any)
		if len(resp.ToolCalls) > 0 {
			// Cache thought_signatures for later restoration.
			s.cacheThoughtSignatures(resp.ToolCalls)

			var toolCalls []OpenAIToolCall
			for i, tc := range resp.ToolCalls {
				idx := i
				argsJSON, _ := json.Marshal(tc.Arguments)
				toolCalls = append(toolCalls, OpenAIToolCall{
					Index:            &idx,
					ID:               tc.ID,
					Type:             "function",
					ThoughtSignature: tc.ThoughtSignature,
					Function: OpenAIFunctionCall{
						Name:      tc.Name,
						Arguments: string(argsJSON),
					},
				})
			}
			writeSSEChunk(w, flusher, ChatCompletionChunk{
				ID:     chatID,
				Object: "chat.completion.chunk",
				Model:  fullModel,
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: ChunkDelta{ToolCalls: toolCalls},
				}},
			})
		}

		// Final chunk: finish reason (normalized to OpenAI's vocabulary).
		finishReason := normalizeFinishReason(resp)
		writeSSEChunk(w, flusher, ChatCompletionChunk{
			ID:     chatID,
			Object: "chat.completion.chunk",
			Model:  fullModel,
			Choices: []ChunkChoice{{
				Index:        0,
				Delta:        ChunkDelta{},
				FinishReason: &finishReason,
			}},
		})

		// Emit usage chunk for fake streaming if requested.
		if includeUsage {
			writeSSEChunk(w, flusher, ChatCompletionChunk{
				ID:      chatID,
				Object:  "chat.completion.chunk",
				Model:   fullModel,
				Choices: []ChunkChoice{},
				Usage:   chatCompletionUsagePtrFromService(resp.Usage),
			})
		}

		// Fire-and-forget usage recording for DB tokens (fake streaming).
		s.recordUsageAsync(r.Context(), auth, fullModel, resp.Usage, fakeLatencyMs, "ok", "", "")
		if respBody, mErr := json.Marshal(buildOpenAIResponse(chatID, fullModel, resp)); mErr == nil {
			s.recordLLMCallAsync(r.Context(), llmAuditParams{
				auth: auth, source: audit.resolveSource(), endpoint: audit.endpoint,
				traceID: audit.traceID, sessionID: audit.sessionID, userField: audit.userField,
				requestBody: audit.requestBody, responseBody: respBody,
				requestedModel: audit.requestedModel, fullModel: fullModel,
				usage: resp.Usage, latencyMs: fakeLatencyMs, streamed: true, status: "ok",
				finishReason: normalizeFinishReason(resp),
			})
		}
	}

	// End the stream
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// buildDeltaContent constructs the delta content for an SSE chunk.
// When inline images are present, it returns an array of content parts
// (OpenAI multimodal format); otherwise it returns the text string as-is.
// Returns nil when there is no content to send.
func buildDeltaContent(text string, images []service.InlineImage) any {
	if len(images) == 0 {
		if text == "" {
			return nil
		}
		return text
	}

	// Build multimodal content array with text (if any) followed by image parts.
	var parts []map[string]any
	if text != "" {
		parts = append(parts, map[string]any{
			"type": "text",
			"text": text,
		})
	}
	for _, img := range images {
		parts = append(parts, map[string]any{
			"type": "image_url",
			"image_url": map[string]string{
				"url": "data:" + img.MimeType + ";base64," + img.Data,
			},
		})
	}
	return parts
}

// buildReasoningContent returns the reasoning text as an any value suitable
// for the ChunkDelta.ReasoningContent field. Returns nil when there is no
// reasoning content, which causes the field to be omitted from JSON output.
func buildReasoningContent(text string) any {
	if text == "" {
		return nil
	}
	return text
}

// writeSSEChunk writes a single SSE data line with the JSON-encoded chunk.
// It auto-stamps the Created timestamp when callers leave it unset so every
// chunk carries a unix-second value as OpenAI clients expect.
func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, chunk ChatCompletionChunk) {
	if chunk.Created == 0 {
		chunk.Created = time.Now().Unix()
	}
	data, _ := json.Marshal(chunk)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// writeSSEError writes an error as an SSE chunk with a finish reason,
// then terminates the stream.
func writeSSEError(w http.ResponseWriter, flusher http.Flusher, chatID, model, errMsg string) {
	finishReason := "stop"
	writeSSEChunk(w, flusher, ChatCompletionChunk{
		ID:     chatID,
		Object: "chat.completion.chunk",
		Model:  model,
		Choices: []ChunkChoice{{
			Index:        0,
			Delta:        ChunkDelta{Content: errMsg},
			FinishReason: &finishReason,
		}},
	})
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// ─── Token Usage Helpers ───

// checkTokenLimits checks whether a DB token has exceeded its token or spend budget.
// If the token has a configured limit_reset_interval, a lazy reset is performed
// when the reset period has elapsed. Returns an empty message when allowed.
// On error the caller should log and allow the request through (non-fatal).
func (s *Server) checkTokenLimits(ctx context.Context, auth *authResult) (string, error) {
	if auth == nil || auth.token == nil || auth.token.ID == "" {
		return "", nil // config token or unrestricted — no limit
	}

	token := auth.token
	hasTokenLimit := token.TotalTokenLimit.Valid && token.TotalTokenLimit.V > 0
	hasSpendLimit := token.SpendLimitCents.Valid && token.SpendLimitCents.V > 0
	if !hasTokenLimit && !hasSpendLimit {
		return "", nil // no limit configured
	}

	// Lazy periodic reset: if a reset interval is configured and enough time
	// has passed since the last reset, reset the counters now.
	if token.LimitResetInterval.Valid && token.LimitResetInterval.V != "" {
		if s.shouldResetUsage(token) && s.tokenUsageStore != nil {
			if err := s.tokenUsageStore.ResetTokenUsage(ctx, token.ID); err != nil {
				return "", fmt.Errorf("lazy reset usage: %w", err)
			}
			// After reset, both token_usage and spend windows start from now.
			return "", nil
		}
	}

	if hasTokenLimit && s.tokenUsageStore != nil {
		total, err := s.tokenUsageStore.GetTokenTotalUsage(ctx, token.ID)
		if err != nil {
			return "", fmt.Errorf("get total usage: %w", err)
		}
		if total >= token.TotalTokenLimit.V {
			return "token usage limit exceeded", nil
		}
	}

	if hasSpendLimit && s.costEventStore != nil {
		spend, err := s.costEventStore.GetCostByAgentSince(ctx, "gateway:"+token.ID, tokenBudgetWindowStart(token))
		if err != nil {
			return "", fmt.Errorf("get token spend: %w", err)
		}
		if spend >= token.SpendLimitCents.V {
			return "token spend limit exceeded", nil
		}
	}

	return "", nil
}

func tokenBudgetWindowStart(token *service.APIToken) string {
	if token == nil {
		return ""
	}
	anchor := token.CreatedAt.Time
	if token.LastResetAt.Valid {
		anchor = token.LastResetAt.V.Time
	}
	if anchor.IsZero() {
		return ""
	}
	return anchor.UTC().Format(time.RFC3339)
}

// shouldResetUsage determines whether a token's usage counters should be
// lazily reset based on its LimitResetInterval and LastResetAt.
func (s *Server) shouldResetUsage(token *service.APIToken) bool {
	if !token.LimitResetInterval.Valid {
		return false
	}

	period, err := str2duration.ParseDuration(token.LimitResetInterval.V)
	if err != nil {
		slog.Warn("invalid limit_reset_interval, skipping reset",
			"token_id", token.ID, "interval", token.LimitResetInterval.V, "error", err)
		return false
	}

	// If never reset, use token creation time as the anchor.
	anchor := token.CreatedAt.Time
	if token.LastResetAt.Valid {
		anchor = token.LastResetAt.V.Time
	}

	return time.Since(anchor) >= period
}

// recordUsageAsync fires a goroutine to record token usage.
// Failures are logged but do not affect the request.
//
// It writes to two places when possible:
//  1. token_usage — cumulative per-(token, model) counters (legacy; rate limiting)
//  2. cost_events — per-call rows with latency/status (usage dashboard)
//
// latencyMs, status, errCode, and errMsg are best-effort — callers pass zero /
// empty when they don't have the info (the summary response usually does).
func (s *Server) recordUsageAsync(ctx context.Context, auth *authResult, fullModel string, usage service.Usage, latencyMs int64, status, errCode, errMsg string) {
	if auth == nil || auth.token == nil || auth.token.ID == "" {
		return // config token or unrestricted — no tracking
	}
	tokenID := auth.token.ID

	// Default successful status if caller left it empty.
	if status == "" {
		status = "ok"
	}
	// Skip entirely if we have nothing to record.
	hasUsage := usage.TotalTokenCount() > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.CacheReadTokens > 0 || usage.CacheWriteTokens > 0
	if !hasUsage && status == "ok" {
		return
	}

	// 1. token_usage: cumulative counters.
	if s.tokenUsageStore != nil && hasUsage {
		go func() {
			if err := s.tokenUsageStore.RecordUsage(context.WithoutCancel(ctx), tokenID, fullModel, usage); err != nil {
				slog.Error("failed to record token usage", "token_id", tokenID, "model", fullModel, "error", err)
			}
		}()
	}

	// 2. cost_events: per-call row with latency/status for the usage dashboard.
	if s.costEventStore != nil {
		providerKey, actualModel := splitProviderModel(fullModel)

		var costCents float64
		if hasUsage {
			costCents = s.estimateGatewayUsageCostCents(context.WithoutCancel(ctx), providerKey, actualModel, fullModel, usage)
		}

		// There is no billing_code on APIToken today; use the token Name as a
		// human-readable attribution tag for gateway-originated calls.
		billingCode := ""
		if auth.token != nil && auth.token.Name != "" {
			billingCode = "token:" + auth.token.Name
		}

		go func() {
			if err := s.costEventStore.RecordCostEvent(context.WithoutCancel(ctx), service.CostEvent{
				AgentID:          "gateway:" + tokenID, // gateway calls have no agent; tag with token ID
				Provider:         providerKey,
				Model:            actualModel,
				BillingCode:      billingCode,
				InputTokens:      int64(usage.PromptTokens),
				OutputTokens:     int64(usage.CompletionTokens),
				CacheReadTokens:  int64(usage.CacheReadTokens),
				CacheWriteTokens: int64(usage.CacheWriteTokens),
				CostCents:        costCents,
				LatencyMs:        latencyMs,
				Status:           status,
				ErrorCode:        errCode,
				ErrorMessage:     errMsg,
			}); err != nil {
				slog.Error("failed to record cost event", "token_id", tokenID, "model", fullModel, "error", err)
			}
		}()
	}
}

// splitProviderModel splits a "provider/model" string. If no slash is present,
// returns ("", fullModel).
func splitProviderModel(fullModel string) (string, string) {
	if i := strings.Index(fullModel, "/"); i > 0 {
		return fullModel[:i], fullModel[i+1:]
	}
	return "", fullModel
}
