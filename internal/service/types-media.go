package service

import (
	"context"
	"errors"
)

// ErrUnsupportedOperation is returned by providers that implement a media
// interface only partially (e.g. MiniMax implements AudioProvider for TTS
// but not STT). The gateway maps it to HTTP 501 `unsupported_operation`
// instead of a generic 502 provider error.
var ErrUnsupportedOperation = errors.New("operation not supported by provider")

// ─── Media Provider Interfaces ───
//
// Providers may optionally implement these interfaces alongside LLMProvider
// to support media operations (image generation, TTS, transcription, embeddings).
// The workflow engine checks for these via type assertion.

// ImageProvider generates and edits images.
type ImageProvider interface {
	GenerateImage(ctx context.Context, req ImageGenerateRequest) (*ImageResponse, error)
}

// AudioProvider handles text-to-speech and speech-to-text.
type AudioProvider interface {
	GenerateAudio(ctx context.Context, req AudioGenerateRequest) (*AudioResponse, error)
	TranscribeAudio(ctx context.Context, req AudioTranscribeRequest) (*AudioTranscribeResponse, error)
}

// EmbeddingProvider creates vector embeddings from text.
type EmbeddingProvider interface {
	CreateEmbedding(ctx context.Context, req EmbeddingRequest) (*EmbeddingResponse, error)
}

// ModerationProvider classifies text against a safety policy.
// Providers that implement this surface OpenAI-shaped moderation results.
type ModerationProvider interface {
	Moderate(ctx context.Context, req ModerationRequest) (*ModerationResponse, error)
}

// RerankProvider re-orders a list of documents by relevance to a query.
// First-party support exists on Cohere; other providers can implement it
// against their own rerank endpoint.
type RerankProvider interface {
	Rerank(ctx context.Context, req RerankRequest) (*RerankResponse, error)
}

// ─── Media Request/Response Types ───

// ImageGenerateRequest describes a text-to-image generation request.
type ImageGenerateRequest struct {
	Prompt  string `json:"prompt"`
	Model   string `json:"model,omitempty"`
	N       int    `json:"n,omitempty"`       // number of images (default 1)
	Size    string `json:"size,omitempty"`    // e.g. "1024x1024", "1792x1024"
	Quality string `json:"quality,omitempty"` // e.g. "standard", "hd"
	Style   string `json:"style,omitempty"`   // e.g. "vivid", "natural"
}

// ImageResponse is the result of an image generation or edit operation.
type ImageResponse struct {
	Images []GeneratedImage `json:"images"`
	Usage  Usage            `json:"usage,omitempty"`
}

// GeneratedImage represents a single generated image.
type GeneratedImage struct {
	URL           string `json:"url,omitempty"`            // URL to the image (when response_format is url)
	Base64        string `json:"base64,omitempty"`         // base64-encoded image data
	RevisedPrompt string `json:"revised_prompt,omitempty"` // prompt revised by the model (DALL-E 3)
}

// AudioGenerateRequest describes a text-to-speech request.
type AudioGenerateRequest struct {
	Input          string  `json:"input"`                     // text to synthesize
	Model          string  `json:"model,omitempty"`           // e.g. "tts-1", "tts-1-hd"
	Voice          string  `json:"voice,omitempty"`           // e.g. "alloy", "echo", "fable", "onyx", "nova", "shimmer"
	ResponseFormat string  `json:"response_format,omitempty"` // e.g. "mp3", "opus", "aac", "flac"
	Speed          float64 `json:"speed,omitempty"`           // 0.25 to 4.0
}

// AudioResponse is the result of a text-to-speech operation.
type AudioResponse struct {
	AudioBase64 string `json:"audio_base64"` // base64-encoded audio data
	ContentType string `json:"content_type"` // e.g. "audio/mpeg"
	DurationMs  int64  `json:"duration_ms,omitempty"`
}

// AudioTranscribeRequest describes a speech-to-text request.
type AudioTranscribeRequest struct {
	AudioBase64    string `json:"audio_base64"`              // base64-encoded audio data
	ContentType    string `json:"content_type"`              // e.g. "audio/mpeg", "audio/wav"
	Filename       string `json:"filename,omitempty"`        // original filename, including its audio extension
	Model          string `json:"model,omitempty"`           // e.g. "whisper-1"
	Language       string `json:"language,omitempty"`        // ISO-639-1 code
	Prompt         string `json:"prompt,omitempty"`          // optional context prompt
	ResponseFormat string `json:"response_format,omitempty"` // "json", "text", "srt", "verbose_json", "vtt"
}

// AudioTranscribeResponse is the result of a transcription.
type AudioTranscribeResponse struct {
	Text     string                 `json:"text"`
	Language string                 `json:"language,omitempty"`
	Duration float64                `json:"duration,omitempty"`
	Segments []TranscriptionSegment `json:"segments,omitempty"`
}

// TranscriptionSegment is a timed segment from verbose transcription.
type TranscriptionSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// EmbeddingRequest describes a text embedding request.
type EmbeddingRequest struct {
	Input []string `json:"input"`           // one or more texts to embed
	Model string   `json:"model,omitempty"` // e.g. "text-embedding-3-small"
}

// EmbeddingResponse is the result of an embedding operation.
type EmbeddingResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
	Model      string      `json:"model"`
	Usage      Usage       `json:"usage,omitempty"`
}

// ModerationRequest describes a content-moderation request.
type ModerationRequest struct {
	Input []string `json:"input"`           // one or more strings to classify
	Model string   `json:"model,omitempty"` // e.g. "omni-moderation-latest"
}

// ModerationResponse is the result of a moderation call. Mirrors OpenAI's
// shape: each input yields one result with per-category flags + scores.
type ModerationResponse struct {
	ID      string             `json:"id"`
	Model   string             `json:"model"`
	Results []ModerationResult `json:"results"`
}

// ModerationResult is a single moderation verdict for one input.
type ModerationResult struct {
	Flagged        bool               `json:"flagged"`
	Categories     map[string]bool    `json:"categories"`
	CategoryScores map[string]float64 `json:"category_scores"`
}

// RerankRequest describes a re-ranking request.
type RerankRequest struct {
	Model           string   `json:"model,omitempty"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments *bool    `json:"return_documents,omitempty"`
}

// RerankResponse is a re-ranked subset of the input documents, sorted by
// relevance score (highest first). When ReturnDocuments=false the
// document text is omitted and only indices/scores are returned.
type RerankResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Results []RerankResult `json:"results"`
	Usage   Usage          `json:"usage,omitempty"`
}

// RerankResult is a single re-ranked document entry.
type RerankResult struct {
	Index          int     `json:"index"` // original index in input documents
	Document       string  `json:"document,omitempty"`
	RelevanceScore float64 `json:"relevance_score"`
}
