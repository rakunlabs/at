package bedrock

import (
	"reflect"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestSplitAPIKey(t *testing.T) {
	tests := []struct {
		in                        string
		wantA, wantS, wantSession string
	}{
		{"", "", "", ""},
		{"OnlyAccess", "", "", ""},
		{"AK:SK", "AK", "SK", ""},
		{"AK:SK:SESS", "AK", "SK", "SESS"},
		{"AK:SK:SESS:EXTRA", "AK", "SK", "SESS"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			a, s, sess := splitAPIKey(tt.in)
			if a != tt.wantA || s != tt.wantS || sess != tt.wantSession {
				t.Errorf("got (%q,%q,%q), want (%q,%q,%q)",
					a, s, sess, tt.wantA, tt.wantS, tt.wantSession)
			}
		})
	}
}

func TestTranslateBedrockToolChoice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want *converseToolChoice
	}{
		{"none string returns nil", "none", nil}, // bedrock has no "none"
		{"auto string", "auto", &converseToolChoice{Auto: ptrEmptyMap()}},
		{"required string", "required", &converseToolChoice{Any: ptrEmptyMap()}},
		{"openai function object", map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "foo"},
		}, &converseToolChoice{Tool: &struct {
			Name string `json:"name"`
		}{Name: "foo"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateBedrockToolChoice(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func ptrEmptyMap() *map[string]any {
	m := map[string]any{}
	return &m
}

func TestToolChoiceNoneOmitsToolConfig(t *testing.T) {
	p := &Provider{}
	tools := []service.Tool{{Name: "foo", Description: "bar", InputSchema: map[string]any{"type": "object"}}}
	msgs := []service.Message{{Role: "user", Content: "hi"}}

	tests := []struct {
		name       string
		toolChoice any
		wantConfig bool
	}{
		{"none string omits toolConfig", "none", false},
		{"NONE string omits toolConfig", "NONE", false},
		{"none object omits toolConfig", map[string]any{"type": "none"}, false},
		{"auto keeps toolConfig", "auto", true},
		{"nil keeps toolConfig", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := p.buildConverseRequest(msgs, tools, &service.ChatOptions{ToolChoice: tt.toolChoice})
			if got := req.ToolConfig != nil; got != tt.wantConfig {
				t.Errorf("ToolConfig presence = %v, want %v", got, tt.wantConfig)
			}
		})
	}
}

func TestImageFormatFromMime(t *testing.T) {
	cases := map[string]string{
		"image/png":  "png",
		"image/jpeg": "jpeg",
		"image/jpg":  "jpeg",
		"image/gif":  "gif",
		"image/webp": "webp",
		"image/heic": "png", // fallback
		"":           "png",
	}
	for in, want := range cases {
		if got := imageFormatFromMime(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
