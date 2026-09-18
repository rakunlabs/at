package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// ─── Anthropic response encoding ───

type anthropicResponse struct {
	ID           string                 `json:"id"`
	Type         string                 `json:"type"`
	Role         string                 `json:"role"`
	Model        string                 `json:"model"`
	Content      []map[string]any       `json:"content"`
	StopReason   string                 `json:"stop_reason"`
	StopSequence *string                `json:"stop_sequence"`
	Usage        anthropicResponseUsage `json:"usage"`
}

type anthropicResponseUsage struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
}

func anthropicMessageID() string {
	return "msg_" + ulid.Make().String()
}

// anthropicStopReason maps the internal finish reason, which is normalised to
// OpenAI's vocabulary, back to Anthropic's.
//
// A response carrying pending tool calls is always "tool_use": adapters run
// ReconcileToolCallFinish, so a provider that reported "stop" alongside tool
// calls has already been corrected, and reporting "end_turn" here would tell the
// client the turn was over while handing it calls to execute.
func anthropicStopReason(resp *service.LLMResponse, stopSequences []string) (reason string, matched *string) {
	if len(resp.ToolCalls) > 0 {
		return "tool_use", nil
	}

	switch resp.FinishReason {
	case "length":
		return "max_tokens", nil
	case "content_filter":
		return "refusal", nil
	case "tool_calls", "function_call":
		return "tool_use", nil
	}

	// Anthropic reports which sequence halted generation; the internal response
	// does not carry it, so it is recovered by matching the tail.
	for _, seq := range stopSequences {
		if seq != "" && strings.HasSuffix(resp.Content, seq) {
			s := seq

			return "stop_sequence", &s
		}
	}

	return "end_turn", nil
}

// buildAnthropicResponse encodes an internal response in the Anthropic Messages
// response shape.
func buildAnthropicResponse(id, model string, resp *service.LLMResponse, stopSequences []string) *anthropicResponse {
	content := make([]map[string]any, 0, 1+len(resp.ToolCalls))

	if resp.ReasoningContent != "" {
		block := map[string]any{"type": "thinking", "thinking": resp.ReasoningContent}
		if resp.ReasoningSignature != "" {
			block["signature"] = resp.ReasoningSignature
		}
		content = append(content, block)
	}
	if resp.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": resp.Content})
	}
	for _, tc := range resp.ToolCalls {
		input := tc.Arguments
		if input == nil {
			input = map[string]any{}
		}
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": input,
		})
	}

	stopReason, matched := anthropicStopReason(resp, stopSequences)

	// A refusal is reported as content, not silently as an empty turn: dropping
	// it makes a refusal indistinguishable from a model that said nothing.
	if resp.Refusal != "" && len(content) == 0 {
		content = append(content, map[string]any{"type": "text", "text": resp.Refusal})
	}

	usage := anthropicResponseUsage{
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
	}
	if resp.Usage.CacheReadTokens > 0 {
		v := resp.Usage.CacheReadTokens
		usage.CacheReadInputTokens = &v
	}
	if resp.Usage.CacheWriteTokens > 0 {
		v := resp.Usage.CacheWriteTokens
		usage.CacheCreationInputTokens = &v
	}

	return &anthropicResponse{
		ID:           id,
		Type:         "message",
		Role:         "assistant",
		Model:        model,
		Content:      content,
		StopReason:   stopReason,
		StopSequence: matched,
		Usage:        usage,
	}
}

// ─── Anthropic error envelope ───

// Anthropic's error type vocabulary. Using the OpenAI envelope here would make a
// client's error handling fall through to its generic branch.
const (
	anthropicErrInvalidRequest = "invalid_request_error"
	anthropicErrAuthentication = "authentication_error"
	anthropicErrPermission     = "permission_error"
	anthropicErrNotFound       = "not_found_error"
	anthropicErrRateLimit      = "rate_limit_error"
	anthropicErrAPI            = "api_error"
	anthropicErrOverloaded     = "overloaded_error"
)

func anthropicErrorBody(errType, message string) map[string]any {
	return map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": message,
		},
	}
}

func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	httpResponseJSON(w, anthropicErrorBody(errType, message), status)
}

// anthropicErrorTypeForStatus maps an HTTP status to Anthropic's vocabulary.
func anthropicErrorTypeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return anthropicErrInvalidRequest
	case http.StatusUnauthorized:
		return anthropicErrAuthentication
	case http.StatusForbidden:
		return anthropicErrPermission
	case http.StatusNotFound:
		return anthropicErrNotFound
	case http.StatusTooManyRequests:
		return anthropicErrRateLimit
	case 529:
		return anthropicErrOverloaded
	}
	if status >= 500 {
		return anthropicErrAPI
	}

	return anthropicErrInvalidRequest
}

// ─── Anthropic SSE encoding ───

// anthropicStreamWriter emits the Anthropic event sequence. Anthropic's stream
// is block-structured — a block is opened, deltas flow, the block closes — so the
// writer tracks which block is open rather than emitting deltas standalone.
type anthropicStreamWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher

	messageID string
	model     string

	openIndex int  // index of the currently open content block
	blockOpen bool // whether a content_block_start has been emitted without its stop
	nextIndex int

	textOpen  bool
	toolOpen  map[int]bool
	toolIndex map[string]int // tool call ID → content block index
}

func newAnthropicStreamWriter(w http.ResponseWriter, flusher http.Flusher, messageID, model string) *anthropicStreamWriter {
	return &anthropicStreamWriter{
		w:         w,
		flusher:   flusher,
		messageID: messageID,
		model:     model,
		toolOpen:  map[int]bool{},
		toolIndex: map[string]int{},
	}
}

func (s *anthropicStreamWriter) event(name string, payload map[string]any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	// Anthropic sends both the event name and the JSON type field; clients read
	// one or the other, so both are emitted.
	fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data)
	s.flusher.Flush()
}

// MessageStart opens the stream. Input tokens are reported here when the
// provider supplied them up front.
func (s *anthropicStreamWriter) MessageStart(usage *service.Usage) {
	input := 0
	if usage != nil {
		input = usage.PromptTokens
	}
	s.event("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         s.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage":         map[string]any{"input_tokens": input, "output_tokens": 0},
		},
	})
}

func (s *anthropicStreamWriter) closeBlock() {
	if !s.blockOpen {
		return
	}
	s.event("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": s.openIndex,
	})
	s.blockOpen = false
	s.textOpen = false
}

// TextDelta appends to the open text block, opening one if needed.
func (s *anthropicStreamWriter) TextDelta(text string) {
	if text == "" {
		return
	}
	if !s.textOpen {
		s.closeBlock()
		s.openIndex = s.nextIndex
		s.nextIndex++
		s.blockOpen = true
		s.textOpen = true
		s.event("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         s.openIndex,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
	}
	s.event("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.openIndex,
		"delta": map[string]any{"type": "text_delta", "text": text},
	})
}

// ThinkingDelta streams reasoning content as a thinking block.
func (s *anthropicStreamWriter) ThinkingDelta(text string) {
	if text == "" {
		return
	}
	if !s.blockOpen || s.textOpen {
		s.closeBlock()
		s.openIndex = s.nextIndex
		s.nextIndex++
		s.blockOpen = true
		s.event("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         s.openIndex,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		})
	}
	s.event("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": s.openIndex,
		"delta": map[string]any{"type": "thinking_delta", "thinking": text},
	})
}

// ToolCall emits a tool_use block. The internal stream carries fully-decoded
// arguments rather than raw fragments, so the whole JSON is emitted as one
// input_json_delta — which is a valid partial sequence of exactly one part.
func (s *anthropicStreamWriter) ToolCall(tc service.ToolCall) {
	s.closeBlock()

	index, seen := s.toolIndex[tc.ID]
	if !seen {
		index = s.nextIndex
		s.nextIndex++
		s.toolIndex[tc.ID] = index
		s.event("content_block_start", map[string]any{
			"type":  "content_block_start",
			"index": index,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Name,
				"input": map[string]any{},
			},
		})
	}

	args := tc.Arguments
	if args == nil {
		args = map[string]any{}
	}
	partial, err := json.Marshal(args)
	if err != nil {
		partial = []byte("{}")
	}
	s.event("content_block_delta", map[string]any{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]any{"type": "input_json_delta", "partial_json": string(partial)},
	})
	s.event("content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": index,
	})
}

// Finish closes any open block and terminates the message.
func (s *anthropicStreamWriter) Finish(stopReason string, stopSequence *string, usage *service.Usage) {
	s.closeBlock()

	delta := map[string]any{"stop_reason": stopReason, "stop_sequence": nil}
	if stopSequence != nil {
		delta["stop_sequence"] = *stopSequence
	}

	output := 0
	if usage != nil {
		output = usage.CompletionTokens
	}
	s.event("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": delta,
		"usage": map[string]any{"output_tokens": output},
	})
	s.event("message_stop", map[string]any{"type": "message_stop"})
}

// Error emits an error event on an already-open stream. Once the first byte has
// been written there is no status code left to set, so this is the only way to
// tell the client what happened.
func (s *anthropicStreamWriter) Error(errType, message string) {
	s.closeBlock()
	s.event("error", anthropicErrorBody(errType, message))
}
