package server

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/render"
)

// TestRenderTemplateHelpers verifies that the mugo-backed render engine (used
// by resolveTemplate in gateway-mcp.go) exposes the helpers we rely on for
// MCP HTTP tool body/URL/header templates.
//
// Reference: https://rytsh.io/mugo/functions/reference.html
func TestRenderTemplateHelpers(t *testing.T) {
	tests := []struct {
		name     string
		tmpl     string
		data     any
		want     string
		contains string
	}{
		{
			name: "codec JsonEncode marshals a string",
			tmpl: `{{ codec.JsonEncode .prompt false | codec.ByteToString }}`,
			data: map[string]any{"prompt": `a cat "named" fluffy`},
			want: `"a cat \"named\" fluffy"`,
		},
		{
			name: "codec JsonEncode marshals a map",
			tmpl: `{{ codec.JsonEncode .m false | codec.ByteToString }}`,
			data: map[string]any{"m": map[string]any{"k": "v"}},
			want: `{"k":"v"}`,
		},
		{
			name: "sprig default falls back on empty string",
			tmpl: `{{ default "9:16" .aspect }}`,
			data: map[string]any{"aspect": ""},
			want: "9:16",
		},
		{
			name: "sprig default returns provided value",
			tmpl: `{{ default "png" .fmt }}`,
			data: map[string]any{"fmt": "jpeg"},
			want: "jpeg",
		},
		{
			name: "sprig toJson shorthand",
			tmpl: `{{ toJson .prompt }}`,
			data: map[string]any{"prompt": "hello"},
			want: `"hello"`,
		},
		{
			name: "combined produces valid JSON body",
			tmpl: `{"prompt": {{ toJson .prompt }}, "aspect": {{ toJson (default "9:16" .aspect) }}}`,
			data: map[string]any{
				"prompt": "hi\nthere",
			},
			contains: `"prompt": "hi\n`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := render.ExecuteWithData(tt.tmpl, tt.data)
			if err != nil {
				t.Fatalf("render: %v", err)
			}

			got := string(out)
			if tt.contains != "" {
				if !strings.Contains(got, tt.contains) {
					t.Errorf("output does not contain %q: got %q", tt.contains, got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
