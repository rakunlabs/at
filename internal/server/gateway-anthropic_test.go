package server

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/gateway/wire"
	"github.com/rakunlabs/at/internal/service"
)

func TestBuildAnthropicProviderMessagesSelectsCellByFamily(t *testing.T) {
	s := &Server{}
	req := decodeAnthropicRequest(t, `{
		"model":"m","max_tokens":100,
		"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}]}]
	}`)

	t.Run("anthropic family keeps native content blocks", func(t *testing.T) {
		messages, _ := s.buildAnthropicProviderMessages("anthropic", req)
		blocks, ok := messages[0].Content.([]service.ContentBlock)
		if !ok || blocks[0].Type != "image" || blocks[0].Source == nil {
			t.Fatalf("expected native blocks, got %#v", messages[0].Content)
		}
	})

	t.Run("bedrock is anthropic family", func(t *testing.T) {
		if !anthropicFamilyProvider("bedrock") || !anthropicFamilyProvider("minimax") {
			t.Fatal("bedrock and minimax consume the Anthropic block shape")
		}
		if anthropicFamilyProvider("openai") || anthropicFamilyProvider("gemini") {
			t.Fatal("openai and gemini are not anthropic family")
		}
	})

	t.Run("openai family produces the openai shape", func(t *testing.T) {
		messages, _ := s.buildAnthropicProviderMessages("openai", req)
		if _, ok := messages[0].Content.([]service.ContentBlock); ok {
			t.Fatal("an OpenAI target must not receive Anthropic content blocks")
		}
	})
}

func decodeAnthropicRequest(t *testing.T, body string) *wire.AnthropicMessagesRequest {
	t.Helper()

	var req wire.AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return &req
}
