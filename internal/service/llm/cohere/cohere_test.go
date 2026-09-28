package cohere

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestCreateEmbeddingNonSuccessReturnsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"invalid token","code":"invalid_api_key"}`))
	}))
	defer server.Close()

	provider, err := New("test-key", "unused", server.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = provider.CreateEmbedding(context.Background(), service.EmbeddingRequest{Input: []string{"hello"}, Model: "embed-v4.0"})

	var upstreamErr *service.UpstreamError
	if !errors.As(err, &upstreamErr) {
		t.Fatalf("error = %T %v, want *service.UpstreamError", err, err)
	}
	if upstreamErr.StatusCode != http.StatusUnauthorized || upstreamErr.Code != "invalid_api_key" || upstreamErr.Message != "invalid token" {
		t.Fatalf("UpstreamError = %#v", upstreamErr)
	}
}

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
		embedding := base64.StdEncoding.EncodeToString(make([]byte, 512*4))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": map[string]any{"base64": []string{embedding}},
			"meta":       map[string]any{"billed_units": map[string]any{"input_tokens": 2}},
		})
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
	if len(resp.Base64Embeddings) != 1 || resp.Base64Embeddings[0] != base64.StdEncoding.EncodeToString(make([]byte, 512*4)) {
		t.Fatalf("base64 embeddings = %#v", resp.Base64Embeddings)
	}
}

func TestCreateEmbeddingChunksAndPreservesOrder(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body embedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(body.Texts) == 0 || len(body.Texts) > 96 {
			t.Fatalf("chunk size = %d", len(body.Texts))
		}
		if body.InputType != "search_query" {
			t.Errorf("input_type = %q", body.InputType)
		}
		embeddings := make([][]float64, len(body.Texts))
		for i, text := range body.Texts {
			embeddings[i] = []float64{float64(len(text))}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": map[string]any{"float": embeddings},
			"meta":       map[string]any{"billed_units": map[string]any{"input_tokens": len(body.Texts)}},
		})
	}))
	defer server.Close()

	provider, err := New("test-key", "unused", server.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	input := make([]string, 200)
	for i := range input {
		input[i] = strings.Repeat("x", i%10+1)
	}
	resp, err := provider.CreateEmbedding(context.Background(), service.EmbeddingRequest{
		Input: input, Model: "embed-v4.0", InputType: "search_query",
	})
	if err != nil {
		t.Fatalf("CreateEmbedding: %v", err)
	}
	if calls != 3 || len(resp.Embeddings) != len(input) || resp.Usage.TotalTokenCount() != len(input) {
		t.Fatalf("calls=%d embeddings=%d usage=%d", calls, len(resp.Embeddings), resp.Usage.TotalTokenCount())
	}
	for i := range input {
		if got := resp.Embeddings[i][0]; got != float64(len(input[i])) {
			t.Fatalf("embedding %d = %v", i, got)
		}
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
