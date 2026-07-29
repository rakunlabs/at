package cohere

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestCreateEmbeddingForwardsOptionalFields(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/embed" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body embedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.OutputDimension == nil || *body.OutputDimension != 512 {
			t.Errorf("output_dimension = %v", body.OutputDimension)
		}
		if !reflect.DeepEqual(body.EmbeddingTypes, []string{"base64"}) {
			t.Errorf("embedding_types = %#v", body.EmbeddingTypes)
		}
		_, _ = w.Write([]byte(`{"embeddings":{"base64":["AQIDBA=="]},"meta":{"billed_units":{"input_tokens":2}}}`))
	}))
	defer server.Close()

	provider, err := New("test-key", "unused", server.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dimensions := 512
	resp, err := provider.CreateEmbedding(context.Background(), service.EmbeddingRequest{
		Input:          []string{"hello"},
		Model:          "embed-v4.0",
		EncodingFormat: "base64",
		Dimensions:     &dimensions,
	})
	if err != nil {
		t.Fatalf("CreateEmbedding: %v", err)
	}
	if len(resp.Base64Embeddings) != 1 || resp.Base64Embeddings[0] != "AQIDBA==" {
		t.Fatalf("base64 embeddings = %#v", resp.Base64Embeddings)
	}
}

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
