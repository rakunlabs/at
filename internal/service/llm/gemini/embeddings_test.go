package gemini

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestCreateEmbeddingForwardsDimensions(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-embedding-001:batchEmbedContents" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body batchEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(body.Requests) != 1 || body.Requests[0].OutputDimensionality == nil || *body.Requests[0].OutputDimensionality != 768 {
			t.Errorf("requests = %+v", body.Requests)
		}
		if body.Requests[0].TaskType != "RETRIEVAL_QUERY" {
			t.Errorf("taskType = %q", body.Requests[0].TaskType)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": []any{map[string]any{"values": make([]float64, 768)}},
		})
	}))
	defer server.Close()

	provider, err := New("test-key", "unused", server.URL, "", false)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dimensions := 768
	resp, err := provider.CreateEmbedding(context.Background(), service.EmbeddingRequest{
		Input:      []string{"hello"},
		Model:      "gemini-embedding-001",
		Dimensions: &dimensions,
		InputType:  "search_query",
	})
	if err != nil {
		t.Fatalf("CreateEmbedding: %v", err)
	}
	if len(resp.Embeddings) != 1 || len(resp.Embeddings[0]) != 768 {
		t.Fatalf("embeddings = %#v", resp.Embeddings)
	}
	if !resp.UsageEstimated || resp.Usage.TotalTokenCount() == 0 {
		t.Fatalf("estimated usage = %+v flag=%v", resp.Usage, resp.UsageEstimated)
	}
}
