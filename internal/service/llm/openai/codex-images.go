package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// CodexDefaultImageModel is the model the Codex CLI's image tool requests.
const CodexDefaultImageModel = "gpt-image-2"

// A single base64 image can reach tens of megabytes; Codex itself bounds a
// decoded image at 32 MiB.
const codexImageResponseMaxBytes = 128 << 20

type codexImageRequest struct {
	Images     []codexImageURL `json:"images,omitempty"`
	Prompt     string          `json:"prompt"`
	Model      string          `json:"model"`
	N          int             `json:"n,omitempty"`
	Size       string          `json:"size,omitempty"`
	Quality    string          `json:"quality,omitempty"`
	Background string          `json:"background,omitempty"`
}

// codexImageURL is one edit input; the Codex endpoint takes data: URLs in a
// JSON body rather than the public API's multipart upload.
type codexImageURL struct {
	ImageURL string `json:"image_url"`
}

type codexImageResponse struct {
	Data []struct {
		B64JSON       string `json:"b64_json"`
		RevisedPrompt string `json:"revised_prompt"`
	} `json:"data"`
	OutputFormat string `json:"output_format"`
	Usage        *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// GenerateImage implements service.ImageProvider through the ChatGPT Codex
// images endpoints, the ones the Codex CLI's image tool calls: generations,
// or edits when reference images are supplied. Usage is charged to the
// ChatGPT subscription rather than to API billing.
func (p *CodexProvider) GenerateImage(ctx context.Context, req service.ImageGenerateRequest) (*service.ImageResponse, error) {
	if req.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	model := req.Model
	if model == "" {
		model = CodexDefaultImageModel
	}
	path := "/images/generations"
	var images []codexImageURL
	if len(req.ReferenceImages) > 0 {
		path = "/images/edits"
		for _, ref := range req.ReferenceImages {
			images = append(images, codexImageURL{ImageURL: ref.DataURL()})
		}
	}
	target, err := p.proxyURL(path, "")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(codexImageRequest{
		Images:     images,
		Prompt:     req.Prompt,
		Model:      model,
		N:          req.N,
		Size:       req.Size,
		Quality:    codexImageQuality(req.Quality),
		Background: req.Background,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal Codex image request: %w", err)
	}

	release, err := p.limiter.Acquire(ctx, max(1, len(req.Prompt)/4))
	if err != nil {
		return nil, err
	}
	defer release()

	send := func() (*http.Response, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("build Codex image request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if err := p.authorize(ctx, httpReq); err != nil {
			return nil, err
		}
		return p.httpClient.Do(httpReq)
	}
	resp, err := send()
	if err != nil {
		return nil, fmt.Errorf("Codex image request failed: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp, err = p.recoverUnauthorized(ctx, resp, send)
		if err != nil {
			return nil, fmt.Errorf("Codex image request recovery failed: %w", err)
		}
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, codexImageResponseMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Codex image response: %w", err)
	}
	if len(respBody) > codexImageResponseMaxBytes {
		return nil, fmt.Errorf("Codex image response exceeds %d bytes", codexImageResponseMaxBytes)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, codexHTTPError(resp, respBody)
	}

	var parsed codexImageResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("parse Codex image response: %w", err)
	}
	out := &service.ImageResponse{}
	for _, image := range parsed.Data {
		if image.B64JSON == "" {
			continue
		}
		out.Images = append(out.Images, service.GeneratedImage{Base64: image.B64JSON, RevisedPrompt: image.RevisedPrompt})
	}
	if len(out.Images) == 0 {
		return nil, fmt.Errorf("Codex image generation returned no image data")
	}
	if parsed.Usage != nil {
		out.Usage = service.Usage{
			PromptTokens:     parsed.Usage.InputTokens,
			CompletionTokens: parsed.Usage.OutputTokens,
			TotalTokens:      parsed.Usage.TotalTokens,
		}
	}
	return out, nil
}

// codexImageQuality maps the DALL-E vocabulary callers commonly send onto the
// GPT Image levels the Codex endpoint accepts.
func codexImageQuality(quality string) string {
	switch quality {
	case "standard":
		return "medium"
	case "hd":
		return "high"
	default:
		return quality
	}
}
