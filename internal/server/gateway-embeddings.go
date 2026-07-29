package server

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// embeddingsRequest mirrors the OpenAI /v1/embeddings request body.
//
// `input` may be a single string OR an array of strings (per OpenAI spec);
// we accept both via json.RawMessage and normalise.
type embeddingsRequest struct {
	Input          json.RawMessage `json:"input"`
	Model          string          `json:"model"`
	EncodingFormat string          `json:"encoding_format,omitempty"` // "float" | "base64" — currently we always return float
	Dimensions     *int            `json:"dimensions,omitempty"`      // accepted but only forwarded to providers that support it
	User           string          `json:"user,omitempty"`
}

// embeddingsResponse mirrors the OpenAI /v1/embeddings response body.
type embeddingsResponse struct {
	Object string           `json:"object"` // "list"
	Data   []embeddingDatum `json:"data"`
	Model  string           `json:"model"`
	Usage  embeddingsUsage  `json:"usage"`
}

type embeddingDatum struct {
	Object    string `json:"object"` // "embedding"
	Index     int    `json:"index"`
	Embedding any    `json:"embedding"` // []float64 or base64 string
}

type embeddingsUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// Embeddings handles POST /gateway/v1/embeddings.
//
// It mirrors ChatCompletions' auth / token-budget / provider-resolution
// pipeline, then delegates to the resolved provider's EmbeddingProvider
// implementation. Providers that don't implement EmbeddingProvider get a
// 501 Not Implemented.
func (s *Server) Embeddings(w http.ResponseWriter, r *http.Request) {
	auth, authErr := s.authenticateRequest(r)
	if authErr != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": authErr,
				"type":    "invalid_request_error",
				"code":    "invalid_api_key",
			},
		}, http.StatusUnauthorized)
		return
	}

	var req embeddingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("invalid request body: %v", err),
				"type":    "invalid_request_error",
			},
		}, http.StatusBadRequest)
		return
	}
	if req.EncodingFormat != "" && req.EncodingFormat != "float" && req.EncodingFormat != "base64" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "encoding_format must be either 'float' or 'base64'",
				"type":    "invalid_request_error",
				"param":   "encoding_format",
			},
		}, http.StatusBadRequest)
		return
	}
	// Parse input — single string OR array.
	inputs, err := parseEmbeddingsInput(req.Input)
	if err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"param":   "input",
			},
		}, http.StatusBadRequest)
		return
	}
	if len(inputs) == 0 {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "input is required",
				"type":    "invalid_request_error",
				"param":   "input",
			},
		}, http.StatusBadRequest)
		return
	}

	providerKey, actualModel, err := parseModelID(req.Model)
	if err != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": err.Error(),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusBadRequest)
		return
	}

	if !auth.isModelAllowed(providerKey, req.Model) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("token does not have access to model %q", req.Model),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusForbidden)
		return
	}

	if limitMessage, resetErr := s.checkTokenLimits(r.Context(), auth); resetErr != nil {
		slog.Error("token limit check failed", "error", resetErr)
	} else if limitMessage != "" {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": limitMessage,
				"type":    "tokens",
				"code":    "rate_limit_exceeded",
			},
		}, http.StatusTooManyRequests)
		return
	}

	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q not found", providerKey),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusNotFound)
		return
	}

	embProvider, ok := info.provider.(service.EmbeddingProvider)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q does not support embeddings", providerKey),
				"type":    "invalid_request_error",
				"code":    "unsupported_operation",
			},
		}, http.StatusNotImplemented)
		return
	}

	dimensions := req.Dimensions
	if dimensions != nil && *dimensions == 0 {
		dimensions = nil
	}

	callStart := time.Now()
	resp, err := embProvider.CreateEmbedding(r.Context(), service.EmbeddingRequest{
		Input:          inputs,
		Model:          actualModel,
		EncodingFormat: req.EncodingFormat,
		Dimensions:     dimensions,
		User:           req.User,
	})
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		slog.Error("embeddings provider call failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, req.Model, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}

	var data []embeddingDatum
	if req.EncodingFormat == "base64" {
		encoded := resp.Base64Embeddings
		if len(encoded) == 0 {
			encoded = make([]string, len(resp.Embeddings))
			for i, embedding := range resp.Embeddings {
				encoded[i] = encodeEmbeddingBase64(embedding)
			}
		}
		data = make([]embeddingDatum, len(encoded))
		for i, embedding := range encoded {
			data[i] = embeddingDatum{Object: "embedding", Index: i, Embedding: embedding}
		}
	} else {
		data = make([]embeddingDatum, len(resp.Embeddings))
		for i, embedding := range resp.Embeddings {
			data[i] = embeddingDatum{Object: "embedding", Index: i, Embedding: embedding}
		}
	}

	out := embeddingsResponse{
		Object: "list",
		Data:   data,
		Model:  req.Model,
		Usage: embeddingsUsage{
			PromptTokens: resp.Usage.PromptTokens,
			TotalTokens:  resp.Usage.TotalTokenCount(),
		},
	}

	s.recordUsageAsync(r.Context(), auth, req.Model, resp.Usage, latencyMs, "ok", "", "")
	httpResponseJSON(w, out, http.StatusOK)
}

// encodeEmbeddingBase64 matches OpenAI's base64 representation: contiguous
// little-endian IEEE-754 float32 values.
func encodeEmbeddingBase64(embedding []float64) string {
	raw := make([]byte, len(embedding)*4)
	for i, value := range embedding {
		binary.LittleEndian.PutUint32(raw[i*4:], math.Float32bits(float32(value)))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// parseEmbeddingsInput accepts either a single string, an array of strings,
// an array of token-id arrays ([][]int), or a single token-id array ([]int).
// We currently support text inputs only; token-id arrays return an error.
func parseEmbeddingsInput(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("input is required")
	}
	// Try single string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil, fmt.Errorf("input must not be an empty string")
		}
		return []string{s}, nil
	}
	// Try array of strings.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	// Token-id inputs are valid in the OpenAI spec but we don't tokenise
	// for the upstream provider here; reject explicitly so the client
	// knows to send text.
	return nil, fmt.Errorf("input must be a string or an array of strings (token-id inputs are not supported by this gateway)")
}
