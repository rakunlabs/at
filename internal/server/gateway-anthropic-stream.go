package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// handleAnthropicStream serves a streaming Anthropic Messages request.
//
// Like the OpenAI-shape streaming path, nothing is written to the client until
// the upstream stream opens: w.Header().Set only populates the header map, so an
// open failure can still answer with a real status code rather than a half
// stream. Once the first event is written the response is committed and any
// later failure can only be reported as an SSE error event.
func (s *Server) handleAnthropicStream(
	w http.ResponseWriter,
	r *http.Request,
	auth *authResult,
	req *anthropicMessagesRequest,
	chain []chatCallTarget,
	baseOpts *service.ChatOptions,
	audit anthropicAuditContext,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAnthropicError(w, http.StatusInternalServerError, anthropicErrAPI, "streaming not supported by this server")

		return
	}

	// Falls back across the chain, bounded by the commitment boundary: no event
	// is written until the upstream stream opens, so an open failure can still
	// be served by the next target invisibly.
	var streamErr error
	for i, target := range chain {
		if target.err != nil {
			continue
		}

		sp, streamable := target.info.provider.(service.LLMStreamProvider)
		if !streamable {
			// A provider that cannot stream still answers the request; a single
			// non-streaming call is what a client asking for SSE can consume.
			s.handleAnthropicSync(w, r, auth, req, chain[i:], baseOpts, audit)

			return
		}

		messages, tools := s.buildAnthropicProviderMessages(target.info.providerType, req)
		opts := cloneChatOptions(baseOpts)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		if target.fullModel != req.Model {
			w.Header().Set("x-at-model-used", target.fullModel)
		}

		streamStart := time.Now()
		type opened struct {
			chunks  <-chan service.StreamChunk
			headers http.Header
		}

		result, err := callWithGatewayRetry(r.Context(), target.providerKey, target.actualModel,
			target.info.RetryAfterCap(),
			func(ctx context.Context) (opened, error) {
				ch, h, e := sp.ChatStream(ctx, target.actualModel, messages, tools, opts)

				return opened{chunks: ch, headers: h}, e
			})
		if err != nil {
			s.noteProviderError(target.providerKey, err)
			s.recordUsageAsync(r.Context(), auth, target.fullModel, service.Usage{},
				time.Since(streamStart).Milliseconds(), "error", classifyHTTPError(err), err.Error())
			s.recordLLMCallAsync(r.Context(), llmAuditParams{
				auth: auth, source: "responses", endpoint: audit.endpoint,
				traceID: audit.traceID, sessionID: audit.sessionID,
				requestBody: audit.requestBody, requestedModel: audit.requestedModel, fullModel: target.fullModel,
				latencyMs: time.Since(streamStart).Milliseconds(), streamed: true, status: "error",
				errCode: classifyHTTPError(err), errMsg: err.Error(),
			})
			slog.Error("anthropic messages: provider stream failed", "provider", target.providerKey, "error", err)

			streamErr = err
			if r.Context().Err() != nil || !shouldFallback(err) || i == len(chain)-1 {
				break
			}
			clearStreamHeaders(w)

			continue
		}

		s.noteProviderResponse(target.providerKey, result.headers)

		writer := newAnthropicStreamWriter(w, flusher, anthropicMessageID(), target.actualModel)
		s.pumpAnthropicStream(r.Context(), writer, result.chunks, req, streamCtx{
			auth:        auth,
			target:      target,
			audit:       audit,
			streamStart: streamStart,
		})

		return
	}

	// Nothing has been written, so a real status code is still possible.
	status, _ := classifyGatewayError(streamErr)
	addGatewayRateLimitHeaders(w, streamErr)
	writeAnthropicError(w, status, anthropicErrorTypeForStatus(status), anthropicErrorMessage(streamErr))
}

// streamCtx bundles the recording inputs for the pump.
type streamCtx struct {
	auth        *authResult
	target      chatCallTarget
	audit       anthropicAuditContext
	streamStart time.Time
}

// pumpAnthropicStream relays provider chunks as Anthropic SSE events.
func (s *Server) pumpAnthropicStream(
	ctx context.Context,
	writer *anthropicStreamWriter,
	chunks <-chan service.StreamChunk,
	req *anthropicMessagesRequest,
	sc streamCtx,
) {
	var (
		usage        *service.Usage
		finishReason string
		content      strings.Builder
		toolCalls    []service.ToolCall
		started      bool
		ttftMs       int64
	)

	ensureStarted := func() {
		if !started {
			started = true
			writer.MessageStart(usage)
		}
	}

	for {
		select {
		case <-ctx.Done():
			// The client is gone; closing without a message_stop is correct,
			// and the upstream context cancellation closes the provider side.
			s.recordAnthropicStreamEnd(ctx, sc, req, usage, finishReason, content.String(), toolCalls, ttftMs, ctx.Err())

			return

		case chunk, open := <-chunks:
			if !open {
				ensureStarted()
				stopReason, matched := anthropicStopReason(&service.LLMResponse{
					Content:      content.String(),
					ToolCalls:    toolCalls,
					FinishReason: finishReason,
				}, req.StopSequences)
				writer.Finish(stopReason, matched, usage)
				s.recordAnthropicStreamEnd(ctx, sc, req, usage, stopReason, content.String(), toolCalls, ttftMs, nil)

				return
			}

			if chunk.Error != nil {
				ensureStarted()
				slog.Error("anthropic messages: stream chunk error", "provider", sc.target.providerKey, "error", chunk.Error)
				writer.Error(anthropicErrorTypeForStatus(gatewayStatusForError(chunk.Error)), chunk.Error.Error())
				s.recordAnthropicStreamEnd(ctx, sc, req, usage, finishReason, content.String(), toolCalls, ttftMs, chunk.Error)

				return
			}

			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			ensureStarted()

			if chunk.ReasoningContent != "" {
				if ttftMs == 0 {
					ttftMs = time.Since(sc.streamStart).Milliseconds()
				}
				writer.ThinkingDelta(chunk.ReasoningContent)
			}
			if chunk.Content != "" {
				if ttftMs == 0 {
					ttftMs = time.Since(sc.streamStart).Milliseconds()
				}
				content.WriteString(chunk.Content)
				writer.TextDelta(chunk.Content)
			}
			for _, tc := range chunk.ToolCalls {
				toolCalls = append(toolCalls, tc)
				writer.ToolCall(tc)
			}
			if chunk.FinishReason != "" {
				finishReason = chunk.FinishReason
			}
		}
	}
}

// recordAnthropicStreamEnd writes the cost event and observation for a finished
// or aborted stream.
func (s *Server) recordAnthropicStreamEnd(
	ctx context.Context,
	sc streamCtx,
	req *anthropicMessagesRequest,
	usage *service.Usage,
	stopReason, content string,
	toolCalls []service.ToolCall,
	ttftMs int64,
	streamErr error,
) {
	var u service.Usage
	if usage != nil {
		u = *usage
	}

	latency := time.Since(sc.streamStart).Milliseconds()
	status := "ok"
	errCode, errMsg := "", ""
	if streamErr != nil {
		status = "error"
		errCode = classifyHTTPError(streamErr)
		errMsg = streamErr.Error()
	}

	s.recordUsageAsync(ctx, sc.auth, sc.target.fullModel, u, latency, status, errCode, errMsg)

	responseBody := content
	if len(toolCalls) > 0 {
		responseBody += "\n[tool_calls: " + strings.Join(toolCallNames(toolCalls), ", ") + "]"
	}

	s.recordLLMCallAsync(ctx, llmAuditParams{
		auth: sc.auth, source: "responses", endpoint: sc.audit.endpoint,
		traceID: sc.audit.traceID, sessionID: sc.audit.sessionID,
		requestBody: sc.audit.requestBody, responseBody: []byte(responseBody),
		requestedModel: sc.audit.requestedModel, fullModel: sc.target.fullModel,
		usage: u, latencyMs: latency, ttftMs: ttftMs, streamed: true,
		status: status, errCode: errCode, errMsg: errMsg, finishReason: stopReason,
	})

	s.cacheThoughtSignatures(toolCalls)
}

func toolCallNames(calls []service.ToolCall) []string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}

	return names
}

// gatewayStatusForError returns just the status half of classifyGatewayError.
func gatewayStatusForError(err error) int {
	status, _ := classifyGatewayError(err)

	return status
}
