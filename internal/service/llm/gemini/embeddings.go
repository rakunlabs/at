package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
)

const embeddingResponseMaxBytes = 256 << 20

// ─── Embeddings ───
//
// Native Gemini embeddings endpoint:
//   POST /v1beta/models/{model}:batchEmbedContents
// Default model: text-embedding-004 (768-dim) or gemini-embedding-001 (3072-dim).

type batchEmbedRequest struct {
	Requests []embedRequest `json:"requests"`
}

type embedRequest struct {
	Model                string  `json:"model"`
	Content              content `json:"content"`
	OutputDimensionality *int    `json:"outputDimensionality,omitempty"`
	TaskType             string  `json:"taskType,omitempty"`
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
	if p.embeddingMaxInputs > 0 && len(req.Input) > p.embeddingMaxInputs {
		return nil, fmt.Errorf("embedding input count %d exceeds configured maximum %d", len(req.Input), p.embeddingMaxInputs)
	}
	release, err := p.limiter.Acquire(ctx, common.EstimateEmbeddingInputTokens(req.Input))
	if err != nil {
		return nil, err
	}
	defer release()

	model := req.Model
	if model == "" {
		model = "text-embedding-004"
	}

	if p.pathPrefix != "" {
		return p.createVertexEmbedding(ctx, req, model)
	}

	body := batchEmbedRequest{
		Requests: make([]embedRequest, len(req.Input)),
	}
	taskType := geminiEmbeddingTaskType(req.InputType)
	for i, text := range req.Input {
		body.Requests[i] = embedRequest{
			// Per Gemini docs, the per-request model field must be of the
			// form "models/<name>".
			Model: "models/" + model,
			Content: content{
				Parts: []part{{Text: text}},
			},
			OutputDimensionality: req.Dimensions,
			TaskType:             taskType,
		}
	}

	jsonData, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal gemini embed: %w", err)
	}

	// Public Generative Language API path; Vertex returned above.
	path := fmt.Sprintf("/v1beta/models/%s:batchEmbedContents", model)

	respBody, err := p.postEmbedding(ctx, path, jsonData)
	if err != nil {
		return nil, err
	}

	var parsed batchEmbedResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("decode embed response: %w (body: %s)", err, string(respBody))
	}

	out := make([][]float64, len(parsed.Embeddings))
	for i, e := range parsed.Embeddings {
		out[i] = e.Values
	}
	usage := common.EstimateEmbeddingInputTokens(req.Input)
	result := &service.EmbeddingResponse{
		Embeddings:     out,
		Model:          model,
		Usage:          service.Usage{PromptTokens: usage, TotalTokens: usage},
		UsageEstimated: true,
	}
	if err := service.ValidateEmbeddingResponse(req, result); err != nil {
		return nil, fmt.Errorf("gemini embed: invalid upstream response: %w", err)
	}
	return result, nil
}

func geminiEmbeddingTaskType(inputType string) string {
	switch inputType {
	case "search_document":
		return "RETRIEVAL_DOCUMENT"
	case "search_query":
		return "RETRIEVAL_QUERY"
	case "classification":
		return "CLASSIFICATION"
	case "clustering":
		return "CLUSTERING"
	default:
		return ""
	}
}

// postEmbedding sends one embedding request and maps upstream failures to the
// shared rate-limit / upstream error types.
func (p *Provider) postEmbedding(ctx context.Context, path string, jsonData []byte) ([]byte, error) {
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
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, embeddingResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read embed response: %w", err)
	}
	if len(respBody) > embeddingResponseMaxBytes {
		return nil, fmt.Errorf("read embed response: body exceeds %d bytes", embeddingResponseMaxBytes)
	}
	if resp.StatusCode >= 400 {
		message := string(respBody)
		code := ""
		var envelope generateContentResponse
		if json.Unmarshal(respBody, &envelope) == nil && envelope.Error != nil {
			message = envelope.Error.Message
			code = envelope.Error.Status
		}
		underlying := fmt.Errorf("gemini embed API error (status %d): %s", resp.StatusCode, message)
		if resp.StatusCode == http.StatusTooManyRequests {
			return nil, &service.RateLimitError{
				StatusCode: resp.StatusCode,
				RetryAfter: common.ParseRetryAfter(resp.Header),
				Provider:   "gemini",
				Message:    message,
				Underlying: underlying,
			}
		}
		return nil, &service.UpstreamError{
			Provider:   "gemini",
			StatusCode: resp.StatusCode,
			Code:       code,
			Message:    message,
			Underlying: underlying,
		}
	}

	return respBody, nil
}

// Vertex AI does not serve batchEmbedContents. Its text embedding models are
// called through the prediction endpoint:
//
//	POST {prefix}/models/{model}:predict
//	{"instances":[{"content":"...","task_type":"..."}],"parameters":{"outputDimensionality":N}}
//
// gemini-embedding-001 accepts a single instance per request, so every input
// is sent separately; other models accept small batches, but one-per-call is
// correct for all of them.
type vertexPredictEmbedRequest struct {
	Instances  []vertexEmbedInstance    `json:"instances"`
	Parameters *vertexEmbedPredictParam `json:"parameters,omitempty"`
}

type vertexEmbedInstance struct {
	Content  string `json:"content"`
	TaskType string `json:"task_type,omitempty"`
}

type vertexEmbedPredictParam struct {
	OutputDimensionality *int `json:"outputDimensionality,omitempty"`
}

type vertexPredictEmbedResponse struct {
	Predictions []struct {
		Embeddings struct {
			Values     []float64 `json:"values"`
			Statistics struct {
				TokenCount float64 `json:"token_count"`
			} `json:"statistics"`
		} `json:"embeddings"`
	} `json:"predictions"`
}

func (p *Provider) createVertexEmbedding(ctx context.Context, req service.EmbeddingRequest, model string) (*service.EmbeddingResponse, error) {
	taskType := geminiEmbeddingTaskType(req.InputType)
	var params *vertexEmbedPredictParam
	if req.Dimensions != nil {
		params = &vertexEmbedPredictParam{OutputDimensionality: req.Dimensions}
	}
	path := p.pathPrefix + fmt.Sprintf("/models/%s:predict", model)

	out := make([][]float64, 0, len(req.Input))
	tokens := 0
	for _, text := range req.Input {
		jsonData, err := json.Marshal(vertexPredictEmbedRequest{
			Instances:  []vertexEmbedInstance{{Content: text, TaskType: taskType}},
			Parameters: params,
		})
		if err != nil {
			return nil, fmt.Errorf("marshal vertex embed: %w", err)
		}
		respBody, err := p.postEmbedding(ctx, path, jsonData)
		if err != nil {
			return nil, err
		}
		var parsed vertexPredictEmbedResponse
		if err := json.Unmarshal(respBody, &parsed); err != nil {
			return nil, fmt.Errorf("decode vertex embed response: %w", err)
		}
		if len(parsed.Predictions) != 1 {
			return nil, fmt.Errorf("vertex embed: expected 1 prediction, got %d", len(parsed.Predictions))
		}
		out = append(out, parsed.Predictions[0].Embeddings.Values)
		tokens += int(parsed.Predictions[0].Embeddings.Statistics.TokenCount)
	}

	result := &service.EmbeddingResponse{
		Embeddings: out,
		Model:      model,
		Usage:      service.Usage{PromptTokens: tokens, TotalTokens: tokens},
	}
	if tokens == 0 {
		estimate := common.EstimateEmbeddingInputTokens(req.Input)
		result.Usage = service.Usage{PromptTokens: estimate, TotalTokens: estimate}
		result.UsageEstimated = true
	}
	if err := service.ValidateEmbeddingResponse(req, result); err != nil {
		return nil, fmt.Errorf("vertex embed: invalid upstream response: %w", err)
	}
	return result, nil
}
