package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
		{name: "missing_provider", provider: &imageCaptureProvider{}, args: map[string]any{"prompt": "x"}, want: "provider is required"},
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
