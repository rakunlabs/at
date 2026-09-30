package server

import (
	"encoding/json"
	"testing"
)

func TestDecideToolCallsDecisionProvider(t *testing.T) {
	provider := &decisionCaptureProvider{}
	s := &Server{providers: map[string]ProviderInfo{
		"laya": {provider: provider, providerType: "systemone", defaultModel: "auto"},
	}}

	raw, err := s.execDecide(t.Context(), map[string]any{
		"provider":       "laya/multilingual",
		"state":          "iki kez ücret alındı",
		"questions":      `{"dept":{"type":"choice","criteria":{"billing":"refunds","tech":"bugs"}}}`,
		"min_confidence": 0.95,
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.req.Model != "multilingual" {
		t.Fatalf("model = %q", provider.req.Model)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	low, _ := out["low_confidence"].([]any)
	if len(low) != 1 || low[0] != "dept" {
		t.Fatalf("low_confidence = %v", out["low_confidence"])
	}
}

func TestDecideToolRejectsChatProvider(t *testing.T) {
	s := &Server{providers: map[string]ProviderInfo{"openai": {provider: &embeddingCaptureProvider{}, providerType: "openai"}}}
	_, err := s.execDecide(t.Context(), map[string]any{
		"provider": "openai", "state": "x", "questions": map[string]any{"q": map[string]any{"type": "noul"}},
	})
	if err == nil {
		t.Fatal("want error for provider without decisions")
	}
}
