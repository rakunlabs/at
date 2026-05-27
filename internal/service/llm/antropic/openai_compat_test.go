package antropic

import (
	"reflect"
	"testing"
)

func TestTranslateAnthropicToolChoice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want map[string]any
	}{
		{"string auto", "auto", map[string]any{"type": "auto"}},
		{"string none", "none", map[string]any{"type": "none"}},
		{"string required → any", "required", map[string]any{"type": "any"}},
		{"openai function object", map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "foo"},
		}, map[string]any{"type": "tool", "name": "foo"}},
		{"anthropic-shape passthrough", map[string]any{"type": "tool", "name": "bar"}, map[string]any{"type": "tool", "name": "bar"}},
		{"unknown string nil", "weird", nil},
		{"object without name nil", map[string]any{"type": "function"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateAnthropicToolChoice(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnthropicResponseFormatInstruction(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"unset", nil, ""},
		{"json_object", map[string]any{"type": "json_object"}, "Respond with a single JSON object and nothing else. Do not wrap the JSON in markdown fences or prose."},
		{"json_schema simple", map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "Output",
				"schema": map[string]any{"type": "object"},
			},
		}, ""}, // we don't check exact body, just non-empty
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anthropicResponseFormatInstruction(tt.in)
			if tt.want == "" && tt.in == nil {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
				return
			}
			if tt.name == "json_object" {
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
				return
			}
			if got == "" {
				t.Error("expected non-empty json_schema instruction")
			}
		})
	}
}
