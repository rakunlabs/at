package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestCodexGenerateImage(t *testing.T) {
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/backend-api/codex/images/generations" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-ID") != "account" {
			t.Errorf("missing auth headers: %#v", r.Header)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"created":1,"data":[{"b64_json":"aW1n"}],"output_format":"png","usage":{"input_tokens":17,"output_tokens":1372,"total_tokens":1389}}`))
	}))
	defer server.Close()

	provider := NewCodexProvider("gpt-6.1-sol", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil),
		WithCodexBaseURL(server.URL+"/backend-api/codex/responses"),
		WithCodexHTTPClient(server.Client()),
	)
	resp, err := provider.GenerateImage(context.Background(), service.ImageGenerateRequest{Prompt: "a red fox", Quality: "hd", Background: "transparent"})
	if err != nil {
		t.Fatalf("GenerateImage: %v", err)
	}
	if body["model"] != CodexDefaultImageModel || body["prompt"] != "a red fox" || body["quality"] != "high" || body["background"] != "transparent" {
		t.Fatalf("request body = %#v", body)
	}
	if _, ok := body["response_format"]; ok {
		t.Fatal("response_format must not be sent to the Codex images endpoint")
	}
	if len(resp.Images) != 1 || resp.Images[0].Base64 != "aW1n" {
		t.Fatalf("images = %#v", resp.Images)
	}
	if resp.Usage.PromptTokens != 17 || resp.Usage.CompletionTokens != 1372 {
		t.Fatalf("usage = %#v", resp.Usage)
	}
}

func TestCodexGenerateImageErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "upstream_error", status: http.StatusForbidden, body: `{"error":{"message":"image generation is not available on this plan"}}`},
		{name: "rate_limited", status: http.StatusTooManyRequests, body: `{"error":{"message":"usage limit reached"}}`},
		{name: "empty", status: http.StatusOK, body: `{"data":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			provider := NewCodexProvider("", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil),
				WithCodexBaseURL(server.URL+"/backend-api/codex/responses"),
				WithCodexHTTPClient(server.Client()),
			)
			_, err := provider.GenerateImage(context.Background(), service.ImageGenerateRequest{Prompt: "x"})
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.status == http.StatusTooManyRequests {
				if _, ok := err.(*service.RateLimitError); !ok {
					t.Fatalf("want RateLimitError, got %T", err)
				}
			}
		})
	}
}
