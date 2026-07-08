package service

import (
	"context"
	"time"

	"github.com/rakunlabs/query"
)

// ─── LLM Call Audit (Langfuse-style request/response tracing) ───

// Feature key for the LLM call audit toggle (see server feature catalog).
const FeatureLLMAudit = "llm_audit"

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

// LLMCallRetention is how long llm_call rows (and their spill files) are
// kept before the janitor removes them. Full-body audit data is large and
// often sensitive; keep the window short by default.
const LLMCallRetention = 7 * 24 * time.Hour

// LLMCall records a single LLM request/response pair with full bodies —
// the audit analogue of a Langfuse "generation" observation. One row per
// upstream provider call (each fallback attempt gets its own row, linked
// by TraceID).
type LLMCall struct {
	ID string `json:"id"`
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

// LLMCallStorer defines persistence for the LLM call audit log.
type LLMCallStorer interface {
	RecordLLMCall(ctx context.Context, call LLMCall) error
	// ListLLMCalls returns rows with request/response bodies clipped to
	// LLMCallPreviewBytes; use GetLLMCall for the full record.
	ListLLMCalls(ctx context.Context, q *query.Query) (*ListResult[LLMCall], error)
	GetLLMCall(ctx context.Context, id string) (*LLMCall, error)
	// DeleteLLMCallsBefore removes rows created before the cutoff
	// (RFC3339) and returns the number deleted. Spill files are the
	// caller's responsibility (swept by mtime).
	DeleteLLMCallsBefore(ctx context.Context, cutoff string) (int64, error)
}
