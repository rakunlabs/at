package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// audioTranscribeNode converts speech to text using a provider that
// implements service.AudioProvider.
//
// Config (node.Data):
//
//	"provider":        string — provider key (required)
//	"model":           string — transcription model (optional, e.g. "whisper-1")
//	"language":        string — ISO-639-1 language code (optional)
//	"response_format": string — output format (optional, e.g. "json", "text", "srt")
//
// Input ports:
//
//	"audio" — base64-encoded audio data (string)
//
// Output ports:
//
//	"text"     — transcribed text
//	"segments" — timed segments (when verbose format is used)
type audioTranscribeNode struct {
	providerKey    string
	model          string
	language       string
	responseFormat string
}

func init() {
	workflow.RegisterNodeType("audio_transcribe", newAudioTranscribeNode)
}

func newAudioTranscribeNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	language, _ := node.Data["language"].(string)
	responseFormat, _ := node.Data["response_format"].(string)

	return &audioTranscribeNode{
		providerKey:    providerKey,
		model:          model,
		language:       language,
		responseFormat: responseFormat,
	}, nil
}

func (n *audioTranscribeNode) Type() string { return "audio_transcribe" }

func (n *audioTranscribeNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "audio_transcribe",
		Label:       "Speech to Text",
		Category:    "media",
		Description: "Transcribe audio to text",
		Inputs: []workflow.PortMeta{
			{Name: "audio", Type: workflow.PortTypeAudio, Required: true, Accept: []workflow.PortType{workflow.PortTypeText, workflow.PortTypeData}, Label: "Audio", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "text", Type: workflow.PortTypeText, Label: "Text", Position: "right"},
			{Name: "segments", Type: workflow.PortTypeData, Label: "Segments", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key"},
			{Name: "model", Type: "string", Default: "whisper-1", Description: "Transcription model"},
			{Name: "language", Type: "string", Description: "ISO-639-1 language code"},
			{Name: "response_format", Type: "string", Default: "json", Enum: []string{"json", "text", "srt", "verbose_json", "vtt"}, Description: "Output format"},
		},
		Color: "orange",
	}
}

func (n *audioTranscribeNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("audio_transcribe: 'provider' is required")
	}
	if reg.ProviderLookup == nil {
		return fmt.Errorf("audio_transcribe: no provider lookup configured")
	}
	return nil
}

func (n *audioTranscribeNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	provider, _, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("audio_transcribe: provider %q: %w", n.providerKey, err)
	}

	audioProvider, ok := provider.(service.AudioProvider)
	if !ok {
		return nil, fmt.Errorf("audio_transcribe: provider %q does not support audio transcription", n.providerKey)
	}

	audioInput := toString(inputs["audio"])
	if audioInput == "" {
		audioInput = toString(inputs["data"])
	}
	if audioInput == "" {
		return nil, fmt.Errorf("audio_transcribe: no audio data provided")
	}

	// Detect content type from input or default to mpeg.
	contentType, _ := inputs["content_type"].(string)
	if contentType == "" {
		contentType = "audio/mpeg"
	}

	req := service.AudioTranscribeRequest{
		AudioBase64:    audioInput,
		ContentType:    contentType,
		Model:          n.model,
		Language:       n.language,
		ResponseFormat: n.responseFormat,
	}

	resp, err := audioProvider.TranscribeAudio(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("audio_transcribe: %w", err)
	}

	// Convert segments to []any for JSON output.
	var segments []any
	for _, seg := range resp.Segments {
		segments = append(segments, map[string]any{
			"start": seg.Start,
			"end":   seg.End,
			"text":  seg.Text,
		})
	}

	return workflow.NewResult(map[string]any{
		"text":     resp.Text,
		"segments": segments,
	}), nil
}
