package server

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

const embeddingRequestBodyMaxBytes = 16 << 20

// embeddingsRequest mirrors the OpenAI /v1/embeddings request body.
//
// `input` may be a single string OR an array of strings (per OpenAI spec);
// we accept both via json.RawMessage and normalise.
type embeddingsRequest struct {
	Input          json.RawMessage `json:"input"`
	Model          string          `json:"model"`
	EncodingFormat string          `json:"encoding_format,omitempty"` // "float" | "base64"
	Dimensions     *int            `json:"dimensions,omitempty"`      // accepted but only forwarded to providers that support it
	User           string          `json:"user,omitempty"`
	InputType      string          `json:"input_type,omitempty"` // AT extension: search_document | search_query | classification | clustering
}

// embeddingsResponse mirrors the OpenAI /v1/embeddings response body.
type embeddingsResponse struct {
	Object         string           `json:"object"` // "list"
	Data           []embeddingDatum `json:"data"`
	Model          string           `json:"model"`
	Usage          embeddingsUsage  `json:"usage"`
	UsageEstimated bool             `json:"at_usage_estimated,omitempty"`
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, embeddingRequestBodyMaxBytes)).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httpResponseJSON(w, map[string]any{
				"error": map[string]any{
					"message": fmt.Sprintf("request body exceeds %d bytes", embeddingRequestBodyMaxBytes),
					"type":    "invalid_request_error",
					"code":    "request_too_large",
				},
			}, http.StatusRequestEntityTooLarge)
			return
		}
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
	for i, input := range inputs {
		if input == "" {
			httpResponseJSON(w, map[string]any{
				"error": map[string]any{
					"message": fmt.Sprintf("input[%d] must not be an empty string", i),
					"type":    "invalid_request_error",
					"param":   "input",
				},
			}, http.StatusBadRequest)
			return
		}
	}
	if !service.ValidEmbeddingInputType(req.InputType) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "input_type must be one of search_document, search_query, classification, or clustering",
				"type":    "invalid_request_error",
				"param":   "input_type",
			},
		}, http.StatusBadRequest)
		return
	}
	if req.Dimensions != nil && *req.Dimensions < 0 {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "dimensions must be greater than 0 (or 0 to use the model default)",
				"type":    "invalid_request_error",
				"param":   "dimensions",
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

	_, _, info, resolveErr := s.resolveModel(r.Context(), auth, req.Model)
	if resolveErr != nil {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": resolveErr.Error(),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusNotFound)
		return
	}
	if info.embeddingMaxInputs > 0 && len(inputs) > info.embeddingMaxInputs {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("input contains %d texts; provider maximum is %d", len(inputs), info.embeddingMaxInputs),
				"type":    "invalid_request_error",
				"param":   "input",
				"code":    "batch_too_large",
			},
		}, http.StatusBadRequest)
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
	traceID, sessionID := auditTraceInfo(r)
	rawBody, _ := json.Marshal(req)
	resp, err := embProvider.CreateEmbedding(r.Context(), service.EmbeddingRequest{
		Input:          inputs,
		Model:          actualModel,
		EncodingFormat: req.EncodingFormat,
		Dimensions:     dimensions,
		User:           req.User,
		InputType:      req.InputType,
	})
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		s.noteProviderError(providerKey, err)
		slog.Error("embeddings provider call failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, req.Model, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "gateway", endpoint: r.URL.Path,
			traceID: traceID, sessionID: sessionID, userField: req.User,
			requestBody: rawBody, requestedModel: req.Model, fullModel: req.Model,
			latencyMs: latencyMs, status: "error", errCode: classifyHTTPError(err), errMsg: err.Error(),
			name: "embeddings", metadata: embeddingAuditMetadata(inputs, req, false),
		})
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}
	validationReq := service.EmbeddingRequest{
		Input: inputs, EncodingFormat: req.EncodingFormat, Dimensions: dimensions,
	}
	if err := service.ValidateEmbeddingResponse(validationReq, resp); err != nil {
		err = fmt.Errorf("invalid upstream embedding response: %w", err)
		slog.Error("embeddings provider returned invalid response", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, req.Model, service.Usage{}, latencyMs, "error", "invalid_upstream_response", err.Error())
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "gateway", endpoint: r.URL.Path,
			traceID: traceID, sessionID: sessionID, userField: req.User,
			requestBody: rawBody, requestedModel: req.Model, fullModel: req.Model,
			latencyMs: latencyMs, status: "error", errCode: "invalid_upstream_response", errMsg: err.Error(),
			name: "embeddings", metadata: embeddingAuditMetadata(inputs, req, resp.UsageEstimated),
		})
		httpResponseJSON(w, map[string]any{"error": map[string]any{
			"message": err.Error(), "type": "server_error", "code": "invalid_upstream_response",
		}}, http.StatusBadGateway)
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
		UsageEstimated: resp.UsageEstimated,
	}
	if costCents := s.estimateGatewayUsageCostCents(r.Context(), providerKey, actualModel, req.Model, resp.Usage); costCents > 0 {
		w.Header().Set("x-at-response-cost-cents", fmt.Sprintf("%.6f", costCents))
	}

	s.recordUsageAsync(r.Context(), auth, req.Model, resp.Usage, latencyMs, "ok", "", "")
	if responseBody, marshalErr := json.Marshal(out); marshalErr == nil {
		s.recordLLMCallAsync(r.Context(), llmAuditParams{
			auth: auth, source: "gateway", endpoint: r.URL.Path,
			traceID: traceID, sessionID: sessionID, userField: req.User,
			requestBody: rawBody, responseBody: responseBody,
			requestedModel: req.Model, fullModel: req.Model,
			usage: resp.Usage, latencyMs: latencyMs, status: "ok", finishReason: "stop",
			name: "embeddings", metadata: embeddingAuditMetadata(inputs, req, resp.UsageEstimated),
		})
	}
	httpResponseJSON(w, out, http.StatusOK)
}

func embeddingAuditMetadata(inputs []string, req embeddingsRequest, usageEstimated bool) map[string]any {
	inputBytes := 0
	for _, input := range inputs {
		inputBytes += len(input)
	}
	metadata := map[string]any{
		"operation": "embeddings", "input_count": len(inputs), "input_bytes": inputBytes,
		"encoding_format": req.EncodingFormat, "input_type": req.InputType,
		"usage_estimated": usageEstimated,
	}
	if req.Dimensions != nil {
		metadata["dimensions"] = *req.Dimensions
	}
	return metadata
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
