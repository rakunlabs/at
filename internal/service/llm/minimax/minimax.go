// Package minimax implements a provider for the MiniMax AI platform
// (https://www.minimax.io/).
//
// MiniMax's chat API is OpenAI-compatible, so the Provider embeds an openai.Provider
// for Chat/ChatStream/Proxy. Image generation and text-to-speech use MiniMax's
// proprietary REST APIs.
//
// Supported capabilities:
//   - Chat (LLMProvider)         — via embedded openai.Provider
//   - ChatStream (LLMStreamProvider) — via embedded openai.Provider
//   - Image generation (ImageProvider) — POST /v1/image_generation
//   - Text-to-speech (AudioProvider.GenerateAudio) — POST /v1/t2a_v2
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
	"strings"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/openai"
)

const (
	DefaultBaseURL = "https://api.minimax.io/v1/chat/completions"
	DefaultAPIBase = "https://api.minimax.io/v1"
)

// Provider wraps an OpenAI-compatible provider for chat and adds MiniMax-native
// image generation and TTS support.
type Provider struct {
	// Embedded OpenAI provider handles Chat, ChatStream, and Proxy.
	*openai.Provider

	apiKey  string
	apiBase string // e.g. "https://api.minimax.io/v1"
	client  *klient.Client
}

// New creates a MiniMax provider.
//
// The baseURL should be the chat completions endpoint (OpenAI-compatible).
// If empty, defaults to "https://api.minimax.io/v1/chat/completions".
// The API base for proprietary endpoints is derived by stripping "/chat/completions".
func New(apiKey, model, baseURL, proxy string, insecureSkipVerify bool, extraHeaders map[string]string) (*Provider, error) {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}

	// Create the embedded OpenAI provider for chat.
	oaiProvider, err := openai.New(apiKey, model, baseURL, proxy, insecureSkipVerify, extraHeaders)
	if err != nil {
		return nil, fmt.Errorf("minimax: create openai provider: %w", err)
	}

	// Derive the API base for proprietary endpoints.
	apiBase := baseURL
	apiBase = strings.TrimSuffix(apiBase, "/")
	apiBase = strings.TrimSuffix(apiBase, "/chat/completions")

	// Build HTTP client for proprietary API calls.
	headers := http.Header{
		"Content-Type":  []string{"application/json"},
		"Authorization": []string{"Bearer " + apiKey},
	}

	klientOpts := []klient.OptionClientFn{
		klient.WithBaseURL(apiBase),
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
		Provider: oaiProvider,
		apiKey:   apiKey,
		apiBase:  apiBase,
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
