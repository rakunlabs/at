package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// imageGenerateNode generates images from a text prompt using a provider
// that implements service.ImageProvider.
//
// Config (node.Data):
//
//	"provider": string — provider key (required)
//	"model":    string — model override (optional)
//	"size":     string — image size (optional, e.g. "1024x1024")
//	"quality":  string — image quality (optional, e.g. "standard", "hd")
//	"style":    string — image style (optional, e.g. "vivid", "natural")
//	"n":        float64 — number of images (optional, default 1)
//
// Input ports:
//
//	"prompt" — text prompt for image generation (string)
//
// Output ports:
//
//	"image"    — URL or base64 of the first generated image
//	"images"   — array of all generated images ([]GeneratedImage)
//	"metadata" — generation metadata (revised prompt, model, etc.)
type imageGenerateNode struct {
	providerKey string
	model       string
	size        string
	quality     string
	style       string
	n           int
}

func init() {
	workflow.RegisterNodeType("image_generate", newImageGenerateNode)
}

func newImageGenerateNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	size, _ := node.Data["size"].(string)
	quality, _ := node.Data["quality"].(string)
	style, _ := node.Data["style"].(string)

	n := 1
	if v, ok := node.Data["n"].(float64); ok && v >= 1 {
		n = int(v)
	}

	return &imageGenerateNode{
		providerKey: providerKey,
		model:       model,
		size:        size,
		quality:     quality,
		style:       style,
		n:           n,
	}, nil
}

func (n *imageGenerateNode) Type() string { return "image_generate" }

func (n *imageGenerateNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "image_generate",
		Label:       "Image Generate",
		Category:    "media",
		Description: "Generate images from a text prompt",
		Inputs: []workflow.PortMeta{
			{Name: "prompt", Type: workflow.PortTypeText, Required: true, Accept: []workflow.PortType{workflow.PortTypeData}, Label: "Prompt", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "image", Type: workflow.PortTypeImage, Label: "Image", Position: "right"},
			{Name: "images", Type: workflow.PortTypeData, Label: "Images", Position: "right"},
			{Name: "metadata", Type: workflow.PortTypeData, Label: "Metadata", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key"},
			{Name: "model", Type: "string", Description: "Image model name"},
			{Name: "size", Type: "string", Default: "1024x1024", Enum: []string{"1024x1024", "1792x1024", "1024x1792"}, Description: "Image size"},
			{Name: "quality", Type: "string", Default: "standard", Enum: []string{"standard", "hd"}, Description: "Image quality"},
			{Name: "style", Type: "string", Default: "vivid", Enum: []string{"vivid", "natural"}, Description: "Image style"},
			{Name: "n", Type: "number", Default: 1, Description: "Number of images (1-4)"},
		},
		Color: "green",
	}
}

func (n *imageGenerateNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("image_generate: 'provider' is required")
	}
	if reg.ProviderLookup == nil {
		return fmt.Errorf("image_generate: no provider lookup configured")
	}
	return nil
}

func (n *imageGenerateNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	provider, _, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("image_generate: provider %q: %w", n.providerKey, err)
	}

	imgProvider, ok := provider.(service.ImageProvider)
	if !ok {
		return nil, fmt.Errorf("image_generate: provider %q does not support image generation", n.providerKey)
	}

	prompt := toString(inputs["prompt"])
	if prompt == "" {
		prompt = toString(inputs["text"])
		if prompt == "" {
			prompt = toString(inputs["data"])
		}
	}
	if prompt == "" {
		return nil, fmt.Errorf("image_generate: no prompt provided")
	}

	req := service.ImageGenerateRequest{
		Prompt:  prompt,
		Model:   n.model,
		N:       n.n,
		Size:    n.size,
		Quality: n.quality,
		Style:   n.style,
	}

	resp, err := imgProvider.GenerateImage(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("image_generate: %w", err)
	}

	// Build the primary image output (URL or base64).
	var primaryImage string
	if len(resp.Images) > 0 {
		if resp.Images[0].URL != "" {
			primaryImage = resp.Images[0].URL
		} else {
			primaryImage = resp.Images[0].Base64
		}
	}

	// Build images array for downstream.
	imagesAny := make([]any, len(resp.Images))
	for i, img := range resp.Images {
		imagesAny[i] = map[string]any{
			"url":            img.URL,
			"base64":         img.Base64,
			"revised_prompt": img.RevisedPrompt,
		}
	}

	metadata := map[string]any{}
	if len(resp.Images) > 0 && resp.Images[0].RevisedPrompt != "" {
		metadata["revised_prompt"] = resp.Images[0].RevisedPrompt
	}

	return workflow.NewResult(map[string]any{
		"image":    primaryImage,
		"images":   imagesAny,
		"metadata": metadata,
	}), nil
}
