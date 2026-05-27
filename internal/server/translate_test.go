package server

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestBuildChatOptions_ToolChoice(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want any
	}{
		{"string auto", `"auto"`, "auto"},
		{"string required", `"required"`, "required"},
		{"string none", `"none"`, "none"},
		{"function object", `{"type":"function","function":{"name":"foo"}}`, map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "foo"},
		}},
		{"empty string drops", `""`, nil},
		{"unset drops", ``, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &ChatCompletionRequest{}
			if tt.raw != "" {
				req.ToolChoice = json.RawMessage(tt.raw)
			}
			opts := buildChatOptions(req)
			var got any
			if opts != nil {
				got = opts.ToolChoice
			}
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tt.want)
			if string(gotJSON) != string(wantJSON) {
				t.Errorf("tool_choice: got %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestBuildChatOptions_NewFields(t *testing.T) {
	tr := true
	n := 3
	pp := 0.5
	fp := -0.5
	tl := 5
	logp := true
	store := false
	req := &ChatCompletionRequest{
		ParallelToolCalls: &tr,
		N:                 &n,
		PresencePenalty:   &pp,
		FrequencyPenalty:  &fp,
		LogitBias:         map[string]int{"123": -100},
		User:              "alice",
		Logprobs:          &logp,
		TopLogprobs:       &tl,
		Store:             &store,
		Metadata:          map[string]any{"trace": "x"},
		ServiceTier:       "default",
	}
	opts := buildChatOptions(req)
	if opts == nil {
		t.Fatal("opts is nil")
	}
	if opts.ParallelToolCalls == nil || *opts.ParallelToolCalls != true {
		t.Error("parallel_tool_calls not forwarded")
	}
	if opts.N == nil || *opts.N != 3 {
		t.Error("n not forwarded")
	}
	if opts.PresencePenalty == nil || *opts.PresencePenalty != 0.5 {
		t.Error("presence_penalty not forwarded")
	}
	if opts.FrequencyPenalty == nil || *opts.FrequencyPenalty != -0.5 {
		t.Error("frequency_penalty not forwarded")
	}
	if opts.LogitBias["123"] != -100 {
		t.Error("logit_bias not forwarded")
	}
	if opts.User != "alice" {
		t.Error("user not forwarded")
	}
	if opts.Logprobs == nil || *opts.Logprobs != true {
		t.Error("logprobs not forwarded")
	}
	if opts.TopLogprobs == nil || *opts.TopLogprobs != 5 {
		t.Error("top_logprobs not forwarded")
	}
	if opts.Store == nil || *opts.Store != false {
		t.Error("store not forwarded")
	}
	if opts.Metadata["trace"] != "x" {
		t.Error("metadata not forwarded")
	}
	if opts.ServiceTier != "default" {
		t.Error("service_tier not forwarded")
	}
}

func TestBuildOpenAIResponse_CreatedAndSystemFingerprint(t *testing.T) {
	resp := &service.LLMResponse{
		Content:           "hi",
		Finished:          true,
		SystemFingerprint: "fp_abc",
		FinishReason:      "stop",
		Usage: service.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
			ReasoningTokens:  2,
			CacheReadTokens:  3,
		},
	}
	out := buildOpenAIResponse("chatcmpl-1", "openai/gpt-4o", resp)
	if out.Created == 0 {
		t.Error("Created should be set")
	}
	if out.SystemFingerprint != "fp_abc" {
		t.Errorf("SystemFingerprint: got %q", out.SystemFingerprint)
	}
	if out.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason: got %q", out.Choices[0].FinishReason)
	}
	if out.Usage.CompletionTokensDetails == nil || out.Usage.CompletionTokensDetails.ReasoningTokens != 2 {
		t.Error("reasoning_tokens not in completion_tokens_details")
	}
	if out.Usage.PromptTokensDetails == nil || out.Usage.PromptTokensDetails.CachedTokens != 3 {
		t.Error("cached_tokens not in prompt_tokens_details")
	}
}

func TestNormalizeFinishReason(t *testing.T) {
	tests := []struct {
		name string
		resp *service.LLMResponse
		want string
	}{
		{"openai stop", &service.LLMResponse{FinishReason: "stop", Finished: true}, "stop"},
		{"openai length", &service.LLMResponse{FinishReason: "length"}, "length"},
		{"anthropic end_turn", &service.LLMResponse{FinishReason: "end_turn", Finished: true}, "stop"},
		{"anthropic max_tokens", &service.LLMResponse{FinishReason: "max_tokens"}, "length"},
		{"anthropic tool_use", &service.LLMResponse{FinishReason: "tool_use"}, "tool_calls"},
		{"gemini SAFETY", &service.LLMResponse{FinishReason: "safety"}, "content_filter"},
		{"openai tool_calls", &service.LLMResponse{FinishReason: "tool_calls"}, "tool_calls"},
		{"empty + tool calls", &service.LLMResponse{
			ToolCalls: []service.ToolCall{{ID: "x"}},
		}, "tool_calls"},
		{"empty + finished", &service.LLMResponse{Finished: true}, "stop"},
		{"unknown defaults to stop", &service.LLMResponse{FinishReason: "weird", Finished: true}, "stop"},
		{"unknown with tool calls", &service.LLMResponse{
			FinishReason: "weird",
			ToolCalls:    []service.ToolCall{{ID: "x"}},
		}, "tool_calls"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeFinishReason(tt.resp); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMapStreamFinishReason(t *testing.T) {
	tests := []struct {
		raw      string
		toolCalls bool
		want     string
	}{
		{"stop", false, "stop"},
		{"length", false, "length"},
		{"max_tokens", false, "length"},
		{"end_turn", false, "stop"},
		{"tool_use", false, "tool_calls"},
		{"safety", false, "content_filter"},
		{"", true, "tool_calls"},
		{"", false, "stop"},
		{"weird", true, "tool_calls"},
		{"weird", false, "stop"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := mapStreamFinishReason(tt.raw, tt.toolCalls); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseEmbeddingsInput(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{"single string", `"hello"`, []string{"hello"}, false},
		{"array", `["a","b","c"]`, []string{"a", "b", "c"}, false},
		{"empty string errors", `""`, nil, true},
		{"empty raw errors", ``, nil, true},
		{"token id array rejected", `[1,2,3]`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw json.RawMessage
			if tt.raw != "" {
				raw = json.RawMessage(tt.raw)
			}
			got, err := parseEmbeddingsInput(raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err: got %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Fatalf("len: got %d, want %d", len(got), len(tt.want))
				}
				for i := range got {
					if got[i] != tt.want[i] {
						t.Errorf("[%d]: got %q, want %q", i, got[i], tt.want[i])
					}
				}
			}
		})
	}
}
