package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// embeddingNode creates vector embeddings from text using a provider that
// implements service.EmbeddingProvider.
//
// Config (node.Data):
//
//	"provider": string — provider key (required)
//	"model":    string — embedding model (optional, e.g. "text-embedding-3-small")
//
// Input ports:
//
//	"input" — text to embed (string or []string via JSON)
//
// Output ports:
//
//	"embedding"  — the first embedding vector ([]float64)
//	"embeddings" — all embedding vectors
//	"data"       — full response data
type embeddingNode struct {
	providerKey string
	model       string
	dimensions  int
	inputType   string
}

func init() {
	workflow.RegisterNodeType("embedding", newEmbeddingNode)
}

func newEmbeddingNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	inputType, _ := node.Data["input_type"].(string)
	if !service.ValidEmbeddingInputType(inputType) {
		return nil, fmt.Errorf("embedding: invalid input_type %q", inputType)
	}
	dimensions := 0
	if value, ok := node.Data["dimensions"].(float64); ok && value > 0 {
		dimensions = int(value)
	}

	return &embeddingNode{
		providerKey: providerKey,
		model:       model,
		dimensions:  dimensions,
		inputType:   inputType,
	}, nil
}

func (n *embeddingNode) Type() string { return "embedding" }

func (n *embeddingNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "embedding",
		Label:       "Embedding",
		Category:    "media",
		Description: "Create vector embeddings from text",
		Inputs: []workflow.PortMeta{
			{Name: "input", Type: workflow.PortTypeText, Required: true, Accept: []workflow.PortType{workflow.PortTypeData}, Label: "Input", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "embedding", Type: workflow.PortTypeEmbedding, Label: "Embedding", Position: "right"},
			{Name: "data", Type: workflow.PortTypeData, Label: "Data", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key"},
			{Name: "model", Type: "string", Description: "Embedding model name"},
			{Name: "dimensions", Type: "number", Description: "Output vector dimensions (provider/model dependent)"},
			{Name: "input_type", Type: "string", Description: "Embedding task: search_document, search_query, classification, or clustering"},
		},
		Color: "teal",
	}
}

func (n *embeddingNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("embedding: 'provider' is required")
	}
	if reg.ProviderLookup == nil {
		return fmt.Errorf("embedding: no provider lookup configured")
	}
	return nil
}

func (n *embeddingNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	provider, _, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("embedding: provider %q: %w", n.providerKey, err)
	}

	embProvider, ok := provider.(service.EmbeddingProvider)
	if !ok {
		return nil, fmt.Errorf("embedding: provider %q does not support embeddings", n.providerKey)
	}

	// Build input texts.
	var texts []string
	switch v := inputs["input"].(type) {
	case string:
		if v != "" {
			texts = []string{v}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				texts = append(texts, s)
			}
		}
	case []string:
		texts = v
	}

	if len(texts) == 0 {
		// Fall back to text/data ports.
		if s := toString(inputs["text"]); s != "" {
			texts = []string{s}
		} else if s := toString(inputs["data"]); s != "" {
			texts = []string{s}
		}
	}

	if len(texts) == 0 {
		return nil, fmt.Errorf("embedding: no input text provided")
	}

	req := service.EmbeddingRequest{
		Input:     texts,
		Model:     n.model,
		InputType: n.inputType,
	}
	if n.dimensions > 0 {
		req.Dimensions = &n.dimensions
	}
	requestedModel := n.providerKey
	if n.model != "" {
		requestedModel += "/" + n.model
	}

	requestBody, _ := json.Marshal(req)
	started := time.Now()
	resp, err := embProvider.CreateEmbedding(ctx, req)
	latencyMs := time.Since(started).Milliseconds()
	if err != nil {
		if reg.RecordObservation != nil {
			reg.RecordObservation(ctx, service.LLMCall{
				ObservationType: service.ObservationEmbedding,
				Name:            "embeddings", Source: "workflow",
				Provider: n.providerKey, Model: n.model, RequestedModel: requestedModel,
				RequestBody: string(requestBody), LatencyMs: latencyMs,
				Status: "error", ErrorCode: "embedding_error", ErrorMessage: err.Error(), Level: service.ObservationLevelError,
				Metadata: map[string]any{"input_count": len(texts), "input_type": n.inputType},
			})
		}
		return nil, fmt.Errorf("embedding: %w", err)
	}
	if reg.RecordObservation != nil {
		responseBody, _ := json.Marshal(resp)
		reg.RecordObservation(ctx, service.LLMCall{
			ObservationType: service.ObservationEmbedding,
			Name:            "embeddings", Source: "workflow",
			Provider: n.providerKey, Model: resp.Model, RequestedModel: requestedModel,
			RequestBody: string(requestBody), ResponseBody: string(responseBody),
			InputTokens: int64(resp.Usage.PromptTokens), LatencyMs: latencyMs,
			Status: "ok", FinishReason: "stop",
			Metadata: map[string]any{"input_count": len(texts), "input_type": n.inputType, "usage_estimated": resp.UsageEstimated},
		})
	}

	// Convert embeddings to []any for JSON output.
	var embeddingsAny []any
	for _, emb := range resp.Embeddings {
		embAny := make([]any, len(emb))
		for i, v := range emb {
			embAny[i] = v
		}
		embeddingsAny = append(embeddingsAny, embAny)
	}

	// Primary output: first embedding.
	var primaryEmbedding any
	if len(embeddingsAny) > 0 {
		primaryEmbedding = embeddingsAny[0]
	}

	return workflow.NewResult(map[string]any{
		"embedding":  primaryEmbedding,
		"embeddings": embeddingsAny,
		"data": map[string]any{
			"model":      resp.Model,
			"embeddings": embeddingsAny,
			"usage": map[string]any{
				"prompt_tokens": resp.Usage.PromptTokens,
				"total_tokens":  resp.Usage.TotalTokenCount(),
				"estimated":     resp.UsageEstimated,
			},
		},
	}), nil
}
