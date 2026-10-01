package server

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/gateway/wire"
)

func TestBuildProviderMessagesPreservesSystemInstructions(t *testing.T) {
	msgs := []wire.OpenAIMessage{
		{Role: "system", Content: json.RawMessage(`"First instruction"`)},
		{Role: "developer", Content: json.RawMessage(`[{"type":"text","text":"Second instruction"}]`)},
		{Role: "system", Content: json.RawMessage(`""`)},
		{Role: "user", Content: json.RawMessage(`"Hello"`)},
	}
	for _, provider := range []string{"anthropic", "minimax", "bedrock"} {
		t.Run(provider, func(t *testing.T) {
			messages, _ := (&Server{}).buildProviderMessages(provider, msgs, nil)
			if len(messages) != 2 || messages[0].Role != "system" || messages[0].Content != "First instruction\n\nSecond instruction" {
				t.Fatalf("system instructions lost: %+v", messages)
			}
			if messages[1].Role != "user" || messages[1].Content != "Hello" {
				t.Fatalf("user message changed: %+v", messages[1])
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
