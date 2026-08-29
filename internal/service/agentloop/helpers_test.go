package agentloop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestAssistantMessage(t *testing.T) {
	resp := &service.LLMResponse{
		ReasoningContent:   "considering",
		ReasoningSignature: "signed-reasoning",
		Content:            "answer",
		ToolCalls: []service.ToolCall{
			{ID: "call-1", Name: "lookup", ThoughtSignature: "opaque"},
			{ID: "call-2", Name: "write", Arguments: map[string]any{"path": "out.txt"}},
		},
	}

	message := AssistantMessage(resp)
	blocks, ok := message.Content.([]service.ContentBlock)
	if !ok {
		t.Fatalf("content type = %T, want []service.ContentBlock", message.Content)
	}
	if message.Role != "assistant" || len(blocks) != 4 {
		t.Fatalf("unexpected assistant message: %+v", message)
	}
	if blocks[0].Type != "thinking" || blocks[0].Thinking != "considering" || blocks[0].Signature != "signed-reasoning" || blocks[0].Text != "" {
		t.Fatalf("reasoning block = %+v", blocks[0])
	}
	if blocks[1].Type != "text" || blocks[1].Text != "answer" {
		t.Fatalf("text block = %+v", blocks[1])
	}
	if blocks[2].Input == nil || blocks[2].ThoughtSignature != "opaque" {
		t.Fatalf("first tool block did not normalize input or preserve thought signature: %+v", blocks[2])
	}
	if blocks[3].Input["path"] != "out.txt" {
		t.Fatalf("second tool block = %+v", blocks[3])
	}
}

func TestAssistantMessageOmitsUnsignedReasoning(t *testing.T) {
	message := AssistantMessage(&service.LLMResponse{
		ReasoningContent: "summary without verifiable state",
		Content:          "answer",
		ToolCalls: []service.ToolCall{{
			ID: "call-1", Name: "lookup",
		}},
	})

	blocks := message.Content.([]service.ContentBlock)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v, want text and tool_use only", blocks)
	}
	for _, block := range blocks {
		if block.Type == "thinking" {
			t.Fatalf("unsigned thinking block was replayed: %+v", block)
		}
	}
}

func TestAssistantMessagePreservesSignatureWithoutReasoningSummary(t *testing.T) {
	message := AssistantMessage(&service.LLMResponse{ReasoningSignature: "opaque-state"})
	blocks := message.Content.([]service.ContentBlock)
	if len(blocks) != 1 || blocks[0].Type != "thinking" || blocks[0].Thinking != "" || blocks[0].Signature != "opaque-state" {
		t.Fatalf("omitted-summary reasoning block = %+v", blocks)
	}
}

func TestGenerationObservation(t *testing.T) {
	ctx := ObservationContext{
		Source: "workflow", TraceID: "trace-1", AgentID: "agent-1", RunID: "run-1",
		Provider: "provider-1", Model: "model-1",
	}
	resp := &service.LLMResponse{
		Content:      "done",
		Finished:     true,
		FinishReason: "stop",
		Usage: service.Usage{
			PromptTokens: 4, CompletionTokens: 3, CacheReadTokens: 2,
			CacheWriteTokens: 1, ReasoningTokens: 1,
		},
	}
	obs := NewGenerationObservation(GenerationObservationParams{
		Context:   ctx,
		Messages:  []service.Message{{Role: "user", Content: "hello"}},
		Response:  resp,
		Iteration: 2,
	})

	if obs.ObservationType != service.ObservationGeneration || obs.RequestedModel != "provider-1/model-1" {
		t.Fatalf("unexpected generation identity: %+v", obs)
	}
	if !strings.Contains(obs.RequestBody, `"messages"`) || !strings.Contains(obs.ResponseBody, `"Content":"done"`) {
		t.Fatalf("missing generation bodies: request=%q response=%q", obs.RequestBody, obs.ResponseBody)
	}
	if obs.InputTokens != 4 || obs.OutputTokens != 3 || obs.CacheReadTokens != 2 || obs.CacheWriteTokens != 1 || obs.ReasoningTokens != 1 {
		t.Fatalf("usage was not copied: %+v", obs)
	}
	if obs.Metadata["iteration"] != 2 || obs.Metadata["finished"] != true || obs.Metadata["tool_calls"] != 0 {
		t.Fatalf("metadata = %+v", obs.Metadata)
	}

	callErr := errors.New("upstream unavailable")
	failed := NewGenerationObservation(GenerationObservationParams{Context: ctx, Err: callErr, ErrorCode: "upstream_error"})
	if failed.Status != "error" || failed.Level != service.ObservationLevelError || failed.ErrorMessage != callErr.Error() {
		t.Fatalf("failed observation = %+v", failed)
	}
}

func TestToolObservationAndResult(t *testing.T) {
	governor := &stubGovernor{replacement: "short"}
	tool := service.ToolCall{ID: "call-1", Name: "lookup", Arguments: map[string]any{"q": "term"}}
	output, block := ToolResult(governor, "run-1", tool, "a long result")
	if output != "short" || block.Content != "short" || block.ToolUseID != "call-1" {
		t.Fatalf("tool result = %q %+v", output, block)
	}
	if governor.runID != "run-1" || governor.toolName != "lookup" {
		t.Fatalf("governor called with run=%q tool=%q", governor.runID, governor.toolName)
	}

	callErr := errors.New("failed")
	obs := NewToolObservation(ToolObservationParams{
		Context:             ObservationContext{Source: "chat", TraceID: "trace-1"},
		ParentObservationID: "generation-1",
		Tool:                tool, Output: output, Iteration: 3, Err: callErr,
	})
	if obs.Level != service.ObservationLevelError || obs.ParentObservationID != "generation-1" || !strings.Contains(obs.Input, `"q":"term"`) {
		t.Fatalf("tool observation = %+v", obs)
	}
}

func TestCallProvider(t *testing.T) {
	governor := &stubGovernor{windowed: []service.Message{{Role: "user", Content: "windowed"}}}
	provider := stubProvider{chat: func(_ context.Context, _ string, messages []service.Message, _ []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		if messages[0].Content != "windowed" {
			t.Fatalf("provider messages = %+v", messages)
		}
		return &service.LLMResponse{Content: "ok"}, nil
	}}

	resp, messages, _, err := CallProvider(context.Background(), governor, provider, "model", "agent", "task", []service.Message{{Role: "user", Content: "full"}}, nil)
	if err != nil || resp.Content != "ok" || messages[0].Content != "windowed" {
		t.Fatalf("CallProvider = resp=%+v messages=%+v err=%v", resp, messages, err)
	}
}

type stubGovernor struct {
	windowed    []service.Message
	replacement string
	runID       string
	toolName    string
}

func (g *stubGovernor) LimitWithTools(_ context.Context, _, _ string, messages []service.Message, _ []service.Tool) ([]service.Message, error) {
	if g.windowed != nil {
		return g.windowed, nil
	}
	return messages, nil
}

func (*stubGovernor) ChatOptions() *service.ChatOptions { return nil }

func (g *stubGovernor) TruncateToolResult(runID, toolName, _ string) (string, bool) {
	g.runID = runID
	g.toolName = toolName
	return g.replacement, true
}

type stubProvider struct {
	chat func(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error)
}

func (p stubProvider) Chat(ctx context.Context, model string, messages []service.Message, tools []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
	return p.chat(ctx, model, messages, tools, opts)
}
