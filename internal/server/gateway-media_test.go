package server

import (
	"strings"
	"testing"
)

func TestUnsupportedAudioTranscriptionMessageChatGPT(t *testing.T) {
	t.Parallel()

	got := unsupportedAudioTranscriptionMessage("openai", ProviderInfo{
		providerType: "openai",
		authType:     "chatgpt",
	})
	for _, want := range []string{"ChatGPT/Codex OAuth", "openai-api/whisper-1"} {
		if !strings.Contains(got, want) {
			t.Errorf("message %q does not contain %q", got, want)
		}
	}
}

func TestUnsupportedAudioTranscriptionMessageGeneric(t *testing.T) {
	t.Parallel()

	got := unsupportedAudioTranscriptionMessage("anthropic", ProviderInfo{providerType: "anthropic"})
	want := `provider "anthropic" does not support audio transcription`
	if got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
}
