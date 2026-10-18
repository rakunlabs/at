package server

import (
	"net/url"
	"testing"
)

func TestOpencodeModelsPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://opencode.ai/inference/go/openai/v1/models", "https://opencode.ai/inference/go/v1/models"},
		{"https://opencode.ai/inference/go/anthropic/v1/models", "https://opencode.ai/inference/go/v1/models"},
		{"https://opencode.ai/zen/go/v1/models", "https://opencode.ai/zen/go/v1/models"},
		{"https://example.com/inference/go/openai/v1/models", "https://example.com/inference/go/openai/v1/models"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			u, err := url.Parse(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			opencodeModelsPath(u)
			if u.String() != tt.want {
				t.Fatalf("got %s, want %s", u, tt.want)
			}
		})
	}
}
