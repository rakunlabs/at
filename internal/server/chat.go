package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
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
	traceID, sessionID := auditTraceInfo(r, req.Metadata)

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
	info, err := s.chatProviderInfo(r, providerKey, actualModel)
	if err != nil {
		status, code := http.StatusBadGateway, "server_error"
		msg := fmt.Sprintf("provider resolution failed: %v", err)
		var unavailable *chatProviderUnavailableError
		switch {
		case errors.As(err, &unavailable):
			status, code = http.StatusNotFound, "model_not_found"
			msg = unavailable.message
		case errors.Is(err, service.ErrAccessDenied):
			status, code = http.StatusForbidden, "access_denied"
			msg = fmt.Sprintf("this workspace cannot use provider %q", providerKey)
		case errors.Is(err, service.ErrAccessResourceNotFound), errors.Is(err, service.ErrProviderDisabled):
			status, code = http.StatusNotFound, "model_not_found"
			msg = fmt.Sprintf("provider %q or model %q is not available in this workspace", providerKey, actualModel)
		}
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": msg,
				"type":    "invalid_request_error",
				"code":    code,
			},
		}, status)
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

// chatProviderInfo resolves the provider for the admin chat endpoint. An
// installation administrator keeps the global gateway registry, which is what
// the endpoint has always served. A scoped workspace member resolves through
// the workspace catalog instead, so the Playground cannot reach a provider or
// credential the member's workspace is not allowed to use.
func (s *Server) chatProviderInfo(r *http.Request, key, model string) (ProviderInfo, error) {
	if a, ok := service.AccessPrincipalFromContext(r.Context()); ok && !a.PlatformAdmin {
		return s.workspaceProviderInfo(r.Context(), key, model)
	}
	info, ok := s.getProviderInfo(key)
	if !ok {
		available := s.availableProviderKeys()
		return ProviderInfo{}, &chatProviderUnavailableError{message: s.providerUnavailableMessage(key, fmt.Sprintf("provider %q not found; available: %v", key, available))}
	}
	return info, nil
}

// chatProviderUnavailableError keeps the legacy registry's richer 404 message
// (provider availability) distinct from the workspace-catalog errors, which
// deliberately do not enumerate providers the caller may not list.
type chatProviderUnavailableError struct{ message string }

func (e *chatProviderUnavailableError) Error() string { return e.message }
