package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ChatCompletions handles POST /v1/chat/completions.
// It accepts an OpenAI-compatible request, routes it to the correct backend
// provider based on the model prefix (e.g., "anthropic/claude-haiku-4-5"),
// and returns an OpenAI-compatible response.
func (s *Server) ChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Auth check
	if !s.checkAuth(r) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "invalid or missing Authorization header",
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

	slog.Info("gateway request",
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
		// OpenAI-compatible providers (openai, vertex): pass through
		messages = translateOpenAIMessages(req.Messages)
	}

	if req.Stream {
		s.handleStreamingChat(w, r, info.provider, providerKey, actualModel, req.Model, messages, tools)
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
	chatResp := buildOpenAIResponse(generateChatID(), req.Model, resp)
	httpResponseJSON(w, chatResp, http.StatusOK)
}

// ListModels handles GET /v1/models.
// It returns all configured provider/model combinations in OpenAI format.
// If a provider has a models list, each model is advertised. Otherwise,
// only the default model is shown.
func (s *Server) ListModels(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "invalid or missing Authorization header",
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
				models = append(models, ModelData{
					ID:      key + "/" + m,
					Object:  "model",
					OwnedBy: key,
				})
			}
		} else {
			models = append(models, ModelData{
				ID:      key + "/" + info.defaultModel,
				Object:  "model",
				OwnedBy: key,
			})
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

// checkAuth validates the Authorization header if auth is configured.
// Returns true if auth passes (or no auth is configured).
func (s *Server) checkAuth(r *http.Request) bool {
	if s.authToken == "" {
		return true
	}

	auth := r.Header.Get("Authorization")
	return auth == "Bearer "+s.authToken
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

	// Try true streaming if the provider supports it.
	if sp, ok := provider.(service.LLMStreamProvider); ok {
		slog.Info("streaming via provider", "provider", providerKey, "model", actualModel)

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

		for chunk := range chunks {
			if chunk.Error != nil {
				slog.Error("stream chunk error", "provider", providerKey, "error", chunk.Error)
				writeSSEError(w, flusher, chatID, fullModel, fmt.Sprintf("stream error: %v", chunk.Error))
				return
			}

			cc := ChatCompletionChunk{
				ID:     chatID,
				Object: "chat.completion.chunk",
				Model:  fullModel,
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: ChunkDelta{Content: chunk.Content},
				}},
			}

			// Add tool calls to the delta if present
			if len(chunk.ToolCalls) > 0 {
				for _, tc := range chunk.ToolCalls {
					argsJSON, _ := json.Marshal(tc.Arguments)
					cc.Choices[0].Delta.ToolCalls = append(cc.Choices[0].Delta.ToolCalls, OpenAIToolCall{
						ID:   tc.ID,
						Type: "function",
						Function: OpenAIFunctionCall{
							Name:      tc.Name,
							Arguments: string(argsJSON),
						},
					})
				}
			}

			if chunk.FinishReason != "" {
				fr := chunk.FinishReason
				cc.Choices[0].FinishReason = &fr
			}

			writeSSEChunk(w, flusher, cc)
		}
	} else {
		// Fallback: fake streaming via non-streaming Chat call.
		slog.Info("fake streaming (provider doesn't support streaming)", "provider", providerKey, "model", actualModel)

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
		if resp.Content != "" {
			writeSSEChunk(w, flusher, ChatCompletionChunk{
				ID:     chatID,
				Object: "chat.completion.chunk",
				Model:  fullModel,
				Choices: []ChunkChoice{{
					Index: 0,
					Delta: ChunkDelta{Content: resp.Content},
				}},
			})
		}

		// Chunk 3: tool calls (if any)
		if len(resp.ToolCalls) > 0 {
			var toolCalls []OpenAIToolCall
			for _, tc := range resp.ToolCalls {
				argsJSON, _ := json.Marshal(tc.Arguments)
				toolCalls = append(toolCalls, OpenAIToolCall{
					ID:   tc.ID,
					Type: "function",
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
	}

	// End the stream
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
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
