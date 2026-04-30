package openaicompatible

import (
	"bytes"
	"encoding/json"
)

// ─── Roles ────────────────────────────────────────────────────────────────

// Standard message roles. Servers may accept additional values; these are
// just the well-known ones for convenience and to avoid string typos.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
	// RoleDeveloper is OpenAI's instruction-priority role for newer models
	// (o-series, gpt-4.1+). Servers that don't recognise it will typically
	// treat it as system.
	RoleDeveloper = "developer"
)

// ─── Messages ─────────────────────────────────────────────────────────────

// Message is one entry in the chat history. The on-the-wire format follows
// OpenAI's spec exactly, so callers can populate any field a particular
// server understands.
//
// Content can be either a plain string (the most common case) or a slice
// of [ContentPart] for multimodal input. Use the constructor helpers
// ([UserMessage], [SystemMessage], [AssistantMessage], [ToolMessage]) for
// readable code.
type Message struct {
	Role string `json:"role"`
	// Content is either string, []ContentPart, or nil.
	Content any `json:"content,omitempty"`
	// Name is optional; some servers use it to disambiguate participants
	// or to identify the function whose result is being returned.
	Name string `json:"name,omitempty"`
	// ToolCallID is set on role="tool" messages to associate the tool
	// result with the assistant's earlier tool_call.id.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// ToolCalls is set on role="assistant" messages that requested one or
	// more tool invocations.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent is exposed by some servers (e.g. Anthropic via the
	// AT gateway, DeepSeek-R1, OpenAI o-series) to surface chain-of-thought
	// separately from the final answer. Optional.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// Refusal carries an explicit refusal message from OpenAI safety models.
	Refusal string `json:"refusal,omitempty"`
}

// ContentPart is a single block of multimodal content within a [Message].
// Use the helpers [TextPart], [ImageURLPart], [ImageDataPart],
// [InputAudioPart], and [FilePart] to construct parts.
type ContentPart struct {
	Type string `json:"type"`

	// Text is set when Type == "text".
	Text string `json:"text,omitempty"`

	// ImageURL is set when Type == "image_url".
	ImageURL *ImageURL `json:"image_url,omitempty"`

	// InputAudio is set when Type == "input_audio".
	InputAudio *InputAudio `json:"input_audio,omitempty"`

	// File is set when Type == "file".
	File *FileContent `json:"file,omitempty"`
}

// ImageURL references an image either by URL or by inline base64 data URI.
type ImageURL struct {
	// URL is either an https:// URL or a data: URI of the form
	// "data:image/png;base64,<base64-data>".
	URL string `json:"url"`
	// Detail controls the model's image fidelity: "low", "high", or "auto".
	Detail string `json:"detail,omitempty"`
}

// InputAudio holds inline base64-encoded audio.
type InputAudio struct {
	Data   string `json:"data"`             // base64-encoded
	Format string `json:"format,omitempty"` // e.g. "wav", "mp3"
}

// FileContent describes an attached file. Either FileID (already uploaded)
// or FileData (inline base64) should be set.
type FileContent struct {
	FileID   string `json:"file_id,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"` // base64
}

// ─── Tools ────────────────────────────────────────────────────────────────

// Tool describes a function the model may call.
//
// Currently only Type == "function" is widely supported across providers;
// some servers may add other tool types (web_search, code_interpreter, …)
// — set Type accordingly.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction defines a callable function the model can request.
type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"` // JSON schema
	Strict      *bool          `json:"strict,omitempty"`
}

// ToolCall is emitted by the model when it wants to invoke a function.
// Arguments is the raw JSON string returned by the model — call
// [ToolCall.UnmarshalArguments] to decode it into a Go value, or
// [ToolCall.ArgumentsMap] to get a map[string]any.
type ToolCall struct {
	// Index is set on streaming deltas so fragments of the same tool call
	// can be reassembled. nil on non-streaming responses.
	Index *int `json:"index,omitempty"`

	ID       string             `json:"id"`
	Type     string             `json:"type"` // "function"
	Function ToolCallFunction   `json:"function"`
}

// ToolCallFunction is the function-call payload of a [ToolCall].
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // raw JSON string per OpenAI spec
}

// UnmarshalArguments decodes the function-call arguments JSON into v.
// Returns nil if Arguments is empty.
func (tc ToolCall) UnmarshalArguments(v any) error {
	if tc.Function.Arguments == "" {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(tc.Function.Arguments)))
	dec.UseNumber()
	return dec.Decode(v)
}

// ArgumentsMap decodes the function-call arguments into a map[string]any.
// Returns an empty map if Arguments is empty.
func (tc ToolCall) ArgumentsMap() (map[string]any, error) {
	out := map[string]any{}
	if tc.Function.Arguments == "" {
		return out, nil
	}
	if err := json.Unmarshal([]byte(tc.Function.Arguments), &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── Chat Request / Response ──────────────────────────────────────────────

// ChatRequest is the body sent to POST /chat/completions. Fields map 1:1 to
// the OpenAI API. Use Extra to set anything this struct does not model
// directly (provider-specific knobs, future fields, …); Extra entries are
// merged into the JSON body.
type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`

	// Tools and tool selection.
	Tools      []Tool `json:"tools,omitempty"`
	ToolChoice any    `json:"tool_choice,omitempty"` // "auto" | "none" | "required" | {type:"function",function:{name:"x"}}
	ParallelToolCalls *bool `json:"parallel_tool_calls,omitempty"`

	// Sampling.
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	N                *int     `json:"n,omitempty"`
	Stop             any      `json:"stop,omitempty"` // string or []string
	Seed             *int     `json:"seed,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	LogitBias        map[string]int `json:"logit_bias,omitempty"`

	// Output limits.
	MaxTokens           *int `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int `json:"max_completion_tokens,omitempty"` // OpenAI o-series

	// Output shape.
	ResponseFormat any    `json:"response_format,omitempty"` // {"type":"json_object"} or json_schema
	User           string `json:"user,omitempty"`

	// Reasoning / thinking.
	ReasoningEffort string `json:"reasoning_effort,omitempty"` // "low" | "medium" | "high"

	// Streaming. Callers should use [Client.ChatStream] rather than setting
	// these directly; ChatStream populates them automatically.
	Stream        bool           `json:"stream,omitempty"`
	StreamOptions *StreamOptions `json:"stream_options,omitempty"`

	// Extra carries arbitrary additional fields that will be merged into
	// the JSON body. Use it for server-specific extensions such as
	// "web_search_options", "thinking", "top_k", "min_p", etc.
	Extra map[string]any `json:"-"`
}

// StreamOptions is documented under OpenAI's stream_options request field.
type StreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

// MarshalJSON merges Extra into the wire body without losing the typed
// fields. Extra keys do not overwrite already-set typed fields.
func (r ChatRequest) MarshalJSON() ([]byte, error) {
	type alias ChatRequest
	base, err := json.Marshal(alias(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return base, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(base, &m); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		if _, exists := m[k]; exists {
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		m[k] = raw
	}
	return json.Marshal(m)
}

// ChatResponse is the body of a non-streaming /chat/completions response.
type ChatResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   *Usage   `json:"usage,omitempty"`

	// SystemFingerprint is a backend identifier (OpenAI feature; may be
	// empty on other servers).
	SystemFingerprint string `json:"system_fingerprint,omitempty"`
}

// Choice is one of N completions returned by the server.
type Choice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
	// Logprobs is left as raw bytes — wire format varies between providers.
	Logprobs json.RawMessage `json:"logprobs,omitempty"`
}

// Usage reports token consumption for a request.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`

	// Some servers expose more granular breakdowns. These map directly to
	// fields seen in OpenAI / Anthropic responses; absent fields stay zero.
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details,omitempty"`
}

// PromptTokensDetails is the optional breakdown of the prompt token count.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens,omitempty"`
	AudioTokens  int `json:"audio_tokens,omitempty"`
}

// CompletionTokensDetails is the optional breakdown of completion tokens.
type CompletionTokensDetails struct {
	ReasoningTokens          int `json:"reasoning_tokens,omitempty"`
	AudioTokens              int `json:"audio_tokens,omitempty"`
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens,omitempty"`
	RejectedPredictionTokens int `json:"rejected_prediction_tokens,omitempty"`
}

// FirstChoice returns the first completion or nil if none. Convenience for
// the common single-choice case.
func (r *ChatResponse) FirstChoice() *Choice {
	if r == nil || len(r.Choices) == 0 {
		return nil
	}
	return &r.Choices[0]
}

// Content returns the assistant text from the first choice, or "".
func (r *ChatResponse) Content() string {
	c := r.FirstChoice()
	if c == nil {
		return ""
	}
	if s, ok := c.Message.Content.(string); ok {
		return s
	}
	return ""
}

// ToolCalls returns the tool calls from the first choice, or nil.
func (r *ChatResponse) ToolCalls() []ToolCall {
	c := r.FirstChoice()
	if c == nil {
		return nil
	}
	return c.Message.ToolCalls
}
