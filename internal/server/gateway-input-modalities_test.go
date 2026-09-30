package server

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func TestRequestInputModalities(t *testing.T) {
	msgs := []OpenAIMessage{
		{Role: "system", Content: json.RawMessage(`"be brief"`)},
		{Role: "user", Content: json.RawMessage(`[
			{"type":"text","text":"read"},
			{"type":"image_url","image_url":{"url":"data:image/png;base64,AA"}},
			{"type":"file","file":{"filename":"a.pdf","file_data":"data:application/pdf;base64,AA"}},
			{"type":"file","file":{"filename":"notes.txt","file_data":"data:text/plain;base64,AA"}},
			{"type":"input_file","filename":"q.sql","file_data":"data:application/sql;base64,AA"},
			{"type":"input_file","filename":"data.json","file_data":"data:application/json;base64,AA"},
			{"type":"input_file","filename":"report.docx"}
		]`)},
		{Role: "tool", ToolCallID: "c1", Content: json.RawMessage(`[{"type":"input_audio","input_audio":{"data":"AA","format":"wav"}}]`)},
	}
	if got := requestInputModalities(msgs); !slices.Equal(got, []string{"image", "pdf", "audio"}) {
		t.Fatalf("modalities = %v", got)
	}
	if got := requestInputModalities([]OpenAIMessage{{Role: "user", Content: json.RawMessage(`"hi"`)}}); len(got) != 0 {
		t.Fatalf("text-only = %v", got)
	}

	anth := []anthropicMessage{{Role: "user", Content: json.RawMessage(`[
		{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"AA"}},
		{"type":"tool_result","tool_use_id":"x","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA"}}]}
	]`)}}
	if got := anthropicInputModalities(anth); !slices.Equal(got, []string{"image", "pdf"}) {
		t.Fatalf("anthropic modalities = %v", got)
	}
}

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
