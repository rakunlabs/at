package server

import (
	"errors"
	"slices"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func TestAdmitChainInputs(t *testing.T) {
	claude := ProviderInfo{providerType: "anthropic"}
	gemini := ProviderInfo{providerType: "gemini"}
	unknown := ProviderInfo{providerType: "openai"}
	textOnly := ProviderInfo{providerType: "openai", modelCapabilities: map[string]config.ModelCapability{"llama": {InputModalities: []string{"text"}}}}

	target := func(info ProviderInfo, model string) chatCallTarget {
		return chatCallTarget{fullModel: "p/" + model, actualModel: model, info: info}
	}

	t.Run("audio to claude falls back to gemini", func(t *testing.T) {
		chain, err := admitChainInputs([]chatCallTarget{target(claude, "claude-opus-5-5"), target(gemini, "gemini-2.5-pro")}, []string{"audio"})
		if err != nil {
			t.Fatal(err)
		}
		var unsupported *unsupportedInputError
		if !errors.As(chain[0].err, &unsupported) || chain[1].err != nil {
			t.Fatalf("chain = %+v", chain)
		}
	})
	t.Run("pdf to a text-only model is refused", func(t *testing.T) {
		_, err := admitChainInputs([]chatCallTarget{target(textOnly, "llama")}, []string{"pdf"})
		var unsupported *unsupportedInputError
		if !errors.As(err, &unsupported) || !slices.Equal(unsupported.missing, []string{"pdf"}) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown models are admitted", func(t *testing.T) {
		if _, err := admitChainInputs([]chatCallTarget{target(unknown, "some-new-model")}, []string{"video"}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("claude reads pdf and images", func(t *testing.T) {
		if _, err := admitChainInputs([]chatCallTarget{target(claude, "claude-sonnet-4-6")}, []string{"image", "pdf"}); err != nil {
			t.Fatal(err)
		}
	})
}
