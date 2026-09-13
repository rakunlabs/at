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
	body := p.buildRequest(context.Background(), []service.Message{
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
