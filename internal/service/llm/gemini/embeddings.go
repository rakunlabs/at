package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Embeddings ───
//
// Native Gemini embeddings endpoint:
//   POST /v1beta/models/{model}:batchEmbedContents
// Default model: text-embedding-004 (768-dim) or gemini-embedding-001 (3072-dim).

type batchEmbedRequest struct {
	Requests []embedRequest `json:"requests"`
}

type embedRequest struct {
	Model   string  `json:"model"`
	Content content `json:"content"`
}

type batchEmbedResponse struct {
	Embeddings []struct {
		Values []float64 `json:"values"`
	} `json:"embeddings"`
}

// CreateEmbedding implements service.EmbeddingProvider for the Gemini provider.
//
// On the public Generative Language API this requires an API key
// (x-goog-api-key); on Vertex-Gemini it uses the configured token source.
func (p *Provider) CreateEmbedding(ctx context.Context, req service.EmbeddingRequest) (*service.EmbeddingResponse, error) {
	model := req.Model
	if model == "" {
		model = "text-embedding-004"
	}

	body := batchEmbedRequest{
		Requests: make([]embedRequest, len(req.Input)),
	}
	for i, text := range req.Input {
		body.Requests[i] = embedRequest{
			// Per Gemini docs, the per-request model field must be of the
			// form "models/<name>".
			Model: "models/" + model,
			Content: content{
				Parts: []part{{Text: text}},
			},
		}
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini embed: %w", err)
	}

	// URL path depends on whether we're using Vertex prefix or public API.
	path := fmt.Sprintf("/v1beta/models/%s:batchEmbedContents", model)
	if p.pathPrefix != "" {
		path = p.pathPrefix + fmt.Sprintf("/models/%s:batchEmbedContents", model)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL+path, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.tokenSource != nil {
		tk, terr := p.tokenSource.Token()
		if terr != nil {
			return nil, fmt.Errorf("gemini auth: %w", terr)
		}
		httpReq.Header.Set("Authorization", "Bearer "+tk)
	} else if p.APIKey != "" {
		httpReq.Header.Set("x-goog-api-key", p.APIKey)
	}

	resp, err := p.client.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini embed http: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("gemini embed API error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var parsed batchEmbedResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode embed response: %w (body: %s)", err, string(respBody))
	}

	out := make([][]float64, len(parsed.Embeddings))
	for i, e := range parsed.Embeddings {
		out[i] = e.Values
	}
	return &service.EmbeddingResponse{
		Embeddings: out,
		Model:      model,
	}, nil
}
