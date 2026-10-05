package cohere

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestThinkingRequestAndResponse(t *testing.T) {
	for _, tt := range []struct {
		name, mode         string
		budget, wantBudget int
	}{
		{"default", "", 0, 0},
		{"enabled", "enabled", 500, 500},
		{"unlimited", "enabled", 0, 0},
		{"disabled", "disabled", 500, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body chatRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				if tt.mode == "" {
					if body.Thinking != nil {
						t.Errorf("unexpected thinking: %+v", body.Thinking)
					}
				} else if body.Thinking == nil || body.Thinking.Type != tt.mode || body.Thinking.TokenBudget != tt.wantBudget {
					t.Errorf("thinking = %+v", body.Thinking)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":[{"type":"thinking","thinking":"Check the data."},{"type":"text","text":"Done."}]},"finish_reason":"COMPLETE","usage":{"tokens":{"input_tokens":10,"output_tokens":20}}}`))
			}))
			defer server.Close()
			p, err := New("test-key", "command-a-reasoning-08-2025", server.URL, "", false)
			if err != nil {
				t.Fatal(err)
			}
			opts := &service.ChatOptions{}
			if tt.mode != "" {
				opts.Thinking = &service.ThinkingConfig{Type: tt.mode, BudgetTokens: tt.budget}
			}
			resp, err := p.Chat(context.Background(), "", []service.Message{{Role: "user", Content: "hello"}}, nil, opts)
			if err != nil {
				t.Fatal(err)
			}
			if resp.Content != "Done." || resp.ReasoningContent != "Check the data." || resp.Usage.TotalTokens != 30 {
				t.Fatalf("response = %+v", resp)
			}
		})
	}
}

func TestThinkingHistoryPreserved(t *testing.T) {
	for _, content := range []any{
		[]service.ContentBlock{{Type: "thinking", Thinking: "Check."}, {Type: "text", Text: "Answer."}, {Type: "tool_use", ID: "call_1", Name: "ping"}},
		map[string]any{"content": "Answer.", "reasoning_content": "Check.", "tool_calls": []any{map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "ping", "arguments": "{}"}}}},
	} {
		msgs := translateMessagesToCohere([]service.Message{{Role: "assistant", Content: content}})
		if len(msgs) != 1 || len(msgs[0].ToolCalls) != 1 {
			t.Fatalf("messages = %+v", msgs)
		}
		blocks, ok := msgs[0].Content.([]contentBlock)
		if !ok || len(blocks) != 2 || blocks[0].Thinking != "Check." || blocks[1].Text != "Answer." {
			t.Fatalf("thinking history lost: %+v", msgs[0].Content)
		}
	}
}
