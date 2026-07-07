package cohere

import (
	"reflect"
	"testing"
)

func TestTranslateCohereToolChoice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"nil", nil, ""},
		{"auto is default (omitted)", "auto", ""},
		{"required", "required", "REQUIRED"},
		{"any", "any", "REQUIRED"},
		{"none", "none", "NONE"},
		{"NONE uppercase", "NONE", "NONE"},
		{"unknown string", "banana", ""},
		{"function object maps to REQUIRED", map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "foo"},
		}, "REQUIRED"},
		{"none object", map[string]any{"type": "none"}, "NONE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := translateCohereToolChoice(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTranslateCohereResponseFormat(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"type": "string"}}}
	tests := []struct {
		name string
		in   map[string]any
		want any
	}{
		{"nil", nil, nil},
		{"empty", map[string]any{}, nil},
		{"json_object", map[string]any{"type": "json_object"}, map[string]any{"type": "json_object"}},
		{"json_schema", map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "Out", "schema": schema},
		}, map[string]any{"type": "json_object", "schema": schema}},
		{"json_schema without schema", map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "Out"},
		}, map[string]any{"type": "json_object"}},
		{"text", map[string]any{"type": "text"}, map[string]any{"type": "text"}},
		{"unknown type", map[string]any{"type": "xml"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateCohereResponseFormat(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
