// Package agentloop contains policy-neutral building blocks shared by AT's
// agentic loops. Loop lifecycle, retries, persistence, confirmation, and tool
// dispatch remain owned by each caller.
package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// CallGovernor is the subset of loop governance needed for one provider call.
// Both loopgov.Governor and workflow.LoopGovernor satisfy it.
type CallGovernor interface {
	LimitWithTools(ctx context.Context, agentID, taskID string, messages []service.Message, tools []service.Tool) ([]service.Message, error)
	ChatOptions() *service.ChatOptions
}

// ToolResultTruncater is the independent governance boundary needed before a
// tool result enters message history.
type ToolResultTruncater interface {
	TruncateToolResult(runID, toolName, body string) (string, bool)
}

// CallProvider applies context governance and performs exactly one provider
// call. Retry policy deliberately remains with the caller.
func CallProvider(
	ctx context.Context,
	governor CallGovernor,
	provider service.LLMProvider,
	model, agentID, taskID string,
	messages []service.Message,
	tools []service.Tool,
	reasoningEffort string,
) (resp *service.LLMResponse, callMessages []service.Message, latencyMs int64, err error) {
	callMessages = messages
	if err := service.ValidateReasoningEffort(reasoningEffort); err != nil {
		return nil, callMessages, 0, fmt.Errorf("agent reasoning effort: %w", err)
	}
	if validator, ok := provider.(service.ReasoningEffortValidator); ok {
		if err := validator.ValidateReasoningEffort(reasoningEffort); err != nil {
			return nil, callMessages, 0, fmt.Errorf("agent provider reasoning effort: %w", err)
		}
	}
	var opts *service.ChatOptions
	if governor != nil {
		// Governors recover from summarization failures by dropping old context;
		// callers historically treat that fallback as best-effort.
		callMessages, _ = governor.LimitWithTools(ctx, agentID, taskID, messages, tools)
		opts = governor.ChatOptions()
	}
	if reasoningEffort != "" {
		merged := service.ChatOptions{}
		if opts != nil {
			merged = *opts
		}
		merged.ReasoningEffort = reasoningEffort
		opts = &merged
	}

	started := time.Now()
	resp, err = provider.Chat(ctx, model, callMessages, tools, opts)
	return resp, callMessages, time.Since(started).Milliseconds(), err
}

// AssistantMessage converts a normalized provider response into the canonical
// assistant message retained by agent loops. Signed reasoning precedes visible
// text, followed by tool calls in provider order. Unsigned reasoning is not
// replayed because providers such as Anthropic reject modified thinking blocks.
// Opaque tool thought signatures stay on their corresponding tool-use blocks.
func AssistantMessage(resp *service.LLMResponse) service.Message {
	blocks := make([]service.ContentBlock, 0, len(resp.ToolCalls)+2)
	if resp.ReasoningSignature != "" {
		blocks = append(blocks, service.ContentBlock{
			Type:      "thinking",
			Thinking:  resp.ReasoningContent,
			Signature: resp.ReasoningSignature,
		})
	}
	if resp.Content != "" {
		blocks = append(blocks, service.ContentBlock{
			Type: "text",
			Text: resp.Content,
		})
	}
	for _, tc := range resp.ToolCalls {
		input := tc.Arguments
		if input == nil {
			input = map[string]any{}
		}
		blocks = append(blocks, service.ContentBlock{
			Type:             "tool_use",
			ID:               tc.ID,
			Name:             tc.Name,
			Input:            input,
			ThoughtSignature: tc.ThoughtSignature,
		})
	}

	return service.Message{Role: "assistant", Content: blocks}
}

// GenerationRequestJSON serializes the canonical request shape recorded by
// every agent loop after message governance has been applied.
func GenerationRequestJSON(model string, messages []service.Message, tools []service.Tool, reasoningEffort string) []byte {
	body, _ := json.Marshal(struct {
		ReasoningEffort string            `json:"reasoning_effort,omitempty"`
		Model           string            `json:"model"`
		Messages        []service.Message `json:"messages"`
		Tools           []service.Tool    `json:"tools"`
	}{
		ReasoningEffort: reasoningEffort,
		Model:           model,
		Messages:        messages,
		Tools:           tools,
	})
	return body
}

// ObservationContext identifies observations produced by one agent-loop run.
type ObservationContext struct {
	Source          string
	TraceID         string
	SessionID       string
	AgentID         string
	TaskID          string
	RunID           string
	OrganizationID  string
	Provider        string
	Model           string
	ReasoningEffort string
}

// GenerationObservationParams contains the common data around one provider
// call. Response is nil on failed calls.
type GenerationObservationParams struct {
	Context   ObservationContext
	Messages  []service.Message
	Tools     []service.Tool
	Response  *service.LLMResponse
	LatencyMs int64
	Iteration int
	Err       error
	ErrorCode string
	Metadata  map[string]any
}

// NewGenerationObservation builds a generation observation without applying
// recorder-specific body-retention or persistence policy.
func NewGenerationObservation(p GenerationObservationParams) service.LLMCall {
	metadata := cloneMetadata(p.Metadata)
	metadata["iteration"] = p.Iteration

	obs := service.LLMCall{
		ObservationType: service.ObservationGeneration,
		Source:          p.Context.Source,
		TraceID:         p.Context.TraceID,
		SessionID:       p.Context.SessionID,
		AgentID:         p.Context.AgentID,
		TaskID:          p.Context.TaskID,
		RunID:           p.Context.RunID,
		OrganizationID:  p.Context.OrganizationID,
		Provider:        p.Context.Provider,
		Model:           p.Context.Model,
		RequestedModel:  p.Context.Provider + "/" + p.Context.Model,
		RequestBody:     string(GenerationRequestJSON(p.Context.Model, p.Messages, p.Tools, p.Context.ReasoningEffort)),
		LatencyMs:       p.LatencyMs,
		Metadata:        metadata,
	}

	if p.Err != nil {
		obs.Status = "error"
		obs.Level = service.ObservationLevelError
		obs.ErrorCode = p.ErrorCode
		obs.ErrorMessage = p.Err.Error()
		return obs
	}

	if p.Response != nil {
		responseBody, _ := json.Marshal(p.Response)
		obs.ResponseBody = string(responseBody)
		obs.InputTokens = int64(p.Response.Usage.PromptTokens)
		obs.OutputTokens = int64(p.Response.Usage.CompletionTokens)
		obs.CacheReadTokens = int64(p.Response.Usage.CacheReadTokens)
		obs.CacheWriteTokens = int64(p.Response.Usage.CacheWriteTokens)
		obs.ReasoningTokens = int64(p.Response.Usage.ReasoningTokens)
		obs.FinishReason = p.Response.FinishReason
		metadata["finished"] = p.Response.Finished
		metadata["tool_calls"] = len(p.Response.ToolCalls)
	}

	return obs
}

// ToolObservationParams contains the common data around one tool execution.
type ToolObservationParams struct {
	Context             ObservationContext
	ParentObservationID string
	Tool                service.ToolCall
	Output              string
	LatencyMs           int64
	Iteration           int
	Err                 error
	Metadata            map[string]any
}

// NewToolObservation builds a tool observation without choosing dispatch,
// confirmation, persistence, or event-delivery policy.
func NewToolObservation(p ToolObservationParams) service.LLMCall {
	input, _ := json.Marshal(p.Tool.Arguments)
	metadata := cloneMetadata(p.Metadata)
	metadata["iteration"] = p.Iteration
	level := service.ObservationLevelDefault
	if p.Err != nil {
		level = service.ObservationLevelError
	}

	return service.LLMCall{
		ObservationType:     service.ObservationTool,
		ParentObservationID: p.ParentObservationID,
		Name:                p.Tool.Name,
		Input:               string(input),
		Output:              p.Output,
		Level:               level,
		Metadata:            metadata,
		TraceID:             p.Context.TraceID,
		SessionID:           p.Context.SessionID,
		Source:              p.Context.Source,
		AgentID:             p.Context.AgentID,
		TaskID:              p.Context.TaskID,
		RunID:               p.Context.RunID,
		OrganizationID:      p.Context.OrganizationID,
		LatencyMs:           p.LatencyMs,
	}
}

// ToolResult applies the global result cap and constructs the content block
// returned to the provider. It returns the retained text for event and
// observation callers that need the exact history value.
func ToolResult(governor ToolResultTruncater, runID string, tool service.ToolCall, output string) (string, service.ContentBlock) {
	if governor != nil {
		output, _ = governor.TruncateToolResult(runID, tool.Name, output)
	}
	return output, service.ContentBlock{
		Type:      "tool_result",
		ToolUseID: tool.ID,
		Content:   output,
	}
}

func cloneMetadata(metadata map[string]any) map[string]any {
	cloned := make(map[string]any, len(metadata)+3)
	for key, value := range metadata {
		cloned[key] = value
	}
	return cloned
}

// StreamDelta is one incremental piece of a streamed model response.
type StreamDelta struct {
	Content   string
	Reasoning string
}

// CallProviderStream behaves like CallProvider but streams text through onDelta
// when the provider supports it, then assembles the same LLMResponse the
// non-streaming path returns. Providers without streaming fall back to one
// Chat call whose text is delivered as a single delta, so callers need no
// second code path.
func CallProviderStream(
	ctx context.Context,
	governor CallGovernor,
	provider service.LLMProvider,
	model, agentID, taskID string,
	messages []service.Message,
	tools []service.Tool,
	onDelta func(StreamDelta),
) (resp *service.LLMResponse, callMessages []service.Message, latencyMs int64, err error) {
	streamer, ok := provider.(service.ExecutionStreamer)
	if !ok {
		resp, callMessages, latencyMs, err = CallProvider(ctx, governor, provider, model, agentID, taskID, messages, tools, "")
		if err == nil && onDelta != nil && (resp.Content != "" || resp.ReasoningContent != "") {
			onDelta(StreamDelta{Content: resp.Content, Reasoning: resp.ReasoningContent})
		}
		return resp, callMessages, latencyMs, err
	}
	callMessages = messages
	var opts *service.ChatOptions
	if governor != nil {
		callMessages, _ = governor.LimitWithTools(ctx, agentID, taskID, messages, tools)
		opts = governor.ChatOptions()
	}
	started := time.Now()
	chunks, err := streamer.StreamChat(ctx, model, callMessages, tools, opts)
	if errors.Is(err, service.ErrUnsupportedOperation) {
		resp, err = provider.Chat(ctx, model, callMessages, tools, opts)
		if err == nil && onDelta != nil && (resp.Content != "" || resp.ReasoningContent != "") {
			onDelta(StreamDelta{Content: resp.Content, Reasoning: resp.ReasoningContent})
		}
		return resp, callMessages, time.Since(started).Milliseconds(), err
	}
	if err != nil {
		return nil, callMessages, time.Since(started).Milliseconds(), err
	}
	resp, err = CollectStream(ctx, chunks, onDelta)
	return resp, callMessages, time.Since(started).Milliseconds(), err
}

// CollectStream drains a provider stream into one response. Adapters emit each
// tool call once, complete, when its arguments finish; a repeated ID replaces
// the earlier entry rather than duplicating it. A stream that closes without a
// finish reason or tool call is an error: it is indistinguishable from a
// dropped connection, and treating it as success would end the turn silently.
func CollectStream(ctx context.Context, chunks <-chan service.StreamChunk, onDelta func(StreamDelta)) (*service.LLMResponse, error) {
	var content, reasoning strings.Builder
	resp := &service.LLMResponse{}
	index := map[string]int{}
	finished := false
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				if !finished && len(resp.ToolCalls) == 0 && content.Len() == 0 {
					return nil, errors.New("model stream ended without a response")
				}
				resp.Content = content.String()
				resp.ReasoningContent = reasoning.String()
				if resp.FinishReason == "" {
					resp.FinishReason = "stop"
				}
				// Truncated or filtered output may carry incomplete calls; they are
				// never executed, and the stop reason is preserved for the caller.
				if resp.FinishReason == "length" || resp.FinishReason == "content_filter" {
					resp.ToolCalls = nil
				}
				resp.Finished = len(resp.ToolCalls) == 0
				if len(resp.ToolCalls) > 0 {
					resp.FinishReason = "tool_calls"
				}
				return resp, nil
			}
			if chunk.Error != nil {
				return nil, chunk.Error
			}
			if chunk.Content != "" || chunk.ReasoningContent != "" {
				content.WriteString(chunk.Content)
				reasoning.WriteString(chunk.ReasoningContent)
				if onDelta != nil {
					onDelta(StreamDelta{Content: chunk.Content, Reasoning: chunk.ReasoningContent})
				}
			}
			for _, call := range chunk.ToolCalls {
				if at, seen := index[call.ID]; seen && call.ID != "" {
					resp.ToolCalls[at] = call
					continue
				}
				index[call.ID] = len(resp.ToolCalls)
				resp.ToolCalls = append(resp.ToolCalls, call)
			}
			if chunk.Usage != nil {
				resp.Usage = *chunk.Usage
			}
			if chunk.FinishReason != "" {
				finished = true
				resp.FinishReason = normalizeStreamFinish(chunk.FinishReason)
			}
		}
	}
}

func normalizeStreamFinish(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence", "STOP":
		return "stop"
	case "max_tokens", "MAX_TOKENS":
		return "length"
	case "tool_use":
		return "tool_calls"
	}
	return reason
}
