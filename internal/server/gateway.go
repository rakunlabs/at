package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

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

	hasProviderRestrictions := len(a.token.AllowedProviders) > 0
	hasModelRestrictions := len(a.token.AllowedModels) > 0

	if !hasProviderRestrictions && !hasModelRestrictions {
		return true // no restrictions
	}

	// Check provider-level access (OR logic with model-level).
	if hasProviderRestrictions {
		for _, p := range a.token.AllowedProviders {
			if p == providerKey {
				return true
			}
		}
	}

	// Check model-level access.
	if hasModelRestrictions {
		for _, m := range a.token.AllowedModels {
			if m == fullModelID {
				return true
			}
		}
	}

	return false
}

// ChatCompletions handles POST /gateway/v1/chat/completions.
// It accepts an OpenAI-compatible request, routes it to the correct backend
// provider based on the model prefix (e.g., "anthropic/claude-haiku-4-5"),
// and returns an OpenAI-compatible response.
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

	// Parse request
	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("invalid request body: %v", err),
				"type":    "invalid_request_error",
			},
		}, http.StatusBadRequest)
		return
	}

	// Parse model: "provider_key/actual_model"
	providerKey, actualModel, err := parseModelID(req.Model)
	if err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"code":    "model_not_found",
			},
		}, http.StatusBadRequest)
		return
	}

	// Token-level access check.
	if !auth.isModelAllowed(providerKey, req.Model) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("token does not have access to model %q", req.Model),
				"type":    "invalid_request_error",
				"code":    "model_not_found",
			},
		}, http.StatusForbidden)
		return
	}

	// Look up provider
	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		available := s.availableProviderKeys()
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q not found; available: %v", providerKey, available),
				"type":    "invalid_request_error",
				"code":    "model_not_found",
			},
		}, http.StatusNotFound)
		return
	}

	// Strict model validation: if the provider has a models list,
	// reject requests for models not in the list.
	if len(info.models) > 0 && !info.hasModel(actualModel) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("model %q is not available for provider %q; available models: %v", actualModel, providerKey, info.models),
				"type":    "invalid_request_error",
				"code":    "model_not_found",
			},
		}, http.StatusNotFound)
		return
	}

	provider := info.provider
	providerType := info.providerType

	slog.Debug("gateway request",
		"provider", providerKey,
		"model", actualModel,
		"provider_type", providerType,
		"messages", len(req.Messages),
	)

	// Translate tools
	tools := translateOpenAITools(req.Tools)

	// Translate messages based on provider type
	var messages []service.Message

	switch providerType {
	case "anthropic":
		// Translate OpenAI format → Anthropic format.
		// System prompt is extracted separately because Anthropic uses
		// a top-level "system" parameter instead of a system message.
		// We pass it as a role="system" service.Message; the Anthropic
		// provider extracts it when building the request body.
		var systemPrompt string
		systemPrompt, messages = translateOpenAIToAnthropic(req.Messages)
		if systemPrompt != "" {
			// Prepend system message — the Anthropic provider will extract it.
			messages = append([]service.Message{{Role: "system", Content: systemPrompt}}, messages...)
		}
	default:
		// OpenAI-compatible providers (openai, vertex) and gemini: pass through.
		// The gemini provider handles its own format translation internally.
		messages = translateOpenAIMessages(req.Messages, s.lookupThoughtSignature)
	}

	if req.Stream {
		s.handleStreamingChat(w, r, info.provider, providerKey, actualModel, req.Model, messages, tools, req.StreamOptions)
		return
	}

	// Call the provider (non-streaming)
	resp, err := provider.Chat(r.Context(), actualModel, messages, tools)
	if err != nil {
		slog.Error("provider chat failed", "provider", providerKey, "error", err)
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider error: %v", err),
				"type":    "server_error",
			},
		}, http.StatusBadGateway)
		return
	}

	// Build OpenAI-compatible response
	// Cache thought_signatures before sending the response to the client.
	s.cacheThoughtSignatures(resp.ToolCalls)
	chatResp := buildOpenAIResponse(generateChatID(), req.Model, resp)
	httpResponseJSON(w, chatResp, http.StatusOK)
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
		if len(info.models) > 0 {
			for _, m := range info.models {
				fullID := key + "/" + m
				if auth.isModelAllowed(key, fullID) {
					models = append(models, ModelData{
						ID:      fullID,
						Object:  "model",
						OwnedBy: key,
					})
				}
			}
		} else {
			fullID := key + "/" + info.defaultModel
			if auth.isModelAllowed(key, fullID) {
				models = append(models, ModelData{
					ID:      fullID,
					Object:  "model",
					OwnedBy: key,
				})
			}
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

// authenticateRequest validates the Authorization header.
// Returns an authResult on success, or an error message string on failure.
// When no auth is configured at all (no config tokens and no token store),
// all requests are rejected — at least one token must be configured.
func (s *Server) authenticateRequest(r *http.Request) (*authResult, string) {
	auth := r.Header.Get("Authorization")
	bearerToken := strings.TrimPrefix(auth, "Bearer ")

	// If no auth is configured at all, reject everything.
	if len(s.authTokens) == 0 && s.tokenStore == nil {
		return nil, "no authentication configured; add a token via config or UI"
	}

	if auth == "" || bearerToken == "" {
		return nil, "missing Authorization header"
	}

	// 1. Check config auth tokens.
	for _, cfgToken := range s.authTokens {
		if cfgToken.Token == "" || bearerToken != cfgToken.Token {
			continue
		}

		// Token matched — check expiry if set.
		if cfgToken.ExpiresAt != "" {
			expiresAt, err := time.Parse(time.RFC3339, cfgToken.ExpiresAt)
			if err != nil {
				slog.Error("invalid expires_at in config auth token, rejecting", "name", cfgToken.Name, "error", err)
				return nil, "config token has invalid expires_at"
			}

			if expiresAt.Before(time.Now().UTC()) {
				return nil, "token has expired"
			}
		}

		// If no scoping is configured, return unrestricted access.
		if len(cfgToken.AllowedProviders) == 0 && len(cfgToken.AllowedModels) == 0 {
			return &authResult{}, ""
		}

		// Build a synthetic APIToken for scope checking.
		syntheticToken := &service.APIToken{
			Name: cfgToken.Name,
		}
		if len(cfgToken.AllowedProviders) > 0 {
			syntheticToken.AllowedProviders = cfgToken.AllowedProviders
		}
		if len(cfgToken.AllowedModels) > 0 {
			syntheticToken.AllowedModels = cfgToken.AllowedModels
		}

		return &authResult{token: syntheticToken}, ""
	}

	// 2. Check DB token.
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

			return &authResult{token: token}, ""
		}
	}

	return nil, "invalid or missing Authorization header"
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
	provider service.LLMProvider,
	providerKey, actualModel, fullModel string,
	messages []service.Message,
	tools []service.Tool,
	streamOpts *StreamOptions,
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

		chunks, err := sp.ChatStream(r.Context(), actualModel, messages, tools)
		if err != nil {
			// Can't send JSON error after SSE headers are set in some cases,
			// but we haven't written anything yet, so we can still respond.
			slog.Error("provider stream failed", "provider", providerKey, "error", err)
			writeSSEError(w, flusher, chatID, fullModel, fmt.Sprintf("provider error: %v", err))
			return
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
		var streamUsage *ChatCompletionUsage

		for chunk := range chunks {
			if chunk.Error != nil {
				slog.Error("stream chunk error", "provider", providerKey, "error", chunk.Error)
				writeSSEError(w, flusher, chatID, fullModel, fmt.Sprintf("stream error: %v", chunk.Error))
				return
			}

			// Capture usage if present (don't emit it yet).
			if chunk.Usage != nil {
				streamUsage = &ChatCompletionUsage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
				}
			}

			// Usage-only chunks (no content, no tool calls, no finish reason)
			// are just captured above — nothing to send to the client.
			if chunk.Content == "" && len(chunk.InlineImages) == 0 && len(chunk.ToolCalls) == 0 && chunk.FinishReason == "" {
				continue
			}

			cc := ChatCompletionChunk{
				ID:     chatID,
				Object: "chat.completion.chunk",
				Model:  fullModel,
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: ChunkDelta{Content: buildDeltaContent(chunk.Content, chunk.InlineImages)},
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
			hasData := len(chunk.ToolCalls) > 0 || chunk.Content != "" || len(chunk.InlineImages) > 0
			if chunk.FinishReason != "" && hasData {
				// Send the data chunk first (without finish_reason).
				writeSSEChunk(w, flusher, cc)
				// Then send a separate chunk with just the finish_reason.
				fr := chunk.FinishReason
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
					fr := chunk.FinishReason
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
				Usage:   streamUsage,
			})
		}
	} else {
		// Fallback: fake streaming via non-streaming Chat call.
		slog.Debug("fake streaming (provider doesn't support streaming)", "provider", providerKey, "model", actualModel)

		resp, err := provider.Chat(r.Context(), actualModel, messages, tools)
		if err != nil {
			slog.Error("provider chat failed", "provider", providerKey, "error", err)
			writeSSEError(w, flusher, chatID, fullModel, fmt.Sprintf("provider error: %v", err))
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

		// Chunk 2: content (if any)
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

		// Final chunk: finish reason
		finishReason := "stop"
		if !resp.Finished {
			finishReason = "tool_calls"
		}
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
				Usage: &ChatCompletionUsage{
					PromptTokens:     resp.Usage.PromptTokens,
					CompletionTokens: resp.Usage.CompletionTokens,
					TotalTokens:      resp.Usage.TotalTokens,
				},
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

// writeSSEChunk writes a single SSE data line with the JSON-encoded chunk.
func writeSSEChunk(w http.ResponseWriter, flusher http.Flusher, chunk ChatCompletionChunk) {
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
