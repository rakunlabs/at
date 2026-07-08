package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// auditTraceInfo extracts the client-supplied trace and session correlation
// IDs from request headers. Both are optional: an empty trace ID causes the
// recorder to mint a fresh ULID, grouping only the fallback attempts of this
// one request. Clients that want to stitch a multi-turn conversation set
// x-at-session-id (or the standard OpenAI-ecosystem headers) to a stable value.
func auditTraceInfo(r *http.Request) (traceID, sessionID string) {
	traceID = firstNonEmpty(
		r.Header.Get("x-at-trace-id"),
		r.Header.Get("x-trace-id"),
	)
	sessionID = firstNonEmpty(
		r.Header.Get("x-at-session-id"),
		r.Header.Get("x-session-id"),
	)
	return traceID, sessionID
}

// llmAuditDumpDir is the sub-directory (under the loop-governor workspace
// root) where full request/response payloads are spilled when they exceed
// the inline cap. Swept by the workspace janitor under LLMCallRetention.
const llmAuditDumpDir = ".at-llm-audit"

// llmAuditSpanBodyCap bounds how many bytes of the request/response body we
// attach to an OTEL span (Langfuse input/output). Spans are not the system
// of record — the DB row is — so we keep the span attribute small.
const llmAuditSpanBodyCap = 16384

// llmAuditCache caches the llm_audit feature toggle so the per-call hot path
// doesn't hit the DB on every request. Refreshed at most once per TTL.
type llmAuditCache struct {
	enabled   atomic.Bool
	checkedAt atomic.Int64 // unix nanos of last DB refresh
	inited    atomic.Bool
}

const llmAuditCacheTTL = 30 * time.Second

// llmAuditParams carries everything needed to record one LLM call. Callers
// fill in what they have; empty fields are fine.
type llmAuditParams struct {
	auth      *authResult
	source    string // "gateway" | "gateway_stream" | "responses" | ...
	endpoint  string
	traceID   string
	sessionID string
	userField string

	requestBody  []byte
	responseBody []byte

	requestedModel string // full "provider/model" the client asked for
	fullModel      string // full "provider/model" that served the call

	usage        service.Usage
	costCents    float64 // when 0 and usage>0, recomputed from pricing
	latencyMs    int64
	ttftMs       int64
	streamed     bool
	status       string
	errCode      string
	errMsg       string
	finishReason string

	// attribution overrides (agent/workflow callers set these; gateway
	// leaves them empty and attribution comes from the token).
	agentID string
	taskID  string
	runID   string
	orgID   string
}

// llmAuditEnabled reports whether the LLM call audit feature is on, using a
// short-lived cache to avoid a DB read per gateway call.
func (s *Server) llmAuditEnabled(ctx context.Context) bool {
	if s.llmCallStore == nil {
		return false
	}
	now := time.Now().UnixNano()
	if s.llmAudit.inited.Load() && now-s.llmAudit.checkedAt.Load() < int64(llmAuditCacheTTL) {
		return s.llmAudit.enabled.Load()
	}
	enabled, err := s.isFeatureEnabled(ctx, service.FeatureLLMAudit)
	if err != nil {
		// On error, fall back to the last known state (or false if never
		// checked). Don't spam logs — this runs off the hot path anyway.
		if s.llmAudit.inited.Load() {
			return s.llmAudit.enabled.Load()
		}
		return false
	}
	s.llmAudit.enabled.Store(enabled)
	s.llmAudit.checkedAt.Store(now)
	s.llmAudit.inited.Store(true)
	return enabled
}

// recordLLMCallAsync persists one LLM call (full request/response bodies)
// and emits an OTEL gen-ai span. Fire-and-forget: failures are logged, never
// surfaced to the caller. No-op when the feature is disabled or no store is
// wired.
func (s *Server) recordLLMCallAsync(ctx context.Context, p llmAuditParams) {
	if !s.llmAuditEnabled(ctx) {
		return
	}

	// Snapshot everything we need off the request goroutine.
	call := s.buildLLMCall(ctx, p)

	go func() {
		bg := context.WithoutCancel(ctx)
		s.spillLLMCallBodies(&call, p.requestBody, p.responseBody)
		if err := s.llmCallStore.RecordLLMCall(bg, call); err != nil {
			slog.Error("failed to record llm call", "trace_id", call.TraceID, "model", call.Model, "error", err.Error())
		}
		s.emitLLMSpan(call, p.requestBody, p.responseBody)
	}()
}

// buildLLMCall assembles the service.LLMCall record (minus body spill, which
// happens in the goroutine because it touches disk).
func (s *Server) buildLLMCall(ctx context.Context, p llmAuditParams) service.LLMCall {
	provider, model := splitProviderModel(p.fullModel)

	status := p.status
	if status == "" {
		status = "ok"
	}

	tokenID := ""
	if p.auth != nil && p.auth.token != nil {
		tokenID = p.auth.token.ID
	}

	costCents := p.costCents
	if costCents == 0 && (p.usage.PromptTokens > 0 || p.usage.CompletionTokens > 0) {
		costCents = s.estimateGatewayUsageCostCents(context.WithoutCancel(ctx), provider, model, p.fullModel, p.usage)
	}

	traceID := p.traceID
	if traceID == "" {
		traceID = ulid.Make().String()
	}

	return service.LLMCall{
		ID:                 ulid.Make().String(),
		TraceID:            traceID,
		SessionID:          p.sessionID,
		Source:             p.source,
		Endpoint:           p.endpoint,
		TokenID:            tokenID,
		AgentID:            p.agentID,
		TaskID:             p.taskID,
		RunID:              p.runID,
		OrganizationID:     p.orgID,
		Provider:           provider,
		Model:              model,
		RequestedModel:     p.requestedModel,
		RequestBytes:       int64(len(p.requestBody)),
		ResponseBytes:      int64(len(p.responseBody)),
		Streamed:           p.streamed,
		InputTokens:        int64(p.usage.PromptTokens),
		OutputTokens:       int64(p.usage.CompletionTokens),
		CacheReadTokens:    int64(p.usage.CacheReadTokens),
		CacheWriteTokens:   int64(p.usage.CacheWriteTokens),
		ReasoningTokens:    int64(p.usage.ReasoningTokens),
		CostCents:          costCents,
		LatencyMs:          p.latencyMs,
		TimeToFirstTokenMs: p.ttftMs,
		Status:             status,
		ErrorCode:          p.errCode,
		ErrorMessage:       p.errMsg,
		FinishReason:       p.finishReason,
		UserField:          p.userField,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
	}
}

// spillLLMCallBodies clips oversized bodies inline and writes the full
// payload to a spill file, recording its path on the call. Best-effort:
// a spill failure just means we keep the truncated body with no ref.
func (s *Server) spillLLMCallBodies(call *service.LLMCall, reqBody, respBody []byte) {
	call.RequestBody, call.RequestTruncated, call.RequestRef = s.clipOrSpill(call.ID, "request", reqBody)
	call.ResponseBody, call.ResponseTruncated, call.ResponseRef = s.clipOrSpill(call.ID, "response", respBody)
}

// clipOrSpill returns (inlineBody, truncated, spillRef). When body fits under
// LLMCallBodyMaxBytes it is returned verbatim. Otherwise the full body is
// written to <workspace>/.at-llm-audit/<yyyy-mm-dd>/<id>-<side>.json and the
// inline copy is truncated.
func (s *Server) clipOrSpill(id, side string, body []byte) (string, bool, string) {
	if len(body) <= service.LLMCallBodyMaxBytes {
		return string(body), false, ""
	}

	ref := ""
	root := s.llmAuditRoot()
	if root != "" {
		day := time.Now().UTC().Format("2006-01-02")
		dir := filepath.Join(root, day)
		if err := os.MkdirAll(dir, 0o755); err == nil {
			path := filepath.Join(dir, fmt.Sprintf("%s-%s.json", id, side))
			if err := os.WriteFile(path, body, 0o644); err == nil {
				ref = path
			} else {
				slog.Debug("llm audit: spill write failed", "path", path, "error", err.Error())
			}
		}
	}

	return string(body[:service.LLMCallBodyMaxBytes]) + "\n...[truncated, full payload in spill file]", true, ref
}

// llmAuditRoot returns the directory where oversized bodies are spilled, or
// "" when no workspace root is configured (bodies then truncate with no ref).
func (s *Server) llmAuditRoot() string {
	if s.loopGov == nil {
		return ""
	}
	root := s.loopGov.Config().WorkspaceRoot
	if root == "" {
		return ""
	}
	return filepath.Join(root, llmAuditDumpDir)
}

// emitLLMSpan emits a completed OTEL span following the gen-ai semantic
// conventions, plus Langfuse-compatible trace/session/input/output
// attributes. When no tracer provider is configured (telemetry off) the
// global tracer is a no-op, so this is safe and cheap.
func (s *Server) emitLLMSpan(call service.LLMCall, reqBody, respBody []byte) {
	tracer := otel.Tracer("github.com/rakunlabs/at/gateway")

	end := time.Now()
	start := end.Add(-time.Duration(call.LatencyMs) * time.Millisecond)

	_, span := tracer.Start(context.Background(), "chat "+call.Model,
		oteltrace.WithTimestamp(start),
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
	)

	span.SetAttributes(
		attribute.String("gen_ai.operation.name", "chat"),
		attribute.String("gen_ai.system", call.Provider),
		attribute.String("gen_ai.request.model", firstNonEmpty(call.RequestedModel, call.Model)),
		attribute.String("gen_ai.response.model", call.Model),
		attribute.Int64("gen_ai.usage.input_tokens", call.InputTokens),
		attribute.Int64("gen_ai.usage.output_tokens", call.OutputTokens),
		attribute.Bool("gen_ai.request.stream", call.Streamed),
		// Langfuse dimensions.
		attribute.String("langfuse.trace.id", call.TraceID),
		attribute.String("langfuse.observation.type", "generation"),
		attribute.String("gen_ai.prompt", clipSpanBody(reqBody)),
		attribute.String("gen_ai.completion", clipSpanBody(respBody)),
	)
	if call.SessionID != "" {
		span.SetAttributes(attribute.String("langfuse.session.id", call.SessionID))
	}
	if call.UserField != "" {
		span.SetAttributes(attribute.String("langfuse.user.id", call.UserField))
	}
	if call.FinishReason != "" {
		span.SetAttributes(attribute.StringSlice("gen_ai.response.finish_reasons", []string{call.FinishReason}))
	}
	if call.TokenID != "" {
		span.SetAttributes(attribute.String("at.token_id", call.TokenID))
	}
	if call.Status == "error" {
		span.SetStatus(codes.Error, call.ErrorMessage)
		if call.ErrorCode != "" {
			span.SetAttributes(attribute.String("error.type", call.ErrorCode))
		}
	}

	span.End(oteltrace.WithTimestamp(end))
}

func clipSpanBody(b []byte) string {
	if len(b) <= llmAuditSpanBodyCap {
		return string(b)
	}
	return string(b[:llmAuditSpanBodyCap]) + "...[truncated]"
}

// chatRespFinishReason returns the finish reason of the first choice, or ""
// when the response has no choices.
func chatRespFinishReason(resp *ChatCompletionResponse) string {
	if resp == nil || len(resp.Choices) == 0 {
		return ""
	}
	return resp.Choices[0].FinishReason
}

// streamAuditCtx carries the audit correlation and the original request
// bytes into the streaming handler so it can record the reconstructed call
// once the stream completes.
type streamAuditCtx struct {
	auth           *authResult
	source         string
	endpoint       string
	traceID        string
	sessionID      string
	userField      string
	requestBody    []byte
	requestedModel string
}

// resolveSource returns the audit source, defaulting to "gateway_stream"
// for the gateway path that leaves it empty.
func (c streamAuditCtx) resolveSource() string {
	if c.source != "" {
		return c.source
	}
	return "gateway_stream"
}

// usageOrZero dereferences a possibly-nil usage pointer.
func usageOrZero(u *service.Usage) service.Usage {
	if u == nil {
		return service.Usage{}
	}
	return *u
}

// streamAuditResponseBody reconstructs an OpenAI-shape non-streaming
// response JSON from the accumulated streaming deltas, so the audit log
// stores a single coherent response body instead of raw SSE frames.
func streamAuditResponseBody(id, model, content, reasoning string, toolCalls []service.ToolCall, finishReason string, usage *service.Usage) []byte {
	msg := ChatCompletionMessage{Role: "assistant"}
	if content != "" {
		msg.Content = &content
	}
	if reasoning != "" {
		msg.ReasoningContent = &reasoning
	}
	for i, tc := range toolCalls {
		idx := i
		argsJSON, _ := json.Marshal(tc.Arguments)
		msg.ToolCalls = append(msg.ToolCalls, OpenAIToolCall{
			Index: &idx,
			ID:    tc.ID,
			Type:  "function",
			Function: OpenAIFunctionCall{
				Name:      tc.Name,
				Arguments: string(argsJSON),
			},
		})
	}

	resp := ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatCompletionChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReason,
		}},
	}
	if usage != nil {
		resp.Usage = chatCompletionUsageFromService(*usage)
	}

	b, _ := json.Marshal(resp)
	return b
}
