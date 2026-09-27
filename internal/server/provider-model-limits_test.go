package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func TestValidateModelLimits(t *testing.T) {
	tests := []struct {
		name   string
		config config.LLMConfig
		want   string
	}{
		{name: "unset"},
		{name: "valid default", config: config.LLMConfig{Model: "claude-opus", ModelLimits: map[string]config.ModelLimit{"claude-opus": {Context: 1_000_000, Output: 128_000}}}},
		{name: "valid model list", config: config.LLMConfig{Models: []string{"claude-opus"}, ModelLimits: map[string]config.ModelLimit{"claude-opus": {Context: 1_000_000, Output: 128_000}}}},
		{name: "empty model", config: config.LLMConfig{ModelLimits: map[string]config.ModelLimit{" ": {Context: 1, Output: 1}}}, want: "keys must not be empty"},
		{name: "not advertised", config: config.LLMConfig{Model: "other", ModelLimits: map[string]config.ModelLimit{"model": {Context: 1, Output: 1}}}, want: "does not match an advertised chat model"},
		{name: "missing context", config: config.LLMConfig{Model: "model", ModelLimits: map[string]config.ModelLimit{"model": {Output: 1}}}, want: ".context must be > 0"},
		{name: "missing output", config: config.LLMConfig{Model: "model", ModelLimits: map[string]config.ModelLimit{"model": {Context: 1}}}, want: ".output must be > 0"},
		{name: "output exceeds context", config: config.LLMConfig{Model: "model", ModelLimits: map[string]config.ModelLimit{"model": {Context: 10, Output: 11}}}, want: ".output must be <= context"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateModelLimits(tt.config)
			if tt.want == "" && got != "" {
				t.Fatalf("unexpected error: %s", got)
			}
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Fatalf("error %q does not contain %q", got, tt.want)
			}
		})
	}
}

func TestValidateModelCapabilities(t *testing.T) {
	enabled, disabled := true, false
	tests := []struct {
		name   string
		config config.LLMConfig
		want   string
	}{
		{name: "unset"},
		{name: "enabled", config: config.LLMConfig{Model: "vision", ModelCapabilities: map[string]config.ModelCapability{"vision": {ImageInput: &enabled}}}},
		{name: "explicitly disabled", config: config.LLMConfig{Models: []string{"text"}, ModelCapabilities: map[string]config.ModelCapability{"text": {ImageInput: &disabled}}}},
		{name: "empty model", config: config.LLMConfig{ModelCapabilities: map[string]config.ModelCapability{" ": {ImageInput: &enabled}}}, want: "keys must not be empty"},
		{name: "not advertised", config: config.LLMConfig{Model: "other", ModelCapabilities: map[string]config.ModelCapability{"vision": {ImageInput: &enabled}}}, want: "does not match an advertised chat model"},
		{name: "empty capability", config: config.LLMConfig{Model: "vision", ModelCapabilities: map[string]config.ModelCapability{"vision": {}}}, want: "image_input must be true or false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateModelCapabilities(tt.config)
			if tt.want == "" && got != "" {
				t.Fatalf("unexpected error: %s", got)
			}
			if tt.want != "" && !strings.Contains(got, tt.want) {
				t.Fatalf("error %q does not contain %q", got, tt.want)
			}
		})
	}
}
