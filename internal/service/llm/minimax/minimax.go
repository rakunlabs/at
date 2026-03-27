// Package minimax implements a provider for the MiniMax AI platform
// (https://www.minimax.io/).
//
// MiniMax recommends using their Anthropic-compatible API for chat, so the
// Provider embeds an antropic.Provider for Chat/ChatStream/Proxy. Image
// generation and text-to-speech use MiniMax's proprietary REST APIs.
//
// Supported capabilities:
//   - Chat (LLMProvider)              — via embedded antropic.Provider (Anthropic Messages API)
//   - ChatStream (LLMStreamProvider)  — via embedded antropic.Provider
//   - Image generation (ImageProvider) — POST /v1/image_generation (native)
//   - Text-to-speech (AudioProvider)   — POST /v1/t2a_v2 (native)
//
// Not supported:
//   - Speech-to-text (no MiniMax API for this)
//   - Embeddings (no MiniMax API for this)
package minimax

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/service"
	antropic "github.com/rakunlabs/at/internal/service/llm/antropic"
)

const (
	// DefaultAnthropicBaseURL is the MiniMax Anthropic-compatible endpoint.
	// The antropic.Provider appends /v1/messages to this base.
	DefaultAnthropicBaseURL = "https://api.minimax.io/anthropic"

	// DefaultNativeAPIBase is the base for MiniMax's proprietary APIs (image, TTS).
	DefaultNativeAPIBase = "https://api.minimax.io/v1"
)

// Provider wraps an Anthropic-compatible provider for chat and adds MiniMax-native
// image generation and TTS support.
type Provider struct {
	// Embedded Anthropic provider handles Chat, ChatStream, and Proxy.
	*antropic.Provider

	apiKey  string
	apiBase string // native API base, e.g. "https://api.minimax.io/v1"
	client  *klient.Client
}

// New creates a MiniMax provider.
//
// The baseURL controls the Anthropic-compatible chat endpoint. If empty,
// defaults to "https://api.minimax.io/anthropic". For backward compatibility,
// if the baseURL contains "/v1/chat/completions" (old OpenAI-style URL), it
// is automatically converted to the Anthropic endpoint.
func New(apiKey, model, baseURL, proxy string, insecureSkipVerify bool, extraHeaders map[string]string) (*Provider, error) {
	anthropicBaseURL := baseURL
	nativeAPIBase := DefaultNativeAPIBase

	if anthropicBaseURL == "" {
		anthropicBaseURL = DefaultAnthropicBaseURL
	}

	// Backward compat: convert old OpenAI-style URLs to Anthropic endpoint.
	// e.g. "https://api.minimax.io/v1/chat/completions" -> "https://api.minimax.io/anthropic"
	if idx := indexOf(anthropicBaseURL, "/v1/chat/completions"); idx != -1 {
		nativeAPIBase = anthropicBaseURL[:idx] + "/v1"
		anthropicBaseURL = anthropicBaseURL[:idx] + "/anthropic"
	} else if idx := indexOf(anthropicBaseURL, "/v1"); idx != -1 && !contains(anthropicBaseURL, "/anthropic") {
		// e.g. "https://api.minimax.io/v1" -> "https://api.minimax.io/anthropic"
		nativeAPIBase = anthropicBaseURL[:idx] + "/v1"
		anthropicBaseURL = anthropicBaseURL[:idx] + "/anthropic"
	} else if contains(anthropicBaseURL, "/anthropic") {
		// Already an Anthropic URL, derive native base.
		// e.g. "https://api.minimax.io/anthropic" -> "https://api.minimax.io/v1"
		if idx := indexOf(anthropicBaseURL, "/anthropic"); idx != -1 {
			nativeAPIBase = anthropicBaseURL[:idx] + "/v1"
		}
	}

	// Create the embedded Anthropic provider for chat.
	// The Anthropic provider makes requests to absolute path "/v1/messages".
	// Go's URL resolution replaces the base path entirely for absolute paths.
	// So we set the base URL to the root (e.g. "https://api.minimax.io") and
	// use a PathPrefixTransport to prepend "/anthropic" to every request path.
	//
	// This makes: base="https://api.minimax.io" + path="/v1/messages"
	//           → transport rewrites to "/anthropic/v1/messages"
	//           → final URL: "https://api.minimax.io/anthropic/v1/messages"
	pathPrefix := ""
	rootBaseURL := anthropicBaseURL
	if idx := indexOf(anthropicBaseURL, "/anthropic"); idx != -1 {
		rootBaseURL = anthropicBaseURL[:idx]
		pathPrefix = anthropicBaseURL[idx:]
	}

	anthProvider, err := antropic.New(apiKey, model, rootBaseURL, proxy, insecureSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("minimax: create anthropic provider: %w", err)
	}

	// Wrap the Anthropic provider's HTTP transport to prepend the path prefix.
	if pathPrefix != "" {
		origTransport := anthProvider.Client.HTTP.Transport
		if origTransport == nil {
			origTransport = http.DefaultTransport
		}
		anthProvider.Client.HTTP.Transport = &pathPrefixTransport{
			base:   origTransport,
			prefix: pathPrefix,
		}
	}

	// Build HTTP client for proprietary API calls (image gen, TTS).
	headers := http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer " + apiKey},
	}

	klientOpts := []klient.OptionClientFn{
		klient.WithBaseURL(nativeAPIBase),
		klient.WithHeaderSet(headers),
		klient.WithDisableRetry(true),
		klient.WithDisableEnvValues(true),
	}
	if proxy != "" {
		klientOpts = append(klientOpts, klient.WithProxy(proxy))
	}
	if insecureSkipVerify {
		klientOpts = append(klientOpts, klient.WithInsecureSkipVerify(true))
	}

	client, err := klient.New(klientOpts...)
	if err != nil {
		return nil, fmt.Errorf("minimax: create http client: %w", err)
	}

	return &Provider{
		Provider: anthProvider,
		apiKey:   apiKey,
		apiBase:  nativeAPIBase,
		client:   client,
	}, nil
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
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

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

// imageGenRequest is the MiniMax /v1/image_generation request.
type imageGenRequest struct {
	Model           string `json:"model"`
	Prompt          string `json:"prompt"`
	AspectRatio     string `json:"aspect_ratio,omitempty"`
	N               int    `json:"n,omitempty"`
	ResponseFormat  string `json:"response_format,omitempty"`
	PromptOptimizer bool   `json:"prompt_optimizer,omitempty"`
}

// imageGenResponse is the MiniMax /v1/image_generation response.
type imageGenResponse struct {
	Data struct {
		ImageURLs []string `json:"image_urls"`
	} `json:"data"`
	Metadata struct {
		SuccessCount int `json:"success_count"`
		FailedCount  int `json:"failed_count"`
	} `json:"metadata"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// GenerateImage implements service.ImageProvider using MiniMax's native image API.
func (p *Provider) GenerateImage(ctx context.Context, req service.ImageGenerateRequest) (*service.ImageResponse, error) {
	url := p.apiBase + "/image_generation"

	// Map OpenAI-style size to MiniMax aspect ratio.
	aspectRatio := sizeToAspectRatio(req.Size)

	n := req.N
	if n == 0 {
		n = 1
	}

	apiReq := imageGenRequest{
		Model:           "image-01",
		Prompt:          req.Prompt,
		AspectRatio:     aspectRatio,
		N:               n,
		ResponseFormat:  "url",
		PromptOptimizer: true,
	}

	if req.Model != "" {
		apiReq.Model = req.Model
	}

	var apiResp imageGenResponse
	if err := p.doJSON(ctx, "POST", url, apiReq, &apiResp); err != nil {
		return nil, fmt.Errorf("minimax image generation: %w", err)
	}

	if apiResp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("minimax image generation: %s (code %d)", apiResp.BaseResp.StatusMsg, apiResp.BaseResp.StatusCode)
	}

	images := make([]service.GeneratedImage, len(apiResp.Data.ImageURLs))
	for i, u := range apiResp.Data.ImageURLs {
		images[i] = service.GeneratedImage{URL: u}
	}

	return &service.ImageResponse{Images: images}, nil
}

// sizeToAspectRatio converts OpenAI-style size strings to MiniMax aspect ratios.
func sizeToAspectRatio(size string) string {
	switch size {
	case "1024x1024", "512x512", "256x256":
		return "1:1"
	case "1792x1024":
		return "16:9"
	case "1024x1792":
		return "9:16"
	default:
		if size != "" {
			return size // Pass through if already an aspect ratio like "16:9"
		}
		return "1:1"
	}
}

// ─── Text-to-Speech ───

// t2aRequest is the MiniMax /v1/t2a_v2 request.
type t2aRequest struct {
	Model        string       `json:"model"`
	Text         string       `json:"text"`
	Stream       bool         `json:"stream"`
	VoiceSetting voiceSetting `json:"voice_setting"`
	AudioSetting audioSetting `json:"audio_setting"`
	OutputFormat string       `json:"output_format,omitempty"`
}

type voiceSetting struct {
	VoiceID string  `json:"voice_id"`
	Speed   float64 `json:"speed,omitempty"`
	Vol     float64 `json:"vol,omitempty"`
	Pitch   int     `json:"pitch,omitempty"`
}

type audioSetting struct {
	SampleRate int    `json:"sample_rate,omitempty"`
	Bitrate    int    `json:"bitrate,omitempty"`
	Format     string `json:"format,omitempty"`
	Channel    int    `json:"channel,omitempty"`
}

// t2aResponse is the MiniMax /v1/t2a_v2 response.
type t2aResponse struct {
	Data struct {
		Audio string `json:"audio"` // hex-encoded audio data
	} `json:"data"`
	ExtraInfo struct {
		AudioLength     int    `json:"audio_length"`
		AudioSampleRate int    `json:"audio_sample_rate"`
		AudioSize       int    `json:"audio_size"`
		AudioFormat     string `json:"audio_format"`
	} `json:"extra_info"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// GenerateAudio implements service.AudioProvider using MiniMax's native TTS API.
func (p *Provider) GenerateAudio(ctx context.Context, req service.AudioGenerateRequest) (*service.AudioResponse, error) {
	url := p.apiBase + "/t2a_v2"

	model := req.Model
	if model == "" {
		model = "speech-2.8-hd"
	}

	voice := req.Voice
	if voice == "" {
		voice = "English_expressive_narrator"
	}

	speed := req.Speed
	if speed == 0 {
		speed = 1.0
	}

	audioFormat := "mp3"
	if req.ResponseFormat != "" {
		audioFormat = req.ResponseFormat
	}

	apiReq := t2aRequest{
		Model:  model,
		Text:   req.Input,
		Stream: false,
		VoiceSetting: voiceSetting{
			VoiceID: voice,
			Speed:   speed,
			Vol:     1.0,
		},
		AudioSetting: audioSetting{
			SampleRate: 32000,
			Bitrate:    128000,
			Format:     audioFormat,
			Channel:    1,
		},
		OutputFormat: "hex",
	}

	var apiResp t2aResponse
	if err := p.doJSON(ctx, "POST", url, apiReq, &apiResp); err != nil {
		return nil, fmt.Errorf("minimax tts: %w", err)
	}

	if apiResp.BaseResp.StatusCode != 0 {
		return nil, fmt.Errorf("minimax tts: %s (code %d)", apiResp.BaseResp.StatusMsg, apiResp.BaseResp.StatusCode)
	}

	// MiniMax returns hex-encoded audio. Decode to bytes then encode as base64.
	audioBytes, err := hex.DecodeString(apiResp.Data.Audio)
	if err != nil {
		return nil, fmt.Errorf("minimax tts: decode hex audio: %w", err)
	}

	contentType := "audio/" + audioFormat
	if audioFormat == "mp3" {
		contentType = "audio/mpeg"
	}

	return &service.AudioResponse{
		AudioBase64: encodeBase64(audioBytes),
		ContentType: contentType,
		DurationMs:  int64(apiResp.ExtraInfo.AudioLength),
	}, nil
}

// TranscribeAudio is not supported by MiniMax. Returns an error.
func (p *Provider) TranscribeAudio(_ context.Context, _ service.AudioTranscribeRequest) (*service.AudioTranscribeResponse, error) {
	return nil, fmt.Errorf("minimax does not support speech-to-text")
}

// ─── String helpers ───

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func contains(s, substr string) bool {
	return indexOf(s, substr) != -1
}
