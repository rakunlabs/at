package openai

import (
	"context"
	"net/http"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestApplyPromptCacheKey(t *testing.T) {
	ctx := service.ContextWithUpstreamSession(context.Background(), "ses_1")
	tests := []struct {
		name       string
		baseURL    string
		ctx        context.Context
		body       map[string]any
		wantKey    any
		wantHeader string
	}{
		{name: "openai", baseURL: "https://api.openai.com/v1/chat/completions", ctx: ctx, wantKey: "ses_1"},
		{name: "mistral", baseURL: "https://api.mistral.ai/v1/chat/completions", ctx: ctx, wantKey: "ses_1"},
		{name: "xai header", baseURL: "https://api.x.ai/v1/chat/completions", ctx: ctx, wantHeader: "ses_1"},
		{name: "groq rejects the field", baseURL: "https://api.groq.com/openai/v1/chat/completions", ctx: ctx},
		{name: "cerebras rejects the field", baseURL: "https://api.cerebras.ai/v1/chat/completions", ctx: ctx},
		{name: "local server", baseURL: "http://127.0.0.1:9090/v1/chat/completions", ctx: ctx},
		{name: "no session", baseURL: "https://api.openai.com/v1/chat/completions", ctx: context.Background()},
		{name: "explicit key wins", baseURL: "https://api.openai.com/v1/chat/completions", ctx: ctx, body: map[string]any{"prompt_cache_key": "mine"}, wantKey: "mine"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := tt.body
			if body == nil {
				body = map[string]any{}
			}
			header := http.Header{}
			applyPromptCacheKey(tt.ctx, tt.baseURL, body, header)
			if body["prompt_cache_key"] != tt.wantKey {
				t.Fatalf("prompt_cache_key = %#v, want %#v", body["prompt_cache_key"], tt.wantKey)
			}
			if got := header.Get("x-grok-conv-id"); got != tt.wantHeader {
				t.Fatalf("x-grok-conv-id = %q, want %q", got, tt.wantHeader)
			}
		})
	}
}
