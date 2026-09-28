package service

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestValidateEmbeddingResponse(t *testing.T) {
	tests := []struct {
		name string
		req  EmbeddingRequest
		resp *EmbeddingResponse
		want string
	}{
		{
			name: "valid float batch",
			req:  EmbeddingRequest{Input: []string{"a", "b"}},
			resp: &EmbeddingResponse{Embeddings: [][]float64{{1, 2}, {3, 4}}},
		},
		{
			name: "count mismatch",
			req:  EmbeddingRequest{Input: []string{"a", "b"}},
			resp: &EmbeddingResponse{Embeddings: [][]float64{{1}}},
			want: "does not match input count",
		},
		{
			name: "dimension mismatch",
			req:  EmbeddingRequest{Input: []string{"a", "b"}},
			resp: &EmbeddingResponse{Embeddings: [][]float64{{1, 2}, {3}}},
			want: "expected 2",
		},
		{
			name: "valid base64",
			req:  EmbeddingRequest{Input: []string{"a"}, EncodingFormat: "base64"},
			resp: &EmbeddingResponse{Base64Embeddings: []string{base64.StdEncoding.EncodeToString(make([]byte, 8))}},
		},
		{
			name: "invalid base64 bytes",
			req:  EmbeddingRequest{Input: []string{"a"}, EncodingFormat: "base64"},
			resp: &EmbeddingResponse{Base64Embeddings: []string{base64.StdEncoding.EncodeToString([]byte{1, 2, 3})}},
			want: "invalid float32 byte length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateEmbeddingResponse(tt.req, tt.resp)
			if tt.want == "" && err != nil {
				t.Fatalf("ValidateEmbeddingResponse: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidEmbeddingInputType(t *testing.T) {
	for _, value := range []string{"", "search_document", "search_query", "classification", "clustering"} {
		if !ValidEmbeddingInputType(value) {
			t.Errorf("%q should be valid", value)
		}
	}
	if ValidEmbeddingInputType("image") {
		t.Fatal("image should not be a portable text embedding input type")
	}
}
