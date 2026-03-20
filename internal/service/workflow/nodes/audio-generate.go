package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// audioGenerateNode converts text to speech using a provider that
// implements service.AudioProvider.
//
// Config (node.Data):
//
//	"provider":        string  — provider key (required)
//	"model":           string  — TTS model (optional, e.g. "tts-1", "tts-1-hd")
//	"voice":           string  — voice name (optional, e.g. "alloy", "nova")
//	"response_format": string  — audio format (optional, e.g. "mp3", "opus")
//	"speed":           float64 — speed multiplier 0.25–4.0 (optional)
//
// Input ports:
//
//	"text" — text to synthesize (string)
//
// Output ports:
//
//	"audio"    — base64-encoded audio data
//	"metadata" — content type, duration, etc.
type audioGenerateNode struct {
	providerKey    string
	model          string
	voice          string
	responseFormat string
	speed          float64
}

func init() {
	workflow.RegisterNodeType("audio_generate", newAudioGenerateNode)
}

func newAudioGenerateNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	voice, _ := node.Data["voice"].(string)
	responseFormat, _ := node.Data["response_format"].(string)

	speed := 0.0
	if v, ok := node.Data["speed"].(float64); ok {
		speed = v
	}

	return &audioGenerateNode{
		providerKey:    providerKey,
		model:          model,
		voice:          voice,
		responseFormat: responseFormat,
		speed:          speed,
	}, nil
}

func (n *audioGenerateNode) Type() string { return "audio_generate" }

func (n *audioGenerateNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "audio_generate",
		Label:       "Text to Speech",
		Category:    "media",
		Description: "Convert text to speech audio",
		Inputs: []workflow.PortMeta{
			{Name: "text", Type: workflow.PortTypeText, Required: true, Accept: []workflow.PortType{workflow.PortTypeData}, Label: "Text", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "audio", Type: workflow.PortTypeAudio, Label: "Audio", Position: "right"},
			{Name: "metadata", Type: workflow.PortTypeData, Label: "Metadata", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key"},
			{Name: "model", Type: "string", Default: "tts-1", Description: "TTS model"},
			{Name: "voice", Type: "string", Default: "alloy", Enum: []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}, Description: "Voice name"},
			{Name: "response_format", Type: "string", Default: "mp3", Enum: []string{"mp3", "opus", "aac", "flac"}, Description: "Audio format"},
			{Name: "speed", Type: "number", Default: 1.0, Description: "Speed multiplier (0.25-4.0)"},
		},
		Color: "orange",
	}
}

func (n *audioGenerateNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("audio_generate: 'provider' is required")
	}
	if reg.ProviderLookup == nil {
		return fmt.Errorf("audio_generate: no provider lookup configured")
	}
	return nil
}

func (n *audioGenerateNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	provider, _, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("audio_generate: provider %q: %w", n.providerKey, err)
	}

	audioProvider, ok := provider.(service.AudioProvider)
	if !ok {
		return nil, fmt.Errorf("audio_generate: provider %q does not support audio generation", n.providerKey)
	}

	text := toString(inputs["text"])
	if text == "" {
		text = toString(inputs["prompt"])
		if text == "" {
			text = toString(inputs["data"])
		}
	}
	if text == "" {
		return nil, fmt.Errorf("audio_generate: no text provided")
	}

	req := service.AudioGenerateRequest{
		Input:          text,
		Model:          n.model,
		Voice:          n.voice,
		ResponseFormat: n.responseFormat,
		Speed:          n.speed,
	}

	resp, err := audioProvider.GenerateAudio(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("audio_generate: %w", err)
	}

	return workflow.NewResult(map[string]any{
		"audio": resp.AudioBase64,
		"metadata": map[string]any{
			"content_type": resp.ContentType,
			"duration_ms":  resp.DurationMs,
		},
	}), nil
}
