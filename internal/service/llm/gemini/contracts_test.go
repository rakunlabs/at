package gemini

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestInstructionsAndAgentToolHistory(t *testing.T) {
	p := &Provider{}
	result := []service.ContentBlock{{Type: "tool_result", ToolUseID: "call_1", Content: "found"}}
	body := p.buildRequest(context.Background(), "gemini-2.5-flash", []service.Message{
		{Role: "system", Content: "first"}, {Role: "developer", Content: "second"},
		{Role: "assistant", Content: []service.ContentBlock{{Type: "tool_use", ID: "call_1", Name: "lookup"}}},
		{Role: "user", Content: result},
	}, nil, nil)
	if body.SystemInstruction == nil || len(body.SystemInstruction.Parts) != 2 {
		t.Fatalf("instructions lost: %+v", body.SystemInstruction)
	}
	if got := body.Contents[1].Parts[0].FunctionResponse.Name; got != "lookup" {
		t.Fatalf("tool name = %q", got)
	}
	if result[0].Name != "" {
		t.Fatal("caller history mutated")
	}
}

// Anthropic-shaped (block form) system content must not be silently dropped.
func TestBlockFormSystemInstruction(t *testing.T) {
	p := &Provider{}
	body := p.buildRequest(context.Background(), "gemini-2.5-flash", []service.Message{
		{Role: "system", Content: []service.ContentBlock{
			{Type: "text", Text: "rule one"},
			{Type: "text", Text: " rule two"},
		}},
		{Role: "user", Content: "go"},
	}, nil, nil)
	if body.SystemInstruction == nil || len(body.SystemInstruction.Parts) != 1 {
		t.Fatalf("block-form instructions dropped: %+v", body.SystemInstruction)
	}
	if got := body.SystemInstruction.Parts[0].Text; got != "rule one rule two" {
		t.Fatalf("instruction text = %q", got)
	}
}

// promptTokenCount already includes the cached prefix, while server-side tool
// input is reported outside it. Both have to land in the right bucket or
// grounded requests are billed short.
func TestUsageSplitsCacheAndToolUseInput(t *testing.T) {
	usage := geminiServiceUsage(&usageMetadata{
		PromptTokenCount:        100,
		CachedContentTokenCount: 80,
		ToolUsePromptTokenCount: 30,
		CandidatesTokenCount:    10,
		ThoughtsTokenCount:      5,
		TotalTokenCount:         145,
	})
	if usage.CacheReadTokens != 80 {
		t.Errorf("cache read = %d, want 80", usage.CacheReadTokens)
	}
	// 100 total prompt - 80 cached + 30 tool-use input.
	if usage.PromptTokens != 50 {
		t.Errorf("prompt = %d, want 50", usage.PromptTokens)
	}
	if usage.CompletionTokens != 15 || usage.ReasoningTokens != 5 {
		t.Errorf("completion = %d, reasoning = %d", usage.CompletionTokens, usage.ReasoningTokens)
	}
	if usage.TotalTokens != 145 {
		t.Errorf("total = %d, want 145", usage.TotalTokens)
	}
}

// When upstream omits the total, the derived total must still account for
// every billed bucket.
func TestUsageDerivedTotalIncludesToolUseInput(t *testing.T) {
	usage := geminiServiceUsage(&usageMetadata{
		PromptTokenCount:        100,
		CachedContentTokenCount: 80,
		ToolUsePromptTokenCount: 30,
		CandidatesTokenCount:    10,
	})
	if usage.TotalTokens != 50+10+80 {
		t.Errorf("derived total = %d, want %d", usage.TotalTokens, 50+10+80)
	}
}

func TestGatewayMultipartPreservesToolsAndFiles(t *testing.T) {
	p := &Provider{}
	parts := p.convertToParts(context.Background(), service.Message{Role: "assistant", Content: map[string]any{
		"content":    []any{map[string]any{"type": "text", "text": "read"}, map[string]any{"type": "file", "file": map[string]any{"file_data": "data:application/pdf;base64,cGRm"}}},
		"tool_calls": []any{map[string]any{"function": map[string]any{"name": "lookup", "arguments": "{}"}}},
	}})
	if len(parts) != 3 || parts[1].InlineData == nil || parts[1].InlineData.Data != "cGRm" || parts[2].FunctionCall == nil {
		t.Fatalf("parts lost: %+v", parts)
	}
}

func TestRemoteImageDoesNotReceiveProviderCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-Api-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Error("provider credentials sent to image host")
		}
		w.Header().Set("Content-Type", "image/png")
		fmt.Fprint(w, "image")
	}))
	defer srv.Close()
	p, err := New("private-api-key", "model", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.fetchImageAsInlineData(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
}

func TestStreamingUsageAfterFinish(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"ok\"}]},\"finishReason\":\"STOP\"}]}\n\n")
		fmt.Fprint(w, "data: {\"usageMetadata\":{\"promptTokenCount\":100,\"cachedContentTokenCount\":80,\"candidatesTokenCount\":10,\"thoughtsTokenCount\":5,\"totalTokenCount\":115}}\n\n")
	}))
	defer srv.Close()
	p, err := New("key", "model", srv.URL, "", false)
	if err != nil {
		t.Fatal(err)
	}
	ch, _, err := p.ChatStream(context.Background(), "", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var usage *service.Usage
	for c := range ch {
		if c.Error != nil {
			t.Error(c.Error)
		}
		if c.Usage != nil {
			usage = c.Usage
		}
	}
	if usage == nil || usage.TotalTokens != 115 || usage.CacheReadTokens != 80 || usage.PromptTokens != 20 {
		t.Fatalf("final usage lost: %+v", usage)
	}
}
