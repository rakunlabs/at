package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Images (POST /gateway/v1/images/generations) ───

// imagesGenRequest mirrors the OpenAI image generation request body.
type imagesGenRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"` // "url" | "b64_json"
	User           string `json:"user,omitempty"`
}

// imagesGenResponse mirrors the OpenAI image generation response.
type imagesGenResponse struct {
	Created int64               `json:"created"`
	Data    []imagesGenDatum    `json:"data"`
	Usage   *imagesUsage        `json:"usage,omitempty"`
}

type imagesGenDatum struct {
	URL           string `json:"url,omitempty"`
	B64JSON       string `json:"b64_json,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}

type imagesUsage struct {
	TotalTokens int `json:"total_tokens,omitempty"`
}

// Images handles POST /gateway/v1/images/generations.
func (s *Server) Images(w http.ResponseWriter, r *http.Request) {
	auth, providerKey, actualModel, fullModel, info, ok := s.resolveMediaProvider(w, r)
	if !ok {
		return
	}

	var req imagesGenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		gatewayBadRequest(w, fmt.Sprintf("invalid request body: %v", err), "", "")
		return
	}
	if req.Prompt == "" {
		gatewayBadRequest(w, "prompt is required", "prompt", "")
		return
	}

	imgProvider, ok := info.provider.(service.ImageProvider)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q does not support image generation", providerKey),
				"type":    "invalid_request_error",
				"code":    "unsupported_operation",
			},
		}, http.StatusNotImplemented)
		return
	}

	callStart := time.Now()
	resp, err := imgProvider.GenerateImage(r.Context(), service.ImageGenerateRequest{
		Prompt:  req.Prompt,
		Model:   actualModel,
		N:       req.N,
		Size:    req.Size,
		Quality: req.Quality,
		Style:   req.Style,
	})
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		slog.Error("image generation failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}

	data := make([]imagesGenDatum, 0, len(resp.Images))
	for _, img := range resp.Images {
		data = append(data, imagesGenDatum{
			URL:           img.URL,
			B64JSON:       img.Base64,
			RevisedPrompt: img.RevisedPrompt,
		})
	}
	out := imagesGenResponse{
		Created: time.Now().Unix(),
		Data:    data,
	}
	if resp.Usage.TotalTokens > 0 || resp.Usage.PromptTokens > 0 {
		out.Usage = &imagesUsage{TotalTokens: resp.Usage.TotalTokenCount()}
	}

	s.recordUsageAsync(r.Context(), auth, fullModel, resp.Usage, latencyMs, "ok", "", "")
	httpResponseJSON(w, out, http.StatusOK)
}

// ─── Audio TTS (POST /gateway/v1/audio/speech) ───

// audioSpeechRequest mirrors the OpenAI TTS request body.
type audioSpeechRequest struct {
	Model          string  `json:"model"`
	Input          string  `json:"input"`
	Voice          string  `json:"voice,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"` // "mp3" (default) | "opus" | "aac" | "flac" | "wav" | "pcm"
	Speed          float64 `json:"speed,omitempty"`
}

// AudioSpeech handles POST /gateway/v1/audio/speech. Returns raw audio bytes
// (Content-Type matches the requested format).
func (s *Server) AudioSpeech(w http.ResponseWriter, r *http.Request) {
	auth, providerKey, actualModel, fullModel, info, ok := s.resolveMediaProvider(w, r)
	if !ok {
		return
	}

	var req audioSpeechRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		gatewayBadRequest(w, fmt.Sprintf("invalid request body: %v", err), "", "")
		return
	}
	if req.Input == "" {
		gatewayBadRequest(w, "input is required", "input", "")
		return
	}

	audioProvider, ok := info.provider.(service.AudioProvider)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q does not support text-to-speech", providerKey),
				"type":    "invalid_request_error",
				"code":    "unsupported_operation",
			},
		}, http.StatusNotImplemented)
		return
	}

	callStart := time.Now()
	resp, err := audioProvider.GenerateAudio(r.Context(), service.AudioGenerateRequest{
		Input:          req.Input,
		Model:          actualModel,
		Voice:          req.Voice,
		ResponseFormat: req.ResponseFormat,
		Speed:          req.Speed,
	})
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		slog.Error("audio generation failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}

	// Decode the base64-encoded audio and write raw bytes back, matching OpenAI's
	// TTS endpoint which returns the audio as the response body.
	audioBytes, err := base64.StdEncoding.DecodeString(resp.AudioBase64)
	if err != nil {
		slog.Error("audio decode failed", "provider", providerKey, "error", err)
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": "failed to decode audio response",
				"type":    "server_error",
			},
		}, http.StatusInternalServerError)
		return
	}

	ct := resp.ContentType
	if ct == "" {
		ct = "audio/mpeg"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(audioBytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audioBytes)

	// TTS responses don't carry token usage; pass empty Usage.
	s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, latencyMs, "ok", "", "")
}

// ─── Audio transcription (POST /gateway/v1/audio/transcriptions) ───

// AudioTranscriptions handles POST /gateway/v1/audio/transcriptions.
// Accepts multipart/form-data with fields: file, model, language?, prompt?,
// response_format? (json / verbose_json / text / srt / vtt — default verbose_json).
func (s *Server) AudioTranscriptions(w http.ResponseWriter, r *http.Request) {
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

	// Parse multipart. 64 MB cap matches OpenAI's 25 MB plus headroom.
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		gatewayBadRequest(w, fmt.Sprintf("failed to parse multipart form: %v", err), "", "")
		return
	}

	model := r.FormValue("model")
	if model == "" {
		gatewayBadRequest(w, "model field is required", "model", "")
		return
	}

	providerKey, actualModel, err := parseModelID(model)
	if err != nil {
		gatewayBadRequest(w, err.Error(), "model", "model_not_found")
		return
	}
	if !auth.isModelAllowed(providerKey, model) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("token does not have access to model %q", model),
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

	audioProvider, ok := info.provider.(service.AudioProvider)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q does not support audio transcription", providerKey),
				"type":    "invalid_request_error",
				"code":    "unsupported_operation",
			},
		}, http.StatusNotImplemented)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		gatewayBadRequest(w, "file field is required", "file", "")
		return
	}
	defer file.Close()

	audioBytes, err := io.ReadAll(file)
	if err != nil {
		gatewayBadRequest(w, fmt.Sprintf("failed to read uploaded file: %v", err), "file", "")
		return
	}
	if len(audioBytes) == 0 {
		gatewayBadRequest(w, "uploaded file is empty", "file", "")
		return
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/mpeg"
	}

	respFormat := r.FormValue("response_format")
	if respFormat == "" {
		respFormat = "verbose_json"
	}

	callStart := time.Now()
	resp, err := audioProvider.TranscribeAudio(r.Context(), service.AudioTranscribeRequest{
		AudioBase64:    base64.StdEncoding.EncodeToString(audioBytes),
		ContentType:    contentType,
		Model:          actualModel,
		Language:       r.FormValue("language"),
		Prompt:         r.FormValue("prompt"),
		ResponseFormat: respFormat,
	})
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		slog.Error("transcription failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, model, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}

	s.recordUsageAsync(r.Context(), auth, model, service.Usage{}, latencyMs, "ok", "", "")

	// Match OpenAI's response shape based on response_format.
	switch respFormat {
	case "text":
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp.Text))
	case "srt", "vtt":
		// We don't synthesize SRT/VTT here; fall back to plain text.
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp.Text))
	case "verbose_json":
		out := map[string]any{
			"text":     resp.Text,
			"language": resp.Language,
			"duration": resp.Duration,
		}
		if len(resp.Segments) > 0 {
			segments := make([]map[string]any, len(resp.Segments))
			for i, seg := range resp.Segments {
				segments[i] = map[string]any{
					"id":    i,
					"start": seg.Start,
					"end":   seg.End,
					"text":  seg.Text,
				}
			}
			out["segments"] = segments
		}
		httpResponseJSON(w, out, http.StatusOK)
	default:
		httpResponseJSON(w, map[string]any{"text": resp.Text}, http.StatusOK)
	}
}

// ─── Moderations (POST /gateway/v1/moderations) ───

// moderationsRequest mirrors the OpenAI moderations request body.
type moderationsRequest struct {
	Model string          `json:"model"`
	Input json.RawMessage `json:"input"` // string OR []string
}

// Moderations handles POST /gateway/v1/moderations.
func (s *Server) Moderations(w http.ResponseWriter, r *http.Request) {
	auth, providerKey, actualModel, fullModel, info, ok := s.resolveMediaProvider(w, r)
	if !ok {
		return
	}

	var req moderationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		gatewayBadRequest(w, fmt.Sprintf("invalid request body: %v", err), "", "")
		return
	}

	inputs, err := parseEmbeddingsInput(req.Input)
	if err != nil {
		gatewayBadRequest(w, err.Error(), "input", "")
		return
	}

	modProvider, ok := info.provider.(service.ModerationProvider)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q does not support moderations", providerKey),
				"type":    "invalid_request_error",
				"code":    "unsupported_operation",
			},
		}, http.StatusNotImplemented)
		return
	}

	callStart := time.Now()
	resp, err := modProvider.Moderate(r.Context(), service.ModerationRequest{
		Input: inputs,
		Model: actualModel,
	})
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		slog.Error("moderation failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}

	s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, latencyMs, "ok", "", "")
	httpResponseJSON(w, resp, http.StatusOK)
}

// ─── Rerank (POST /gateway/v1/rerank) — Cohere-shaped ───

// rerankRequest mirrors the Cohere rerank request body.
type rerankRequest struct {
	Model           string   `json:"model"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments *bool    `json:"return_documents,omitempty"`
}

// Rerank handles POST /gateway/v1/rerank.
func (s *Server) Rerank(w http.ResponseWriter, r *http.Request) {
	auth, providerKey, actualModel, fullModel, info, ok := s.resolveMediaProvider(w, r)
	if !ok {
		return
	}

	var req rerankRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		gatewayBadRequest(w, fmt.Sprintf("invalid request body: %v", err), "", "")
		return
	}
	if req.Query == "" {
		gatewayBadRequest(w, "query is required", "query", "")
		return
	}
	if len(req.Documents) == 0 {
		gatewayBadRequest(w, "documents must contain at least one string", "documents", "")
		return
	}

	reranker, ok := info.provider.(service.RerankProvider)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q does not support reranking", providerKey),
				"type":    "invalid_request_error",
				"code":    "unsupported_operation",
			},
		}, http.StatusNotImplemented)
		return
	}

	callStart := time.Now()
	resp, err := reranker.Rerank(r.Context(), service.RerankRequest{
		Model:           actualModel,
		Query:           req.Query,
		Documents:       req.Documents,
		TopN:            req.TopN,
		ReturnDocuments: req.ReturnDocuments,
	})
	latencyMs := time.Since(callStart).Milliseconds()
	if err != nil {
		slog.Error("rerank failed", "provider", providerKey, "error", err)
		s.recordUsageAsync(r.Context(), auth, fullModel, service.Usage{}, latencyMs, "error", classifyHTTPError(err), err.Error())
		status, body := classifyGatewayError(err)
		addGatewayRateLimitHeaders(w, err)
		httpResponseJSON(w, body, status)
		return
	}

	s.recordUsageAsync(r.Context(), auth, fullModel, resp.Usage, latencyMs, "ok", "", "")
	httpResponseJSON(w, resp, http.StatusOK)
}

// ─── Health (GET /gateway/v1/health, /gateway/v1/health/{provider}) ───

// HealthOverall handles GET /gateway/v1/health.
// Returns gateway readiness plus a per-provider status map. No auth required —
// liveness/readiness probes shouldn't need credentials.
func (s *Server) HealthOverall(w http.ResponseWriter, r *http.Request) {
	s.providerMu.RLock()
	providers := make(map[string]string, len(s.providers))
	for k := range s.providers {
		providers[k] = "ok"
	}
	s.providerMu.RUnlock()

	httpResponseJSON(w, map[string]any{
		"status":    "ok",
		"providers": providers,
		"version":   s.version,
	}, http.StatusOK)
}

// HealthProvider handles GET /gateway/v1/health/{provider}.
// Returns 200 + {status:"ok"} when the provider is configured, 404 otherwise.
// We don't dial the upstream — that would be expensive and quota-burning.
// Callers needing real upstream health should issue a `models.list` call.
func (s *Server) HealthProvider(w http.ResponseWriter, r *http.Request) {
	providerKey := r.PathValue("provider")
	info, ok := s.getProviderInfo(providerKey)
	if !ok {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q not found", providerKey),
				"type":    "invalid_request_error",
				"code":    "not_found",
			},
		}, http.StatusNotFound)
		return
	}

	httpResponseJSON(w, map[string]any{
		"status":        "ok",
		"provider":      providerKey,
		"provider_type": info.providerType,
		"default_model": info.defaultModel,
		"model_count":   len(info.models),
	}, http.StatusOK)
}

// ─── Helpers ───

// resolveMediaProvider runs the same auth/model/access/budget pipeline as
// ChatCompletions for endpoints that accept a top-level JSON body with a
// `model` field.
//
// It reads the request body to extract the model (returning the body to the
// stream via a buffered NopCloser so the caller can decode it again),
// resolves the provider, then returns everything the caller needs.
//
// When ok=false the response has already been written and the caller must
// return immediately.
func (s *Server) resolveMediaProvider(w http.ResponseWriter, r *http.Request) (
	auth *authResult, providerKey, actualModel, fullModel string, info ProviderInfo, ok bool,
) {
	authR, authErr := s.authenticateRequest(r)
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

	// Peek the body to extract the model field, then put it back for the caller.
	model, _, perr := extractProxyBodyModel(r)
	if perr != nil {
		gatewayBadRequest(w, fmt.Sprintf("failed to read request body: %v", perr), "", "")
		return
	}
	if model == "" {
		gatewayBadRequest(w, "model field is required", "model", "")
		return
	}

	pKey, aModel, mErr := parseModelID(model)
	if mErr != nil {
		gatewayBadRequest(w, mErr.Error(), "model", "model_not_found")
		return
	}

	if !authR.isModelAllowed(pKey, model) {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("token does not have access to model %q", model),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusForbidden)
		return
	}

	if limitMessage, resetErr := s.checkTokenLimits(r.Context(), authR); resetErr != nil {
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

	pInfo, found := s.getProviderInfo(pKey)
	if !found {
		httpResponseJSON(w, map[string]any{
			"error": map[string]any{
				"message": fmt.Sprintf("provider %q not found", pKey),
				"type":    "invalid_request_error",
				"param":   "model",
				"code":    "model_not_found",
			},
		}, http.StatusNotFound)
		return
	}

	return authR, pKey, aModel, model, pInfo, true
}

func gatewayBadRequest(w http.ResponseWriter, msg, param, code string) {
	body := map[string]any{
		"message": msg,
		"type":    "invalid_request_error",
	}
	if param != "" {
		body["param"] = param
	}
	if code != "" {
		body["code"] = code
	}
	httpResponseJSON(w, map[string]any{"error": body}, http.StatusBadRequest)
}
