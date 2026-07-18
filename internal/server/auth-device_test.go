package server

import (
	"context"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/openai"
)

type codexAuthTestProvider struct{}

func (codexAuthTestProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return &service.LLMResponse{}, nil
}

func TestSaveCodexAuthTokensPersistsCredentialsAndAccount(t *testing.T) {
	store := newFakeProviderStore()
	store.providers["chatgpt"] = &service.ProviderRecord{
		ID:  "provider-id",
		Key: "chatgpt",
		Config: config.LLMConfig{
			Type:         "openai",
			AuthType:     "chatgpt",
			Model:        "gpt-5.3-codex",
			ExtraHeaders: map[string]string{"Existing": "header"},
		},
	}
	s := &Server{
		store:     store,
		providers: make(map[string]ProviderInfo),
		providerFactory: func(config.LLMConfig) (service.LLMProvider, error) {
			return codexAuthTestProvider{}, nil
		},
	}
	expiresAt := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	if err := s.saveCodexAuthTokens("chatgpt", codexDeviceProviderSnapshot{ID: "provider-id", Type: "openai"}, &openai.CodexTokens{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "account-id",
		ExpiresAt:    expiresAt,
	}); err != nil {
		t.Fatalf("saveCodexAuthTokens: %v", err)
	}

	got := store.providers["chatgpt"].Config
	if got.APIKey != "access-token" || got.RefreshToken != "refresh-token" {
		t.Fatalf("tokens were not persisted: %+v", got)
	}
	if got.TokenExpiresAt != expiresAt.Format(time.RFC3339) {
		t.Fatalf("token_expires_at = %q", got.TokenExpiresAt)
	}
	if got.ExtraHeaders["ChatGPT-Account-ID"] != "account-id" || got.ExtraHeaders["Existing"] != "header" {
		t.Fatalf("extra headers = %#v", got.ExtraHeaders)
	}
	if _, ok := s.providers["chatgpt"]; !ok {
		t.Fatal("provider was not hot-reloaded")
	}
}

func TestSaveCodexAuthTokensRejectsChangedProvider(t *testing.T) {
	store := newFakeProviderStore()
	store.providers["chatgpt"] = &service.ProviderRecord{
		ID:  "replacement-id",
		Key: "chatgpt",
		Config: config.LLMConfig{
			Type:     "openai",
			AuthType: "",
		},
	}
	s := &Server{store: store}
	err := s.saveCodexAuthTokens("chatgpt", codexDeviceProviderSnapshot{ID: "original-id", Type: "openai"}, &openai.CodexTokens{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "account-id",
	})
	if err == nil {
		t.Fatal("expected changed provider to reject pending authorization")
	}
	if store.providers["chatgpt"].Config.APIKey != "" {
		t.Fatal("OAuth token was written to replacement provider")
	}
}

func TestSaveCodexAuthTokensRejectsChangedNetworkConfig(t *testing.T) {
	store := newFakeProviderStore()
	store.providers["chatgpt"] = &service.ProviderRecord{
		ID:  "provider-id",
		Key: "chatgpt",
		Config: config.LLMConfig{
			Type:     "openai",
			AuthType: "chatgpt",
			Proxy:    "https://unexpected-proxy.example",
		},
	}
	s := &Server{store: store}
	err := s.saveCodexAuthTokens("chatgpt", codexDeviceProviderSnapshot{ID: "provider-id", Type: "openai"}, &openai.CodexTokens{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		AccountID:    "account-id",
	})
	if err == nil {
		t.Fatal("expected changed network config to reject pending authorization")
	}
	if store.providers["chatgpt"].Config.APIKey != "" {
		t.Fatal("OAuth token was written after proxy changed")
	}
}
