package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func TestDiscoverChatGPTModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("client_version") == "" {
			t.Errorf("request URL = %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer access-token" || r.Header.Get("ChatGPT-Account-ID") != "account-id" {
			t.Errorf("auth headers = %#v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{{"slug": "gpt-codex-a"}, {"slug": "gpt-codex-b"}},
		})
	}))
	defer server.Close()

	models, err := discoverOpenAIModels(context.Background(), config.LLMConfig{
		Type:     "openai",
		AuthType: "chatgpt",
		APIKey:   "access-token",
		BaseURL:  server.URL + "/backend-api/codex/responses",
		ExtraHeaders: map[string]string{
			"ChatGPT-Account-ID": "account-id",
		},
	})
	if err != nil {
		t.Fatalf("discoverOpenAIModels: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-codex-a" || models[1] != "gpt-codex-b" {
		t.Fatalf("models = %#v", models)
	}
}
