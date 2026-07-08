package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// AdminChatCompletions handles POST /api/v1/chat/completions.
// This is the admin-side chat endpoint used by the workflow editor's AI panel.
// Unlike the gateway endpoint, it does not require Bearer token auth — it is
// protected by ForwardAuth (if configured), same as all other admin routes.
func (s *Server) AdminChatCompletions(w http.ResponseWriter, r *http.Request) {
	// Parse request (same format as gateway). Capture the raw bytes first
	// so the LLM audit log can persist the exact request.
	rawBody, _ := io.ReadAll(r.Body)
	var req ChatCompletionRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("invalid request body: %v", err),
				"type":    "invalid_request_error",
			},
		}, http.StatusBadRequest)
		return
	}
	traceID, sessionID := auditTraceInfo(r)

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

	// Optional model validation
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

	slog.Debug("admin chat request",
		"provider", providerKey,
		"model", actualModel,
		"provider_type", providerType,
		"messages", len(req.Messages),
		"tools", len(req.Tools),
	)

	// Translate tools
	tools := translateOpenAITools(req.Tools)

	// Translate messages based on provider type. buildProviderMessages is
	// the single source of truth (it also covers bedrock, which needs the
	// Anthropic-style content-block shape so system prompts survive).
	messages, _ := s.buildProviderMessages(providerType, req.Messages, nil)

	// Build per-request generation options from the client request.
	opts := buildChatOptions(&req)

	if req.Stream {
		audit := streamAuditCtx{
			endpoint: r.URL.Path, source: "chat",
			traceID: traceID, sessionID: sessionID, userField: req.User,
			requestBody: rawBody, requestedModel: req.Model,
		}
		s.handleStreamingChat(w, r, nil, info.provider, info.RetryAfterCap(), providerKey, actualModel, req.Model, messages, tools, req.StreamOptions, opts, audit)
		return
	}

	// Non-streaming
	resp, err := provider.Chat(r.Context(), actualModel, messages, tools, opts)
	if err != nil {
		slog.Error("admin chat provider failed", "provider", providerKey, "error", err)
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider error: %v", err),
				"type":    "server_error",
			},
		}, http.StatusBadGateway)
		return
	}

	// Forward provider headers
	for k, v := range resp.Header {
		for _, val := range v {
			w.Header().Add(k, val)
		}
	}

	s.cacheThoughtSignatures(resp.ToolCalls)
	chatResp := buildOpenAIResponse(generateChatID(), req.Model, resp)
	httpResponseJSON(w, chatResp, http.StatusOK)
}
