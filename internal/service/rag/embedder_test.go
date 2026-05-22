package rag

import "testing"

func TestNormalizeEmbeddingAPIType(t *testing.T) {
	tests := []struct {
		name         string
		apiType      string
		providerType string
		want         string
	}{
		{name: "explicit openai", apiType: "openai", providerType: "gemini", want: "openai"},
		{name: "explicit gemini", apiType: "gemini", providerType: "openai", want: "gemini"},
		{name: "empty gemini provider", apiType: "", providerType: "gemini", want: "gemini"},
		{name: "auto gemini provider", apiType: "auto", providerType: "gemini", want: "gemini"},
		{name: "empty openai provider", apiType: "", providerType: "openai", want: "openai"},
		{name: "auto unknown provider", apiType: "auto", providerType: "anthropic", want: "openai"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeEmbeddingAPIType(tt.apiType, tt.providerType)
			if got != tt.want {
				t.Fatalf("NormalizeEmbeddingAPIType(%q, %q) = %q, want %q", tt.apiType, tt.providerType, got, tt.want)
			}
		})
	}
}

func TestNewATEmbedderClientAutoDefaultsToOpenAI(t *testing.T) {
	c, err := NewATEmbedderClient(ATEmbedderConfig{
		BaseURL: "https://example.test/v1/chat/completions",
		APIType: "auto",
		Model:   "text-embedding-3-small",
	})
	if err != nil {
		t.Fatalf("NewATEmbedderClient() error = %v", err)
	}

	if c.apiType != "openai" {
		t.Fatalf("apiType = %q, want openai", c.apiType)
	}
	if c.embeddingsURL != "https://example.test/v1/embeddings" {
		t.Fatalf("embeddingsURL = %q, want https://example.test/v1/embeddings", c.embeddingsURL)
	}
}

func TestNewATEmbedderClientGeminiGatewayURL(t *testing.T) {
	c, err := NewATEmbedderClient(ATEmbedderConfig{
		BaseURL: "https://at.example.test/gateway/v1/providers/google-ai",
		APIType: "gemini",
		Model:   "text-embedding-004",
	})
	if err != nil {
		t.Fatalf("NewATEmbedderClient() error = %v", err)
	}

	want := "https://at.example.test/gateway/v1/providers/google-ai/v1beta/models/text-embedding-004:batchEmbedContents"
	if c.embeddingsURL != want {
		t.Fatalf("embeddingsURL = %q, want %q", c.embeddingsURL, want)
	}
}
