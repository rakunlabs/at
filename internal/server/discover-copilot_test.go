package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

// copilotCatalog mirrors the shape GET https://api.githubcopilot.com/models
// returns: chat and embedding entries side by side, repeated IDs for models
// published in several versions, and an entry whose org policy is unaccepted.
func copilotCatalog() map[string]any {
	return map[string]any{
		"object": "list",
		"data": []map[string]any{
			{"id": "gpt-4.1", "capabilities": map[string]any{"type": "chat"}},
			{"id": "gpt-4.1", "capabilities": map[string]any{"type": "chat"}},
			{"id": "text-embedding-3-small", "capabilities": map[string]any{"type": "embeddings"}},
			{"id": "claude-sonnet-4.5", "capabilities": map[string]any{"type": "chat"}, "policy": map[string]any{"state": "unconfigured"}},
			{"id": ""},
		},
	}
}

func TestFetchCopilotModels(t *testing.T) {
	var gotPath, gotQuery string
	var gotHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotHeader = r.URL.Path, r.URL.RawQuery, r.Header.Clone()
		gotHeader.Set("User-Agent", r.UserAgent())
		_ = json.NewEncoder(w).Encode(copilotCatalog())
	}))
	defer server.Close()

	cfg := config.LLMConfig{
		Type:     "openai",
		AuthType: "copilot",
		APIKey:   "github-oauth-token",
		// The preset base URL carries the chat endpoint's api-version.
		BaseURL:      server.URL + "/chat/completions?api-version=2025-04-01",
		ExtraHeaders: map[string]string{"Accept": "application/vnd.github+json", "Editor-Version": "custom/1.0"},
	}

	models, err := fetchCopilotModels(context.Background(), cfg, "copilot-jwt", copilotChatCapability)
	if err != nil {
		t.Fatalf("fetchCopilotModels: %v", err)
	}
	// Duplicates collapse, embeddings are excluded, unaccepted policy is kept.
	if len(models) != 2 || models[0] != "gpt-4.1" || models[1] != "claude-sonnet-4.5" {
		t.Fatalf("chat models = %#v", models)
	}
	if gotPath != "/models" || gotQuery != "" {
		t.Fatalf("catalog URL = %q?%q, want /models with no query", gotPath, gotQuery)
	}
	// The exchanged JWT authenticates the catalog, never the stored GitHub token.
	if got := gotHeader.Get("Authorization"); got != "Bearer copilot-jwt" {
		t.Fatalf("Authorization = %q", got)
	}
	if got := gotHeader.Get("Copilot-Integration-Id"); got != "vscode-chat" {
		t.Fatalf("Copilot-Integration-Id = %q, want the editor default", got)
	}
	if got := gotHeader.Get("User-Agent"); got != "GithubCopilot/1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
	// Configured extra headers win over the defaults.
	if got := gotHeader.Get("Editor-Version"); got != "custom/1.0" {
		t.Fatalf("Editor-Version = %q, want the configured override", got)
	}
	if got := gotHeader.Get("Accept"); got != "application/vnd.github+json" {
		t.Fatalf("Accept = %q", got)
	}

	embeddings, err := fetchCopilotModels(context.Background(), cfg, "copilot-jwt", copilotEmbeddingCapability)
	if err != nil {
		t.Fatalf("fetchCopilotModels(embeddings): %v", err)
	}
	if len(embeddings) != 1 || embeddings[0] != "text-embedding-3-small" {
		t.Fatalf("embedding models = %#v", embeddings)
	}
}

func TestFetchCopilotModelsErrors(t *testing.T) {
	t.Run("upstream error is reported", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}))
		defer server.Close()

		_, err := fetchCopilotModels(context.Background(), config.LLMConfig{BaseURL: server.URL + "/chat/completions"}, "jwt", copilotChatCapability)
		if err == nil || !strings.Contains(err.Error(), "401") {
			t.Fatalf("error = %v, want the upstream status", err)
		}
	})

	t.Run("empty catalog is an error, not an empty list", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
		}))
		defer server.Close()

		_, err := fetchCopilotModels(context.Background(), config.LLMConfig{BaseURL: server.URL + "/chat/completions"}, "jwt", copilotChatCapability)
		if err == nil || !strings.Contains(err.Error(), "no chat models") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("unauthorized provider is not asked for a token", func(t *testing.T) {
		_, err := discoverCopilotModels(context.Background(), config.LLMConfig{Type: "openai", AuthType: "copilot"}, copilotChatCapability)
		if err == nil || !strings.Contains(err.Error(), "not authorized") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCopilotModelsURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{"preset chat URL", "https://api.githubcopilot.com/chat/completions?api-version=2025-04-01", "https://api.githubcopilot.com/models"},
		{"empty falls back to the Copilot API", "", "https://api.githubcopilot.com/models"},
		{"relay prefix is preserved", "https://relay.example.com/copilot/chat/completions", "https://relay.example.com/copilot/models"},
		{"root URL", "https://api.githubcopilot.com", "https://api.githubcopilot.com/models"},
		{"trailing slash", "https://api.githubcopilot.com/", "https://api.githubcopilot.com/models"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := copilotModelsURL(tt.baseURL)
			if err != nil {
				t.Fatalf("copilotModelsURL: %v", err)
			}
			if got != tt.want {
				t.Fatalf("copilotModelsURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestIsCopilotConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.LLMConfig
		want bool
	}{
		{"auth type", config.LLMConfig{Type: "openai", AuthType: "copilot"}, true},
		{"copilot base URL without auth type", config.LLMConfig{Type: "openai", BaseURL: "https://api.githubcopilot.com/chat/completions"}, true},
		{"plain openai", config.LLMConfig{Type: "openai", BaseURL: "https://api.openai.com/v1/chat/completions"}, false},
		{"no base URL", config.LLMConfig{Type: "openai"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCopilotConfig(tt.cfg); got != tt.want {
				t.Fatalf("isCopilotConfig = %v, want %v", got, tt.want)
			}
		})
	}
}
