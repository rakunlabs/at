package openai

import "testing"

func TestAPIURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		path    string
		want    string
	}{
		{
			"openai standard",
			"https://api.openai.com/v1/chat/completions",
			"/embeddings",
			"https://api.openai.com/v1/embeddings",
		},
		{
			"trailing slash",
			"https://api.openai.com/v1/chat/completions/",
			"/moderations",
			"https://api.openai.com/v1/moderations",
		},
		{
			"azure with api-version query",
			"https://res.openai.azure.com/openai/deployments/gpt4o/chat/completions?api-version=2024-06-01",
			"/embeddings",
			"https://res.openai.azure.com/openai/deployments/gpt4o/embeddings?api-version=2024-06-01",
		},
		{
			"generic base without chat suffix",
			"https://example.com/v1",
			"/images/generations",
			"https://example.com/v1/images/generations",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Provider{BaseURL: tt.baseURL}
			if got := p.apiURL(tt.path); got != tt.want {
				t.Errorf("apiURL(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
