package service

import (
	"net/url"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func TestFreeTierCatalogAndPolicy(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range FreeTierCatalog {
		if seen[e.ID] || !IsSupportedProviderType(e.Type) {
			t.Fatalf("invalid catalog entry: %q", e.ID)
		}
		seen[e.ID] = true
		for _, value := range []string{e.URL, e.Source, e.Signup} {
			u, err := url.Parse(value)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				t.Fatalf("unsafe catalog URL: %q", value)
			}
		}
		if e.Privacy == "" || e.Note == "" {
			t.Fatalf("missing eligibility/privacy: %q", e.ID)
		}
	}
	for _, tt := range []struct {
		name   string
		policy ProviderUsePolicy
		cfg    config.LLMConfig
		want   bool
	}{
		{"default", ProviderUsePolicy{}, config.LLMConfig{Type: "openai"}, true},
		{"marked", ProviderUsePolicy{BlockFreeTier: true}, config.LLMConfig{FreeTier: "custom"}, false},
		{"unmarked known host", ProviderUsePolicy{BlockFreeTier: true}, config.LLMConfig{BaseURL: "https://API.GROQ.COM/openai/v1"}, false},
		{"default Gemini host", ProviderUsePolicy{BlockFreeTier: true}, config.LLMConfig{Type: "gemini"}, false},
		{"terminal DNS dot", ProviderUsePolicy{BlockFreeTier: true}, config.LLMConfig{BaseURL: "https://api.groq.com./openai/v1"}, false},
		{"empty allowlist", ProviderUsePolicy{AllowedPersonalHosts: []string{}}, config.LLMConfig{BaseURL: "https://example.com"}, false},
		{"host suffix is not match", ProviderUsePolicy{AllowedPersonalHosts: []string{"example.com"}}, config.LLMConfig{BaseURL: "https://example.com.evil.test"}, false},
		{"approved", ProviderUsePolicy{AllowedPersonalHosts: []string{"example.com"}}, config.LLMConfig{BaseURL: "https://example.com/v1"}, true},
		{"proxy", ProviderUsePolicy{AllowedPersonalHosts: []string{"example.com"}}, config.LLMConfig{BaseURL: "https://example.com", Proxy: "http://elsewhere"}, false},
		{"TLS opt-out", ProviderUsePolicy{AllowedPersonalHosts: []string{"example.com"}}, config.LLMConfig{BaseURL: "https://example.com", InsecureSkipVerify: true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.policy.Admits(tt.cfg); got != tt.want {
				t.Fatalf("Admits=%v want %v", got, tt.want)
			}
		})
	}
}

func TestFreeTierCatalogGeminiBaseURLUnversioned(t *testing.T) {
	entry, ok := FreeTierByID("google-ai")
	if !ok {
		t.Fatal("google-ai entry missing")
	}
	if strings.Contains(entry.URL, "/v1beta") {
		t.Fatalf("gemini adapter appends /v1beta itself; catalog URL %q must not include it", entry.URL)
	}
}
