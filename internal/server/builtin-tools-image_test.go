package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// A 1x1 PNG.
var generateImageTestPNG, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

type imageCaptureProvider struct {
	req    service.ImageGenerateRequest
	images []service.GeneratedImage
}

func (p *imageCaptureProvider) Chat(context.Context, string, []service.Message, []service.Tool, *service.ChatOptions) (*service.LLMResponse, error) {
	return nil, nil
}

func (p *imageCaptureProvider) GenerateImage(_ context.Context, req service.ImageGenerateRequest) (*service.ImageResponse, error) {
	p.req = req
	return &service.ImageResponse{Images: p.images}, nil
}

func TestGenerateImageToolWritesToWorkDir(t *testing.T) {
	provider := &imageCaptureProvider{images: []service.GeneratedImage{{Base64: base64.StdEncoding.EncodeToString(generateImageTestPNG), RevisedPrompt: "a fox, watercolor"}}}
	s := &Server{providers: map[string]ProviderInfo{"openai-codex": {provider: provider, providerType: "openai"}}}
	dir := t.TempDir()
	ctx := workflow.ContextWithWorkDir(t.Context(), dir)

	raw, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai-codex", "prompt": "a fox", "n": float64(9), "background": "transparent"})
	if err != nil {
		t.Fatal(err)
	}
	if provider.req.Model != "gpt-image-2" || provider.req.N != generateImageMaxCount || provider.req.Background != "transparent" {
		t.Fatalf("request = %#v", provider.req)
	}
	var out struct {
		Model   string   `json:"model"`
		Files   []string `json:"files"`
		Revised []string `json:"revised_prompts"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	if out.Model != "openai-codex/gpt-image-2" || len(out.Files) != 1 || len(out.Revised) != 1 {
		t.Fatalf("result = %s", raw)
	}
	if filepath.Dir(out.Files[0]) != dir || !strings.HasSuffix(out.Files[0], ".png") {
		t.Fatalf("file outside work dir: %s", out.Files[0])
	}
	data, err := os.ReadFile(out.Files[0])
	if err != nil || string(data) != string(generateImageTestPNG) {
		t.Fatalf("saved image differs: %v", err)
	}
}

func TestGenerateImageToolExplicitModel(t *testing.T) {
	provider := &imageCaptureProvider{images: []service.GeneratedImage{{Base64: base64.StdEncoding.EncodeToString(generateImageTestPNG)}}}
	s := &Server{providers: map[string]ProviderInfo{"openai": {provider: provider, providerType: "openai"}}}
	ctx := workflow.ContextWithWorkDir(t.Context(), t.TempDir())
	if _, err := s.execGenerateImage(ctx, map[string]any{"provider": "openai/gpt-image-1.5", "prompt": "x"}); err != nil {
		t.Fatal(err)
	}
	if provider.req.Model != "gpt-image-1.5" || provider.req.N != 1 {
		t.Fatalf("request = %#v", provider.req)
	}
}

func TestGenerateImageToolErrors(t *testing.T) {
	tests := []struct {
		name     string
		provider service.LLMProvider
		args     map[string]any
		want     string
	}{
		{name: "missing_prompt", provider: &imageCaptureProvider{}, args: map[string]any{"provider": "p"}, want: "prompt is required"},
		{name: "chat_only", provider: &embeddingCaptureProvider{}, args: map[string]any{"provider": "p", "prompt": "x"}, want: "does not support image generation"},
		{name: "not_an_image", provider: &imageCaptureProvider{images: []service.GeneratedImage{{Base64: base64.StdEncoding.EncodeToString([]byte("<svg></svg>"))}}}, args: map[string]any{"provider": "p", "prompt": "x"}, want: "unsupported image type"},
		{name: "no_images", provider: &imageCaptureProvider{}, args: map[string]any{"provider": "p", "prompt": "x"}, want: "no images"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{providers: map[string]ProviderInfo{"p": {provider: tt.provider}}}
			ctx := workflow.ContextWithWorkDir(t.Context(), t.TempDir())
			_, err := s.execGenerateImage(ctx, tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestGenerateImageToolProviderSelection(t *testing.T) {
	png := []service.GeneratedImage{{Base64: base64.StdEncoding.EncodeToString(generateImageTestPNG)}}
	t.Run("single_provider_is_used_when_omitted", func(t *testing.T) {
		provider := &imageCaptureProvider{images: png}
		s := &Server{providers: map[string]ProviderInfo{
			"openai": {provider: provider, providerType: "openai"},
			"claude": {provider: &embeddingCaptureProvider{}, providerType: "anthropic"},
		}}
		ctx := workflow.ContextWithWorkDir(t.Context(), t.TempDir())
		if _, err := s.execGenerateImage(ctx, map[string]any{"prompt": "x"}); err != nil {
			t.Fatal(err)
		}
		if provider.req.Model != "gpt-image-2" {
			t.Fatalf("request = %#v", provider.req)
		}
	})
	t.Run("several_providers_are_listed", func(t *testing.T) {
		s := &Server{providers: map[string]ProviderInfo{
			"openai":  {provider: &imageCaptureProvider{images: png}, providerType: "openai"},
			"minimax": {provider: &imageCaptureProvider{images: png}, providerType: "minimax"},
		}}
		_, err := s.execGenerateImage(workflow.ContextWithWorkDir(t.Context(), t.TempDir()), map[string]any{"prompt": "x"})
		if err == nil || !strings.Contains(err.Error(), "minimax, openai") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("none_available", func(t *testing.T) {
		s := &Server{providers: map[string]ProviderInfo{"claude": {provider: &embeddingCaptureProvider{}}}}
		_, err := s.execGenerateImage(workflow.ContextWithWorkDir(t.Context(), t.TempDir()), map[string]any{"prompt": "x"})
		if err == nil || !strings.Contains(err.Error(), "no image-capable provider") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("minimax_default_model", func(t *testing.T) {
		provider := &imageCaptureProvider{images: png}
		s := &Server{providers: map[string]ProviderInfo{"mm": {provider: provider, providerType: "minimax"}}}
		if _, err := s.execGenerateImage(workflow.ContextWithWorkDir(t.Context(), t.TempDir()), map[string]any{"provider": "mm", "prompt": "x"}); err != nil {
			t.Fatal(err)
		}
		if provider.req.Model != "image-01" {
			t.Fatalf("model = %q", provider.req.Model)
		}
	})
}

func TestGenerateImageMCPConfig(t *testing.T) {
	var base service.Tool
	for _, tool := range builtinTools {
		if tool.Name == "generate_image" {
			base = service.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}
		}
	}
	cfg := &service.ImageGenerationConfig{Provider: "openai", Model: "gpt-image-1.5", Quality: "medium", Size: "1536x1024"}

	tool := generateImageToolForConfig(base, cfg)
	props := tool.InputSchema["properties"].(map[string]any)
	if _, ok := props["provider"]; ok {
		t.Fatal("pinned provider is still advertised")
	}
	if _, ok := props["model"]; ok {
		t.Fatal("pinned model is still advertised")
	}
	if _, ok := base.InputSchema["properties"].(map[string]any)["provider"]; !ok {
		t.Fatal("base schema was mutated")
	}
	if !strings.Contains(tool.Description, "openai (gpt-image-1.5)") {
		t.Fatalf("description = %q", tool.Description)
	}
	if got := generateImageToolForConfig(base, nil); got.Description != base.Description {
		t.Fatal("unconfigured tool changed")
	}

	args := map[string]any{"provider": "other", "model": "dall-e-3", "prompt": "x", "quality": "high"}
	got := applyImageGenerationConfig(args, cfg)
	if got["provider"] != "openai" || got["model"] != "gpt-image-1.5" || got["quality"] != "high" || got["size"] != "1536x1024" {
		t.Fatalf("args = %#v", got)
	}
	if args["provider"] != "other" {
		t.Fatal("caller args mutated")
	}
}

func TestImagePreviewDownscales(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2048, 1024))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range src.Pix {
		src.Pix[i] = byte(rng.Uint32())
		if i%4 == 3 {
			src.Pix[i] = 0xff
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	data, mimeType := imagePreview(buf.Bytes())
	if mimeType != "image/jpeg" || len(data) >= buf.Len() {
		t.Fatalf("preview %s %d bytes (original %d)", mimeType, len(data), buf.Len())
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != imagePreviewMaxSide || cfg.Height != imagePreviewMaxSide/2 {
		t.Fatalf("preview size = %dx%d, %v", cfg.Width, cfg.Height, err)
	}

	src.Pix[3] = 0 // one transparent pixel keeps PNG
	buf.Reset()
	_ = png.Encode(&buf, src)
	if _, mimeType = imagePreview(buf.Bytes()); mimeType != "image/png" {
		t.Fatalf("transparent preview = %s", mimeType)
	}
}
