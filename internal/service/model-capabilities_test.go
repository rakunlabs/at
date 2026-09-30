package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

// Expected values are the providers' published per-model pages:
// platform.claude.com (models overview, vision, PDF support, thinking
// "Sampling parameters"), ai.google.dev per-model "Supported data types" and
// "Capabilities", developers.openai.com per-model modalities.
func TestDetectModelCapabilities(t *testing.T) {
	tests := []struct {
		provider, model string
		inputs, outputs string // "" = unknown
		features        map[string]bool
	}{
		// Claude: text, image, PDF in; text out. Sampling rejected from Opus 4.7 / Sonnet 5.
		{"anthropic", "claude-opus-5-5", "text,image,pdf", "text", map[string]bool{FeatureTemperature: false, FeatureToolCalling: true, FeatureWebSearch: true, FeatureStructuredOutput: false}},
		{"anthropic", "claude-opus-4-7", "text,image,pdf", "text", map[string]bool{FeatureTemperature: false}},
		{"anthropic", "claude-opus-4-6", "text,image,pdf", "text", map[string]bool{FeatureTemperature: true}},
		{"anthropic", "claude-sonnet-4-6", "text,image,pdf", "text", map[string]bool{FeatureTemperature: true}},
		{"anthropic", "claude-sonnet-5", "text,image,pdf", "text", map[string]bool{FeatureTemperature: false}},
		{"anthropic", "claude-haiku-4-5-20251001", "text,image,pdf", "text", map[string]bool{FeatureTemperature: true}},
		{"anthropic", "claude-fable-5-1", "text,image,pdf", "text", map[string]bool{FeatureTemperature: false}},
		{"anthropic", "custom-model", "", "", nil},
		{"bedrock", "us.anthropic.claude-opus-5-5-v1:0", "text,image,pdf", "text", map[string]bool{FeatureWebSearch: false}},
		// Gemini
		{"gemini", "gemini-3-flash-preview", "text,image,pdf,audio,video", "text", map[string]bool{FeatureToolCalling: true, FeatureWebSearch: true}},
		{"gemini", "gemini-2.5-pro", "text,image,pdf,audio,video", "text", nil},
		{"gemini", "gemini-2.0-flash", "text,image,audio,video", "text", nil},
		{"gemini", "gemini-3-pro-image", "text,image", "text,image", map[string]bool{FeatureToolCalling: false}},
		{"gemini", "gemini-3.1-flash-image", "text,image,pdf,video", "text,image", map[string]bool{FeatureToolCalling: false}},
		{"gemini", "gemini-3.8-flash-tts", "text", "audio", nil},
		{"gemini", "gemini-embedding-001", "", "", nil},
		{"vertex", "google/gemini-2.5-flash", "text,image,pdf,audio,video", "text", map[string]bool{FeatureWebSearch: false}},
		// OpenAI: text + image (+ PDF via file parts); reasoning models reject temperature.
		{"openai", "gpt-5", "text,image,pdf", "text", map[string]bool{FeatureTemperature: false, FeatureStructuredOutput: true}},
		{"openai", "gpt-6-astra", "text,image,pdf", "text", map[string]bool{FeatureTemperature: false}},
		{"openai", "gpt-4o", "text,image,pdf", "text", map[string]bool{FeatureTemperature: true}},
		{"openai", "gpt-4.1-mini", "text,image,pdf", "text", map[string]bool{FeatureTemperature: true}},
		{"openai", "o3", "text,image,pdf", "text", map[string]bool{FeatureTemperature: false}},
		{"azure", "gpt-audio", "text,audio", "text,audio", nil},
		{"openai", "gpt-4o-audio-preview", "text,audio", "text,audio", nil},
		{"openai", "gpt-4o-search-preview", "text,image,pdf", "text", map[string]bool{FeatureWebSearch: true, FeatureToolCalling: false}},
		{"openai", "gpt-realtime", "", "", nil},
		{"openai", "llama-3.3-70b-versatile", "", "", nil},
		{"cohere", "command-a", "", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.model, func(t *testing.T) {
			caps := DetectModelCapabilities(tt.provider, tt.model)
			if got := strings.Join(caps.InputModalities, ","); got != tt.inputs {
				t.Fatalf("inputs = %q, want %q", got, tt.inputs)
			}
			if got := strings.Join(caps.OutputModalities, ","); got != tt.outputs {
				t.Fatalf("outputs = %q, want %q", got, tt.outputs)
			}
			for name, want := range tt.features {
				got, known := caps.Feature(name)
				if !known || got != want {
					t.Fatalf("feature %s = %v (known %v), want %v", name, got, known, want)
				}
			}
		})
	}
}

func TestResolveModelCapabilitiesOverride(t *testing.T) {
	enabled, disabled := true, false
	cfg := config.LLMConfig{
		Type:   "openai",
		Models: []string{"custom-omni", "gpt-5", "claude-proxy"},
		ModelCapabilities: map[string]config.ModelCapability{
			// Unknown model: the override is the whole truth.
			"custom-omni": {InputModalities: []string{"audio", "text", "video"}, Features: map[string]bool{FeatureToolCalling: true}},
			// Known model: fields override independently.
			"gpt-5": {Features: map[string]bool{FeatureTemperature: true}},
			// Legacy image switch on an unknown model.
			"claude-proxy": {ImageInput: &enabled},
		},
	}

	omni := ProviderModelCapabilities(cfg, "custom-omni")
	if strings.Join(omni.InputModalities, ",") != "text,audio,video" || omni.OutputModalities != nil {
		t.Fatalf("custom-omni = %+v", omni)
	}
	if v, ok := omni.Feature(FeatureToolCalling); !ok || !v {
		t.Fatalf("custom-omni tool calling = %v %v", v, ok)
	}

	gpt := ProviderModelCapabilities(cfg, "gpt-5")
	if v, _ := gpt.Feature(FeatureTemperature); !v {
		t.Fatal("feature override ignored")
	}
	if v, _ := gpt.Feature(FeatureStructuredOutput); !v {
		t.Fatal("unrelated detected feature lost")
	}
	if strings.Join(gpt.InputModalities, ",") != "text,image,pdf" {
		t.Fatalf("detected inputs lost: %v", gpt.InputModalities)
	}

	if got := strings.Join(ProviderModelCapabilities(cfg, "claude-proxy").InputModalities, ","); got != "text,image" {
		t.Fatalf("legacy image_input = %q", got)
	}

	// Legacy false removes only image from detection.
	cfg.Type = "anthropic"
	cfg.ModelCapabilities = map[string]config.ModelCapability{"claude-opus-5": {ImageInput: &disabled}}
	if got := strings.Join(ProviderModelCapabilities(cfg, "claude-opus-5").InputModalities, ","); got != "text,pdf" {
		t.Fatalf("legacy image_input=false = %q", got)
	}
}

func TestValidateModelCapabilityOverride(t *testing.T) {
	tests := []struct {
		name     string
		override ModelCapabilityOverride
		ok       bool
	}{
		{"inputs", ModelCapabilityOverride{InputModalities: []string{"text", "pdf"}}, true},
		{"empty", ModelCapabilityOverride{}, false},
		{"no text", ModelCapabilityOverride{InputModalities: []string{"image"}}, false},
		{"unknown modality", ModelCapabilityOverride{InputModalities: []string{"text", "3d"}}, false},
		{"pdf is not an output", ModelCapabilityOverride{OutputModalities: []string{"pdf"}}, false},
		{"duplicate", ModelCapabilityOverride{InputModalities: []string{"text", "text"}}, false},
		{"unknown feature", ModelCapabilityOverride{Features: map[string]bool{"teleport": true}}, false},
		{"feature", ModelCapabilityOverride{Features: map[string]bool{FeatureWebSearch: false}}, true},
		{"bad reasoning", ModelCapabilityOverride{ReasoningEfforts: []string{"none"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateModelCapabilityOverride("anthropic", tt.override); (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok %v", err, tt.ok)
			}
		})
	}
}

func TestCapabilitiesAcceptsUnknown(t *testing.T) {
	var unknown ModelCapabilities
	for _, m := range InputModalities {
		if !unknown.Accepts(m) {
			t.Fatalf("unknown capabilities must admit %s", m)
		}
	}
	known := ModelCapabilities{InputModalities: []string{"text", "image"}}
	if known.Accepts(ModalityPDF) || !known.Accepts(ModalityImage) {
		t.Fatal("known capabilities must be enforced")
	}
	if !slices.Equal(orderedModalities([]string{"video", "text"}, InputModalities), []string{"text", "video"}) {
		t.Fatal("modalities must be ordered")
	}
}
