package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// apiBaseURL derives the API root from the chat completions URL.
// e.g. "https://api.openai.com/v1/chat/completions" → "https://api.openai.com/v1"
func (p *Provider) apiBaseURL() string {
	base := p.BaseURL
	base = strings.TrimSuffix(base, "/")
	base = strings.TrimSuffix(base, "/chat/completions")
	return base
}

// doJSON sends a JSON request and decodes the JSON response.
func (p *Provider) doJSON(ctx context.Context, method, url string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		token := p.APIKey
		if p.tokenSource != nil {
			t, err := p.tokenSource.Token(ctx)
			if err != nil {
				return fmt.Errorf("token source: %w", err)
			}
			token = t
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.client.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}

	return nil
}

// ─── Image Generation ───

// imagesRequest is the OpenAI images/generations API request.
type imagesRequest struct {
	Prompt         string `json:"prompt"`
	Model          string `json:"model,omitempty"`
	N              int    `json:"n,omitempty"`
	Size           string `json:"size,omitempty"`
	Quality        string `json:"quality,omitempty"`
	Style          string `json:"style,omitempty"`
	ResponseFormat string `json:"response_format,omitempty"`
}

// imagesResponse is the OpenAI images/generations API response.
type imagesResponse struct {
	Data []struct {
		URL           string `json:"url"`
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
}

// GenerateImage implements service.ImageProvider.
func (p *Provider) GenerateImage(ctx context.Context, req service.ImageGenerateRequest) (*service.ImageResponse, error) {
	url := p.apiBaseURL() + "/images/generations"

	apiReq := imagesRequest{
		Prompt:         req.Prompt,
		Model:          req.Model,
		N:              req.N,
		Size:           req.Size,
		Quality:        req.Quality,
		Style:          req.Style,
		ResponseFormat: "url", // default to URL
	}

	if apiReq.Model == "" {
		apiReq.Model = "dall-e-3"
	}
	if apiReq.N == 0 {
		apiReq.N = 1
	}
	if apiReq.Size == "" {
		apiReq.Size = "1024x1024"
	}

	var apiResp imagesResponse
	if err := p.doJSON(ctx, "POST", url, apiReq, &apiResp); err != nil {
		return nil, fmt.Errorf("generate image: %w", err)
	}

	images := make([]service.GeneratedImage, len(apiResp.Data))
	for i, img := range apiResp.Data {
		images[i] = service.GeneratedImage{
			URL:           img.URL,
			Base64:        img.B64JSON,
			RevisedPrompt: img.RevisedPrompt,
		}
	}

	return &service.ImageResponse{Images: images}, nil
}

// ─── Text-to-Speech ───

// GenerateAudio implements service.AudioProvider.
func (p *Provider) GenerateAudio(ctx context.Context, req service.AudioGenerateRequest) (*service.AudioResponse, error) {
	url := p.apiBaseURL() + "/audio/speech"

	apiReq := map[string]any{
		"input": req.Input,
		"model": req.Model,
		"voice": req.Voice,
	}
	if apiReq["model"] == "" {
		apiReq["model"] = "tts-1"
	}
	if apiReq["voice"] == "" {
		apiReq["voice"] = "alloy"
	}
	if req.ResponseFormat != "" {
		apiReq["response_format"] = req.ResponseFormat
	}
	if req.Speed > 0 {
		apiReq["speed"] = req.Speed
	}

	data, err := json.Marshal(apiReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		token := p.APIKey
		if p.tokenSource != nil {
			t, terr := p.tokenSource.Token(ctx)
			if terr != nil {
				return nil, fmt.Errorf("token source: %w", terr)
			}
			token = t
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.client.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// TTS returns raw audio bytes (not JSON).
	encoded := encodeBase64(body)

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "audio/mpeg"
	}

	return &service.AudioResponse{
		AudioBase64: encoded,
		ContentType: contentType,
	}, nil
}

// ─── Speech-to-Text ───

// transcriptionResponse is the OpenAI transcription API response.
type transcriptionResponse struct {
	Text     string  `json:"text"`
	Language string  `json:"language"`
	Duration float64 `json:"duration"`
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

// TranscribeAudio implements service.AudioProvider.
func (p *Provider) TranscribeAudio(ctx context.Context, req service.AudioTranscribeRequest) (*service.AudioTranscribeResponse, error) {
	url := p.apiBaseURL() + "/audio/transcriptions"

	// Whisper API requires multipart/form-data. For simplicity with base64 input,
	// we first decode the base64 audio, then send as multipart.
	audioBytes, err := decodeBase64(req.AudioBase64)
	if err != nil {
		return nil, fmt.Errorf("decode audio base64: %w", err)
	}

	// Build multipart form.
	var buf bytes.Buffer
	writer := newMultipartWriter(&buf)

	// Add audio file.
	ext := extensionFromContentType(req.ContentType)
	part, err := writer.CreateFormFile("file", "audio"+ext)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(audioBytes); err != nil {
		return nil, fmt.Errorf("write audio: %w", err)
	}

	// Add model.
	model := req.Model
	if model == "" {
		model = "whisper-1"
	}
	if err := writer.WriteField("model", model); err != nil {
		return nil, fmt.Errorf("write model field: %w", err)
	}

	// Add optional fields.
	if req.Language != "" {
		_ = writer.WriteField("language", req.Language)
	}
	if req.Prompt != "" {
		_ = writer.WriteField("prompt", req.Prompt)
	}

	respFormat := req.ResponseFormat
	if respFormat == "" {
		respFormat = "verbose_json"
	}
	_ = writer.WriteField("response_format", respFormat)

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	if p.APIKey != "" {
		token := p.APIKey
		if p.tokenSource != nil {
			t, terr := p.tokenSource.Token(ctx)
			if terr != nil {
				return nil, fmt.Errorf("token source: %w", terr)
			}
			token = t
		}
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.client.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var apiResp transcriptionResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		// For non-JSON formats (text, srt, vtt), return raw text.
		return &service.AudioTranscribeResponse{
			Text: string(body),
		}, nil
	}

	segments := make([]service.TranscriptionSegment, len(apiResp.Segments))
	for i, seg := range apiResp.Segments {
		segments[i] = service.TranscriptionSegment{
			Start: seg.Start,
			End:   seg.End,
			Text:  seg.Text,
		}
	}

	return &service.AudioTranscribeResponse{
		Text:     apiResp.Text,
		Language: apiResp.Language,
		Duration: apiResp.Duration,
		Segments: segments,
	}, nil
}

// ─── Embeddings ───

// embeddingRequest is the OpenAI embeddings API request.
type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embeddingResponse is the OpenAI embeddings API response.
type embeddingResponse struct {
	Data []struct {
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// CreateEmbedding implements service.EmbeddingProvider.
func (p *Provider) CreateEmbedding(ctx context.Context, req service.EmbeddingRequest) (*service.EmbeddingResponse, error) {
	url := p.apiBaseURL() + "/embeddings"

	model := req.Model
	if model == "" {
		model = "text-embedding-3-small"
	}

	apiReq := embeddingRequest{
		Input: req.Input,
		Model: model,
	}

	var apiResp embeddingResponse
	if err := p.doJSON(ctx, "POST", url, apiReq, &apiResp); err != nil {
		return nil, fmt.Errorf("create embedding: %w", err)
	}

	embeddings := make([][]float64, len(apiResp.Data))
	for i, d := range apiResp.Data {
		embeddings[i] = d.Embedding
	}

	return &service.EmbeddingResponse{
		Embeddings: embeddings,
		Model:      apiResp.Model,
		Usage: service.Usage{
			PromptTokens: apiResp.Usage.PromptTokens,
			TotalTokens:  apiResp.Usage.TotalTokens,
		},
	}, nil
}
