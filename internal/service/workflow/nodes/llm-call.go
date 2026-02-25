package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// llmCallNode sends a prompt to an LLM provider and returns the response.
//
// Config (node.Data):
//
//	"provider":      string — provider key for registry lookup (required)
//	"model":         string — model override (optional, empty = provider default)
//	"system_prompt": string — system message prepended to the conversation (optional)
//
// Input ports:
//
//	"prompt"  — the user message text (string)
//	"context" — additional context to include (optional, string)
//
// Output ports:
//
//	"response" — the full LLM response text
//	"text"     — alias for response (convenience port)
type llmCallNode struct {
	providerKey  string
	model        string
	systemPrompt string
}

func init() {
	workflow.RegisterNodeType("llm_call", newLLMCallNode)
}

func newLLMCallNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	systemPrompt, _ := node.Data["system_prompt"].(string)

	return &llmCallNode{
		providerKey:  providerKey,
		model:        model,
		systemPrompt: systemPrompt,
	}, nil
}

func (n *llmCallNode) Type() string { return "llm_call" }

func (n *llmCallNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("llm_call: 'provider' is required")
	}

	if reg.ProviderLookup == nil {
		return fmt.Errorf("llm_call: no provider lookup configured")
	}

	// Verify the provider exists.
	_, _, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return fmt.Errorf("llm_call: provider %q: %w", n.providerKey, err)
	}

	return nil
}

func (n *llmCallNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	provider, defaultModel, err := reg.ProviderLookup(n.providerKey)
	if err != nil {
		return nil, fmt.Errorf("llm_call: provider %q: %w", n.providerKey, err)
	}

	// Determine model.
	model := n.model
	if model == "" {
		model = defaultModel
	}

	// Build the user prompt from inputs.
	prompt := toString(inputs["prompt"])
	if prompt == "" {
		// Fall back to any "text" or "data" input.
		prompt = toString(inputs["text"])
		if prompt == "" {
			prompt = toString(inputs["data"])
		}
	}

	if prompt == "" {
		return nil, fmt.Errorf("llm_call: no prompt provided")
	}

	// Append context if available.
	if ctxStr := toString(inputs["context"]); ctxStr != "" {
		prompt = prompt + "\n\nContext:\n" + ctxStr
	}

	// Build messages.
	var messages []service.Message
	if n.systemPrompt != "" {
		messages = append(messages, service.Message{
			Role:    "system",
			Content: n.systemPrompt,
		})
	}
	messages = append(messages, service.Message{
		Role:    "user",
		Content: prompt,
	})

	resp, err := provider.Chat(ctx, model, messages, nil)
	if err != nil {
		return nil, fmt.Errorf("llm_call: chat failed: %w", err)
	}

	return workflow.NewResult(map[string]any{
		"response": resp.Content,
		"text":     resp.Content,
	}), nil
}

// toString converts a value to a string. Maps and slices are formatted
// with fmt.Sprint; nil returns "".
func toString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
