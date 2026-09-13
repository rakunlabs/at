package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func TestDiscoverEmbeddingModelsAuth(t *testing.T) {
	for _, authType := range []string{"", "chatgpt"} {
		t.Run("auth="+authType, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if authType == "chatgpt" {
					t.Error("Codex embedding discovery should not call upstream")
				}
				if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer key" {
					t.Errorf("unexpected model discovery request: %s", r.URL)
				}
				_, _ = w.Write([]byte(`{"data":[{"id":"gpt-chat"},{"id":"text-embedding-3-small"},{"id":"text-embedding-3-large"}]}`))
			}))
			defer upstream.Close()
			body, _ := json.Marshal(discoverRequest{Config: config.LLMConfig{
				Type: "openai", AuthType: authType, APIKey: "key", BaseURL: upstream.URL + "/v1/chat/completions",
			}})
			r := httptest.NewRequest(http.MethodPost, "/api/v1/providers/discover-embedding-models", strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			(&Server{}).DiscoverEmbeddingModelsAPI(w, r)
			if authType == "chatgpt" {
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "does not support embeddings") {
					t.Fatalf("Codex response: %d %s", w.Code, w.Body.String())
				}
				return
			}
			var result discoverResponse
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil || len(result.Models) != 2 || result.Models[0] != "text-embedding-3-small" || result.Models[1] != "text-embedding-3-large" {
				t.Fatalf("OpenAI embedding discovery: %d %s", w.Code, w.Body.String())
			}
		})
	}
}
