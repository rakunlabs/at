package agentloop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/bedrock"
	"github.com/rakunlabs/at/internal/service/llm/cohere"
	"github.com/rakunlabs/at/internal/service/llm/gemini"
	"github.com/rakunlabs/at/internal/service/llm/minimax"
	"github.com/rakunlabs/at/internal/service/llm/openai"
	"github.com/rakunlabs/at/internal/service/llm/vertex"
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

	resp, messages, _, err := CallProvider(context.Background(), governor, provider, "model", "agent", "task", []service.Message{{Role: "user", Content: "full"}}, nil, "")
	if err != nil || resp.Content != "ok" || messages[0].Content != "windowed" {
		t.Fatalf("CallProvider = resp=%+v messages=%+v err=%v", resp, messages, err)
	}
}

type stubGovernor struct {
	opts        *service.ChatOptions
	windowed    []service.Message
	replacement string
	runID       string
	toolName    string
}

func TestCallProviderReasoningEffort(t *testing.T) {
	temperature := 0.7
	for _, tt := range []struct {
		name     string
		effort   string
		governor *stubGovernor
		wantErr  bool
	}{
		{name: "no governor"},
		{name: "nil governor options", governor: &stubGovernor{}},
		{name: "existing options", governor: &stubGovernor{opts: &service.ChatOptions{Temperature: &temperature}}},
		{name: "effort without governor", effort: "medium"},
		{name: "effort with nil options", effort: "high", governor: &stubGovernor{}},
		{name: "copy options", effort: "xhigh", governor: &stubGovernor{opts: &service.ChatOptions{Temperature: &temperature}}},
		{name: "invalid", effort: "HIGH", wantErr: true},
		{name: "whitespace", effort: " high", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			provider := stubProvider{chat: func(_ context.Context, _ string, _ []service.Message, _ []service.Tool, opts *service.ChatOptions) (*service.LLMResponse, error) {
				calls++
				if tt.wantErr {
					t.Fatal("invalid effort reached Chat")
				}
				var original *service.ChatOptions
				if tt.governor != nil {
					original = tt.governor.opts
				}
				if tt.effort == "" {
					if opts != original {
						t.Fatalf("default opts = %p, want %p", opts, original)
					}
				} else {
					if opts == nil || opts.ReasoningEffort != tt.effort || opts.MaxTokens != nil || opts.MaxCompletionTokens != nil {
						t.Fatalf("effort opts = %+v", opts)
					}
					if original != nil && (opts == original || opts.Temperature != original.Temperature) {
						t.Fatal("options were not shallow copied")
					}
				}
				return &service.LLMResponse{Finished: true}, nil
			}}
			var governor CallGovernor
			if tt.governor != nil {
				governor = tt.governor
			}
			_, _, _, err := CallProvider(context.Background(), governor, provider, "model", "agent", "task", nil, nil, tt.effort)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v", err)
			}
			if !tt.wantErr && calls != 1 {
				t.Fatalf("calls = %d", calls)
			}
			if tt.governor != nil && tt.governor.opts != nil && tt.governor.opts.ReasoningEffort != "" {
				t.Fatal("mutated governor options")
			}
		})
	}
}

func TestProviderReasoningValidators(t *testing.T) {
	for _, tt := range []struct {
		name      string
		provider  service.LLMProvider
		maxEffort string
	}{
		{"openai", &openai.Provider{}, "xhigh"},
		{"codex", &openai.CodexProvider{}, "xhigh"},
		{"vertex", &vertex.Provider{}, "xhigh"},
		{"anthropic", &antropic.Provider{}, "high"},
		{"gemini", &gemini.Provider{}, "high"},
		{"minimax", &minimax.Provider{}, "high"},
		{"bedrock", &bedrock.Provider{}, ""},
		{"cohere", &cohere.Provider{}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			validator, ok := tt.provider.(service.ReasoningEffortValidator)
			if !ok {
				t.Fatal("missing validator")
			}
			for _, effort := range []string{"", "low", "medium", "high", "xhigh", "invalid"} {
				wantErr := effort == "invalid" || (effort != "" && (tt.maxEffort == "" || (effort == "xhigh" && tt.maxEffort != "xhigh")))
				if err := validator.ValidateReasoningEffort(effort); (err != nil) != wantErr {
					t.Fatalf("effort %q: %v", effort, err)
				}
				if wantErr {
					// Zero-value adapters would fail if Chat were reached.
					if _, _, _, err := CallProvider(context.Background(), nil, tt.provider, "model", "", "", nil, nil, effort); err == nil {
						t.Fatalf("effort %q reached Chat", effort)
					}
				}
			}
		})
	}
}

func TestGenerationReasoningEffort(t *testing.T) {
	for _, effort := range []string{"", "medium", "xhigh"} {
		for _, callErr := range []error{nil, errors.New("upstream rejected")} {
			obs := NewGenerationObservation(GenerationObservationParams{Context: ObservationContext{Model: "model", ReasoningEffort: effort}, Err: callErr})
			var body map[string]any
			if err := json.Unmarshal([]byte(obs.RequestBody), &body); err != nil {
				t.Fatal(err)
			}
			value, exists := body["reasoning_effort"]
			if exists != (effort != "") || (exists && value != effort) {
				t.Fatalf("effort %q: body %s", effort, obs.RequestBody)
			}
		}
	}
}

func (g *stubGovernor) LimitWithTools(_ context.Context, _, _ string, messages []service.Message, _ []service.Tool) ([]service.Message, error) {
	if g.windowed != nil {
		return g.windowed, nil
	}
	return messages, nil
}

func (g *stubGovernor) ChatOptions() *service.ChatOptions { return g.opts }

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
