package gemini

import (
	"reflect"
	"testing"
)

func TestTranslateGeminiToolChoice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want *functionCallingConfig
	}{
		{"auto", "auto", &functionCallingConfig{Mode: "AUTO"}},
		{"none", "none", &functionCallingConfig{Mode: "NONE"}},
		{"required", "required", &functionCallingConfig{Mode: "ANY"}},
		{"openai fn", map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "myFn"},
		}, &functionCallingConfig{Mode: "ANY", AllowedFunctionNames: []string{"myFn"}}},
		{"unknown nil", "bogus", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateGeminiToolChoice(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGeminiResponseFormat(t *testing.T) {
	mime, schema := geminiResponseFormat(map[string]any{"type": "json_object"})
	if mime != "application/json" || schema != nil {
		t.Errorf("json_object: got (%q, %v)", mime, schema)
	}

	mime, schema = geminiResponseFormat(map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"schema": map[string]any{"type": "object"},
		},
	})
	if mime != "application/json" {
		t.Errorf("json_schema mime: got %q", mime)
	}
	if schema == nil {
		t.Errorf("json_schema: schema should be non-nil")
	}

	mime, schema = geminiResponseFormat(nil)
	if mime != "" || schema != nil {
		t.Errorf("nil: got (%q, %v)", mime, schema)
	}
}

func TestNormalizeGeminiFinishReason(t *testing.T) {
	tests := []struct {
		raw      string
		toolCalls bool
		want     string
	}{
		{"STOP", false, "stop"},
		{"STOP", true, "tool_calls"},
		{"MAX_TOKENS", false, "length"},
		{"SAFETY", false, "content_filter"},
		{"RECITATION", false, "content_filter"},
		{"PROHIBITED_CONTENT", false, "content_filter"},
		{"SPII", false, "content_filter"},
		{"", true, "tool_calls"},
		{"", false, ""},
		{"MALFORMED_FUNCTION_CALL", false, "tool_calls"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := normalizeGeminiFinishReason(tt.raw, tt.toolCalls); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
