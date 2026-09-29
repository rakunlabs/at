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

type staticGoogleToken string

func (s staticGoogleToken) Token() (string, error) { return string(s), nil }

// Vertex serves text embeddings through :predict, one instance per call for
// gemini-embedding-001; batchEmbedContents does not exist there.
func TestCreateEmbeddingVertexUsesPredict(t *testing.T) {
	t.Parallel()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/projects/p/locations/us-central1/publishers/google/models/gemini-embedding-001:predict" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q", got)
		}
		var body vertexPredictEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if len(body.Instances) != 1 || body.Instances[0].TaskType != "RETRIEVAL_DOCUMENT" {
			t.Errorf("instances = %+v", body.Instances)
		}
		if body.Parameters == nil || body.Parameters.OutputDimensionality == nil || *body.Parameters.OutputDimensionality != 4 {
			t.Errorf("parameters = %+v", body.Parameters)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"predictions": []any{map[string]any{"embeddings": map[string]any{
				"values":     []float64{float64(calls), 0, 0, 0},
				"statistics": map[string]any{"token_count": 3},
			}}},
		})
	}))
	defer server.Close()

	provider, err := New("", "unused", server.URL, "", false,
		WithGoogleTokenSource(staticGoogleToken("tok")),
		WithPathPrefix("/v1/projects/p/locations/us-central1/publishers/google"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dimensions := 4
	resp, err := provider.CreateEmbedding(context.Background(), service.EmbeddingRequest{
		Input:      []string{"a", "b"},
		Model:      "gemini-embedding-001",
		Dimensions: &dimensions,
		InputType:  "search_document",
	})
	if err != nil {
		t.Fatalf("CreateEmbedding: %v", err)
	}
	if calls != 2 || len(resp.Embeddings) != 2 || resp.Embeddings[1][0] != 2 {
		t.Fatalf("calls=%d embeddings=%v", calls, resp.Embeddings)
	}
	if resp.UsageEstimated || resp.Usage.PromptTokens != 6 {
		t.Fatalf("usage = %+v estimated=%v", resp.Usage, resp.UsageEstimated)
	}
}
