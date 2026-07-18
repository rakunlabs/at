package main

import (
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service/llm/openai"
)

func TestNewProviderCreatesUnauthenticatedChatGPTCodexProvider(t *testing.T) {
	provider, err := newProvider(config.LLMConfig{
		Type:     "openai",
		AuthType: "chatgpt",
		Model:    "gpt-5.3-codex",
	})
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	if _, ok := provider.(*openai.CodexProvider); !ok {
		t.Fatalf("provider type = %T, want *openai.CodexProvider", provider)
	}
}
