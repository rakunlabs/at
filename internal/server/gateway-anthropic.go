package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/gateway/wire"

	"github.com/rakunlabs/at/internal/service"
)

// AnthropicMessages handles POST /gateway/v1/messages.
//
// It accepts the Anthropic Messages wire shape so a client configured with the
// gateway as its Anthropic endpoint — Claude Code, Cline, Roo, Kilo — reaches
// the gateway's routing, fallback, budgets and tracing without modification.
// Before this the only way in for those clients was the per-provider
// passthrough, which is pinned to one provider and does none of that.
func (s *Server) AnthropicMessages(w http.ResponseWriter, r *http.Request) {
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		writeAnthropicError(w, http.StatusUnauthorized, wire.AnthropicErrAuthentication, authErr)
		return
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, wire.AnthropicErrInvalidRequest, fmt.Sprintf("failed to read request body: %v", err))
		return
	}

	var req wire.AnthropicMessagesRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, wire.AnthropicErrInvalidRequest, fmt.Sprintf("invalid request body: %v", err))
		return
	}
	if err := wire.ValidateAnthropicRequest(&req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, wire.AnthropicErrInvalidRequest, err.Error())
		return
	}

	// A bare Anthropic model name is what these clients send, so routing
	// profiles are what make this endpoint usable without reconfiguring them.
	chain, routingProfile := s.chatCallChain(r.Context(), auth, req.Model, req.AtFallbacks)
	if routingProfile != "" {
		w.Header().Set("x-at-routing-profile", routingProfile)
	}
	chain = s.partitionCooledTargets(chain)

	if len(chain) == 0 {
		writeAnthropicError(w, http.StatusBadRequest, wire.AnthropicErrInvalidRequest, "model is required")
		return
	}
	if first := chain[0]; first.err != nil {
		status := anthropicModelErrorStatus(first.err)
		writeAnthropicError(w, status, wire.AnthropicErrorTypeForStatus(status), first.err.Error())

		return
	}

	var inputErr error
	if chain, inputErr = admitChainInputs(chain, wire.AnthropicInputModalities(req.Messages)); inputErr != nil {
		writeAnthropicError(w, http.StatusBadRequest, wire.AnthropicErrInvalidRequest, inputErr.Error())

		return
	}

	if limitMessage, resetErr := s.checkTokenLimits(r.Context(), auth); resetErr != nil {
		slog.Error("token limit check failed", "error", resetErr)
	} else if limitMessage != "" {
		writeAnthropicError(w, http.StatusTooManyRequests, wire.AnthropicErrRateLimit, limitMessage)

		return
	}

	traceID, sessionID := auditTraceInfo(r, req.Metadata)
	opts := wire.BuildAnthropicChatOptions(&req)

	callCtx, cancel := withRequestTimeout(r.Context(), req.TimeoutMs)
	defer cancel()

	audit := anthropicAuditContext{
		traceID:        traceID,
		sessionID:      sessionID,
		requestBody:    rawBody,
		requestedModel: req.Model,
		endpoint:       r.URL.Path,
	}

	if req.Stream {
		s.handleAnthropicStream(w, r.WithContext(callCtx), auth, &req, chain, opts, audit)

		return
	}

	s.handleAnthropicSync(w, r.WithContext(callCtx), auth, &req, chain, opts, audit)
}

// anthropicAuditContext carries the per-request recording inputs.
type anthropicAuditContext struct {
	traceID        string
	sessionID      string
	requestBody    []byte
	requestedModel string
	endpoint       string
}

// anthropicModelErrorStatus maps a chain-resolution failure to a status, using
// the same discrimination as the OpenAI-shape endpoint: administratively
// unavailable is a deterministic 404 rather than a retry-inviting 5xx, and a
// denial is 403.
func anthropicModelErrorStatus(err error) int {
	var disabled providerDisabledError
	switch {
	case errors.As(err, &disabled):
		return http.StatusNotFound
	case strings.Contains(err.Error(), "not have access"), strings.Contains(err.Error(), "no usable target"):
		return http.StatusForbidden
	case strings.Contains(err.Error(), "not found"), strings.Contains(err.Error(), "not available"):
		return http.StatusNotFound
	}

	return http.StatusBadRequest
}

// handleAnthropicSync runs the fallback chain and encodes an Anthropic response.
func (s *Server) handleAnthropicSync(
	w http.ResponseWriter,
	r *http.Request,
	auth *authResult,
	req *wire.AnthropicMessagesRequest,
	chain []chatCallTarget,
	baseOpts *service.ChatOptions,
	audit anthropicAuditContext,
) {
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

		// Translated per attempt from the original request, so a chain spanning
		// provider families never derives one attempt from another's form.
		messages, tools := s.buildAnthropicProviderMessages(target.info.providerType, req)
		opts := cloneChatOptions(baseOpts)

		callStart := time.Now()
		r2, err := callWithGatewayRetry(r.Context(), target.providerKey, target.actualModel,
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
		s.noteProviderError(target.providerKey, err)
		slog.Warn("anthropic messages: provider call failed",
			"attempt", i, "provider", target.providerKey, "model", target.actualModel, "error", err)
		s.recordUsageAsync(r.Context(), auth, target.fullModel, service.Usage{}, totalLatency, "error", classifyHTTPError(err), err.Error())
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "responses", endpoint: audit.endpoint,
			traceID: audit.traceID, sessionID: audit.sessionID,
			requestBody: audit.requestBody, requestedModel: audit.requestedModel, fullModel: target.fullModel,
			latencyMs: totalLatency, status: "error",
			errCode: classifyHTTPError(err), errMsg: err.Error(),
		})

		if r.Context().Err() != nil || !shouldFallback(err) {
			break
		}
	}

	if resp == nil {
		status, _ := classifyGatewayError(lastErr)
		addGatewayRateLimitHeaders(w, lastErr)
		writeAnthropicError(w, status, wire.AnthropicErrorTypeForStatus(status), anthropicErrorMessage(lastErr))

		return
	}

	s.noteProviderResponse(used.providerKey, resp.Header)
	forwardProviderResponseHeaders(w.Header(), resp.Header)
	if used.fullModel != req.Model {
		w.Header().Set("x-at-model-used", used.fullModel)
	}

	s.cacheThoughtSignatures(resp.ToolCalls)
	body := wire.BuildAnthropicResponse(wire.AnthropicMessageID(), used.actualModel, resp, req.StopSequences)

	s.recordUsageAsync(r.Context(), auth, used.fullModel, resp.Usage, totalLatency, "ok", "", "")
	if respBody, err := json.Marshal(body); err == nil {
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "responses", endpoint: audit.endpoint,
			traceID: audit.traceID, sessionID: audit.sessionID,
			requestBody: audit.requestBody, responseBody: respBody,
			requestedModel: audit.requestedModel, fullModel: used.fullModel,
			usage: resp.Usage, latencyMs: totalLatency, status: "ok",
			finishReason: body.StopReason,
		})
	}

	httpResponseJSON(w, body, http.StatusOK)
}

// anthropicErrorMessage extracts a client-facing message from an upstream error.
func anthropicErrorMessage(err error) string {
	if err == nil {
		return "upstream request failed"
	}

	return err.Error()
}
