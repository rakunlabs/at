package antropic

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestRequestMaxTokens(t *testing.T) {
	maxTokens, maxCompletionTokens := 6000, 7000
	tests := []struct {
		name     string
		model    string
		provider int
		opts     *service.ChatOptions
		want     int
	}{
		{name: "shorts sonnet", model: "claude-sonnet-5", want: 32000},
		{name: "sonnet 4", model: "claude-sonnet-4-6", want: 32000},
		{name: "opus 4", model: "claude-opus-4-20250514", want: 32000},
		{name: "haiku 4", model: "claude-haiku-4-5-20251001", want: 32000},
		{name: "sonnet 3.7", model: "claude-3-7-sonnet-latest", want: 32000},
		{name: "legacy 3.5", model: "claude-3-5-sonnet-20241022", want: 8192},
		{name: "legacy haiku 3.5", model: "claude-3-5-haiku-latest", want: 8192},
		{name: "legacy opus", model: "claude-3-opus-20240229", want: 4096},
		{name: "legacy sonnet", model: "claude-3-sonnet-20240229", want: 4096},
		{name: "legacy haiku", model: "claude-3-haiku-20240307", want: 4096},
		{name: "legacy 2", model: "claude-2.1", want: 4096},
		{name: "unknown compatible model", model: "custom-model", want: 32000},
		{name: "provider override", model: "claude-sonnet-5", provider: 16384, want: 16384},
		{name: "nonpositive provider", model: "claude-sonnet-5", provider: -1, want: 32000},
		{name: "request override", model: "claude-sonnet-5", provider: 16384, opts: &service.ChatOptions{MaxTokens: &maxTokens}, want: 6000},
		{name: "completion takes precedence", model: "claude-sonnet-5", opts: &service.ChatOptions{MaxTokens: &maxTokens, MaxCompletionTokens: &maxCompletionTokens}, want: 7000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A different constructor model verifies per-request resolution.
			p, err := New("test-key", "claude-3-haiku-20240307", "", "", false, WithMaxTokens(tt.provider))
			if err != nil {
				t.Fatal(err)
			}
			body := p.buildRequestBody(tt.model, []service.Message{{Role: "user", Content: "Write a script"}}, nil, tt.opts)
			if got := body["max_tokens"]; got != tt.want {
				t.Fatalf("max_tokens = %v, want %d", got, tt.want)
			}
		})
	}
}

func TestOAuthDefaultMaxTokens(t *testing.T) {
	p, err := New("", "claude-sonnet-5", "", "", false, WithTokenSource(NewStaticTokenSource("test-oauth")))
	if err != nil {
		t.Fatal(err)
	}
	// Match the Shorts agent path: OAuth provider, no explicit output limit,
	// and a tool used to write the script artifact.
	body := p.buildRequestBody(p.Model, []service.Message{{Role: "user", Content: "Write script.json"}}, []service.Tool{{
		Name: "bash_execute",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"command": map[string]any{"type": "string"}},
		},
	}}, nil)
	if got := body["max_tokens"]; got != 32000 {
		t.Fatalf("OAuth max_tokens = %v, want 32000", got)
	}
}
