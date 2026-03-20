package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// visionAnalyzeNode sends an image + prompt to an LLM with vision capabilities
// (e.g. GPT-4o, Claude, Gemini). It uses the standard LLMProvider.Chat with
// image content blocks — no special VisionProvider interface needed since
// modern LLMs handle images natively.
//
// Config (node.Data):
//
//	"provider":      string — provider key (required)
//	"model":         string — model override (optional)
//	"system_prompt": string — system message (optional)
//
// Input ports:
//
//	"image"  — image URL or base64 data (string)
//	"prompt" — question/instruction about the image (string)
//
// Output ports:
//
//	"response" — the LLM's text analysis
//	"data"     — structured output (same as response, for generic wiring)
type visionAnalyzeNode struct {
	providerKey  string
	model        string
	systemPrompt string
}

func init() {
	workflow.RegisterNodeType("vision_analyze", newVisionAnalyzeNode)
}

func newVisionAnalyzeNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	systemPrompt, _ := node.Data["system_prompt"].(string)

	return &visionAnalyzeNode{
		providerKey:  providerKey,
		model:        model,
		systemPrompt: systemPrompt,
	}, nil
}

func (n *visionAnalyzeNode) Type() string { return "vision_analyze" }

func (n *visionAnalyzeNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "vision_analyze",
		Label:       "Vision Analyze",
		Category:    "media",
		Description: "Analyze an image with an LLM (describe, OCR, detect)",
		Inputs: []workflow.PortMeta{
			{Name: "image", Type: workflow.PortTypeImage, Required: true, Accept: []workflow.PortType{workflow.PortTypeText, workflow.PortTypeData}, Label: "Image", Position: "left"},
			{Name: "prompt", Type: workflow.PortTypeText, Accept: []workflow.PortType{workflow.PortTypeData}, Label: "Prompt", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "response", Type: workflow.PortTypeText, Label: "Response", Position: "right"},
			{Name: "data", Type: workflow.PortTypeData, Label: "Data", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key"},
			{Name: "model", Type: "string", Description: "Model name"},
			{Name: "system_prompt", Type: "string", Description: "System prompt for vision analysis"},
		},
		Color: "lime",
	}
}

func (n *visionAnalyzeNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("vision_analyze: 'provider' is required")
	}
	if reg.ProviderLookup == nil {
		return fmt.Errorf("vision_analyze: no provider lookup configured")
	}
	return nil
}

func (n *visionAnalyzeNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	provider, defaultModel, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("vision_analyze: provider %q: %w", n.providerKey, err)
	}

	model := n.model
	if model == "" {
		model = defaultModel
	}

	// Get image input.
	imageInput := toString(inputs["image"])
	if imageInput == "" {
		imageInput = toString(inputs["data"])
	}
	if imageInput == "" {
		return nil, fmt.Errorf("vision_analyze: no image provided")
	}

	// Get prompt (defaults to generic description request).
	prompt := toString(inputs["prompt"])
	if prompt == "" {
		prompt = toString(inputs["text"])
	}
	if prompt == "" {
		prompt = "Describe this image in detail."
	}

	// Build messages with image content.
	var messages []service.Message
	if n.systemPrompt != "" {
		messages = append(messages, service.Message{
			Role:    "system",
			Content: n.systemPrompt,
		})
	}

	// Build multimodal content array in OpenAI format.
	// Providers handle translation to their native format internally.
	content := []any{
		map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": imageInput,
			},
		},
		map[string]any{
			"type": "text",
			"text": prompt,
		},
	}

	messages = append(messages, service.Message{
		Role:    "user",
		Content: content,
	})

	resp, err := provider.Chat(ctx, model, messages, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("vision_analyze: chat failed: %w", err)
	}

	return workflow.NewResult(map[string]any{
		"response": resp.Content,
		"data":     resp.Content,
	}), nil
}
