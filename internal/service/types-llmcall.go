package service

import (
	"context"
	"time"

	"github.com/rakunlabs/query"
)

// ─── LLM Call Audit (Langfuse-style trace/observation tracing) ───

// Feature key for the LLM call audit toggle (see server feature catalog).
// The flag gates full-body capture only; observation skeletons (tokens,
// cost, latency, hierarchy, tool previews) are always recorded.
const FeatureLLMAudit = "llm_audit"

// Observation types, mirroring Langfuse's model. Every llm_calls row is one
// observation inside a trace.
const (
	// ObservationGeneration is a single LLM request/response pair.
	ObservationGeneration = "generation"
	// ObservationTool is a tool execution requested by a generation.
	ObservationTool = "tool"
	// ObservationEvent is a point-in-time occurrence (task lifecycle).
	ObservationEvent = "event"
)

// Observation levels.
const (
	ObservationLevelDefault = "default"
	ObservationLevelWarning = "warning"
	ObservationLevelError   = "error"
)

// LLMCallBodyMaxBytes is the per-side inline cap (request or response body)
// stored in the llm_calls table. Bodies larger than this are truncated
// inline and the full payload is spilled to a file on disk whose path is
// recorded in RequestRef / ResponseRef. Multimodal requests with base64
// images routinely reach megabytes; without a cap a single call could
// bloat the DB and slow every list query.
const LLMCallBodyMaxBytes = 262144 // 256 KB

// LLMCallPreviewBytes is the per-side preview length returned by list
// queries. Full bodies are only loaded on the detail endpoint.
const LLMCallPreviewBytes = 2048

// LLMCallRetention is the body retention window: after this, the janitor
// nulls request/response bodies (and full tool input/output) and removes
// spill files. Full-body audit data is large and often sensitive; keep the
// window short by default. Observation skeletons live on until
// ObservationRetention.
const LLMCallRetention = 7 * 24 * time.Hour

// ObservationRetention is how long observation rows (skeletons) are kept
// before the janitor deletes them entirely.
const ObservationRetention = 90 * 24 * time.Hour

// ObservationPreviewBytes is the inline cap on tool/event input and output
// previews. Larger payloads are truncated inline; the full payload is only
// kept (via spill) when the llm_audit feature is enabled.
const ObservationPreviewBytes = 4096

// LLMCall records a single LLM request/response pair with full bodies —
// the audit analogue of a Langfuse "generation" observation. One row per
// upstream provider call (each fallback attempt gets its own row, linked
// by TraceID).
type LLMCall struct {
	ID string `json:"id"`
	// ObservationType is one of ObservationGeneration / ObservationTool /
	// ObservationEvent. Empty is treated as "generation" (pre-migration
	// rows and gateway callers).
	ObservationType string `json:"observation_type,omitempty"`
	// ParentObservationID nests this observation under another one in the
	// same trace (e.g. a tool observation under the generation that
	// requested it). Empty = trace-level.
	ParentObservationID string `json:"parent_observation_id,omitempty"`
	// Name is the human label: tool name for tool observations, action
	// (task_started, task_delegated, ...) for events, optional for
	// generations.
	Name string `json:"name,omitempty"`
	// Input / Output carry tool arguments and results (or event details).
	// Inline previews are capped at ObservationPreviewBytes.
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	// Level is ObservationLevelDefault / Warning / Error.
	Level string `json:"level,omitempty"`
	// Metadata carries structured extras (child trace IDs for delegation
	// tools, task titles for events, ...).
	Metadata map[string]any `json:"metadata,omitempty"`
	// TraceID groups related calls (fallback attempts of one gateway
	// request, or every call in one agent run). Client-suppliable via
	// the x-at-trace-id header; generated otherwise.
	TraceID string `json:"trace_id,omitempty"`
	// SessionID groups traces into a conversation (x-at-session-id
	// header, chat session ID, or task ID).
	SessionID string `json:"session_id,omitempty"`
	// Source tells which subsystem made the call: "gateway",
	// "gateway_stream", "responses", "agent", "chat", "workflow".
	Source string `json:"source"`
	// Endpoint is the HTTP path that received the original request.
	Endpoint string `json:"endpoint,omitempty"`

	// Attribution.
	TokenID        string `json:"token_id,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	TaskID         string `json:"task_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	OrganizationID string `json:"organization_id,omitempty"`

	Provider string `json:"provider"`
	Model    string `json:"model"`
	// RequestedModel is the full "provider/model" string the client asked
	// for; differs from Provider/Model when a fallback served the call.
	RequestedModel string `json:"requested_model,omitempty"`

	// RequestBody / ResponseBody hold the full JSON payloads, truncated
	// at LLMCallBodyMaxBytes. When truncated, the *Truncated flag is set
	// and the *Ref field points at the spill file with the full payload.
	RequestBody       string `json:"request_body,omitempty"`
	ResponseBody      string `json:"response_body,omitempty"`
	RequestBytes      int64  `json:"request_bytes"`
	ResponseBytes     int64  `json:"response_bytes"`
	RequestTruncated  bool   `json:"request_truncated"`
	ResponseTruncated bool   `json:"response_truncated"`
	RequestRef        string `json:"request_ref,omitempty"`
	ResponseRef       string `json:"response_ref,omitempty"`

	Streamed bool `json:"streamed"`

	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	CostCents        float64 `json:"cost_cents"`

	LatencyMs int64 `json:"latency_ms"`
	// TimeToFirstTokenMs is only set for true streaming calls.
	TimeToFirstTokenMs int64 `json:"time_to_first_token_ms,omitempty"`

	Status       string `json:"status"` // "ok" | "error"
	ErrorCode    string `json:"error_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"`

	// UserField carries the OpenAI `user` request parameter for
	// end-user attribution, mirroring Langfuse's user dimension.
	UserField string `json:"user_field,omitempty"`

	CreatedAt string `json:"created_at"`
}

// LLMCallTrace is an aggregated view over the observations sharing one
// trace_id — the trace-list row of the trace explorer.
type LLMCallTrace struct {
	TraceID          string  `json:"trace_id"`
	SessionID        string  `json:"session_id,omitempty"`
	Source           string  `json:"source"`
	Name             string  `json:"name,omitempty"`
	TaskID           string  `json:"task_id,omitempty"`
	AgentID          string  `json:"agent_id,omitempty"`
	OrganizationID   string  `json:"organization_id,omitempty"`
	ObservationCount int64   `json:"observation_count"`
	GenerationCount  int64   `json:"generation_count"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CostCents        float64 `json:"cost_cents"`
	LatencyMsTotal   int64   `json:"latency_ms_total"`
	ErrorCount       int64   `json:"error_count"`
	StartedAt        string  `json:"started_at"`
	EndedAt          string  `json:"ended_at"`
}

// NewToolObservation builds a tool observation skeleton. Callers fill in
// attribution (trace/session/task/agent/org) and parent afterwards or via
// the recorder params.
func NewToolObservation(name, input, output string, level string) LLMCall {
	if level == "" {
		level = ObservationLevelDefault
	}
	return LLMCall{
		ObservationType: ObservationTool,
		Name:            name,
		Input:           input,
		Output:          output,
		Level:           level,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
}

// NewEventObservation builds an event observation skeleton.
func NewEventObservation(name string, metadata map[string]any) LLMCall {
	return LLMCall{
		ObservationType: ObservationEvent,
		Name:            name,
		Metadata:        metadata,
		Level:           ObservationLevelDefault,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
}

// TruncateObservationIO clips a tool/event payload to the inline preview
// cap on a UTF-8 boundary, appending a marker when truncated.
func TruncateObservationIO(s string) string {
	if len(s) <= ObservationPreviewBytes {
		return s
	}
	cut := ObservationPreviewBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "\n...[truncated]"
}

// LLMCallStorer defines persistence for the LLM call audit log.
type LLMCallStorer interface {
	RecordLLMCall(ctx context.Context, call LLMCall) error
	// ListLLMCalls returns rows with request/response bodies clipped to
	// LLMCallPreviewBytes; use GetLLMCall for the full record.
	ListLLMCalls(ctx context.Context, q *query.Query) (*ListResult[LLMCall], error)
	GetLLMCall(ctx context.Context, id string) (*LLMCall, error)
	// ListLLMCallTraces returns aggregated per-trace rows (GROUP BY
	// trace_id), newest-first.
	ListLLMCallTraces(ctx context.Context, q *query.Query) (*ListResult[LLMCallTrace], error)
	// DeleteLLMCallsBefore removes rows created before the cutoff
	// (RFC3339) and returns the number deleted. Spill files are the
	// caller's responsibility (swept by mtime).
	DeleteLLMCallsBefore(ctx context.Context, cutoff string) (int64, error)
	// ExpireLLMCallBodiesBefore nulls body/full-IO columns on rows older
	// than the cutoff, keeping the observation skeleton queryable.
	ExpireLLMCallBodiesBefore(ctx context.Context, cutoff string) (int64, error)
}
