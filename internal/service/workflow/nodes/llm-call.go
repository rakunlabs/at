package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
//	"output_format": string — "text" (default) or "json". JSON asks the
//	                 provider for a JSON object (OpenAI json_object, Gemini
//	                 responseMimeType, an instruction on Anthropic) and parses
//	                 the answer into the "json" output; an answer that is not
//	                 a JSON object fails the node instead of flowing on as text.
//	"json_schema":   string — optional JSON Schema the object must follow
//	                 (json mode only); sent as response_format json_schema.
//
// Input ports:
//
//	"prompt"  — the user message text (string)
//	"context" — additional context to include (optional, string)
//	"attachments" — files sent to the model with the prompt: run file refs,
//	                run paths, inline base64 or data: URLs (model-inputs.go)
//
// Output ports:
//
//	"response" — the full LLM response text
//	"json"     — the parsed object (output_format json only)
//	"files"    — run file references for images the model returned
//	             (image-output models such as Gemini image); [] when none
//	"image"    — the first of those files (absent when none)
type llmCallNode struct {
	providerKey  string
	model        string
	systemPrompt string
	outputFormat string
	jsonSchema   map[string]any
}

func init() {
	workflow.RegisterNodeType("llm_call", newLLMCallNode)
}

func newLLMCallNode(node service.WorkflowNode) (workflow.Noder, error) {
	providerKey, _ := node.Data["provider"].(string)
	model, _ := node.Data["model"].(string)
	systemPrompt, _ := node.Data["system_prompt"].(string)
	outputFormat, _ := node.Data["output_format"].(string)
	outputFormat = strings.TrimSpace(outputFormat)

	var schema map[string]any
	switch raw := node.Data["json_schema"].(type) {
	case string:
		if strings.TrimSpace(raw) != "" {
			if err := json.Unmarshal([]byte(raw), &schema); err != nil || schema == nil {
				return nil, fmt.Errorf("llm_call: 'json_schema' must be a JSON object: %v", err)
			}
		}
	case map[string]any:
		if len(raw) > 0 {
			schema = raw
		}
	}

	return &llmCallNode{
		providerKey:  providerKey,
		model:        model,
		systemPrompt: systemPrompt,
		outputFormat: outputFormat,
		jsonSchema:   schema,
	}, nil
}

func (n *llmCallNode) Type() string { return "llm_call" }

func (n *llmCallNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "llm_call",
		Label:       "LLM Call",
		Category:    "processing",
		Description: "Send a prompt to an LLM provider and return the response",
		Inputs: []workflow.PortMeta{
			{Name: "prompt", Type: workflow.PortTypeText, Required: true, Accept: []workflow.PortType{workflow.PortTypeData}, Label: "Prompt", Position: "left"},
			{Name: "context", Type: workflow.PortTypeData, Label: "Context", Position: "left"},
			{Name: "attachments", Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "Attachments", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "response", Type: workflow.PortTypeText, Label: "Response", Position: "right"},
			{Name: "files", Type: workflow.PortTypeData, Label: "Files", Position: "right"},
			{Name: "image", Type: workflow.PortTypeData, Label: "Image", Position: "right"},
			{Name: "json", Type: workflow.PortTypeData, Label: "JSON", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "provider", Type: "string", Required: true, Description: "Provider key"},
			{Name: "model", Type: "string", Description: "Model name"},
			{Name: "system_prompt", Type: "string", Description: "System prompt for the LLM"},
			{Name: "output_format", Type: "string", Default: "text", Enum: []string{"text", "json"}, Description: "json: request a JSON object and emit it parsed on the json output"},
			{Name: "json_schema", Type: "string", Description: "Optional JSON Schema for the object (json output only)"},
		},
		Color: "blue",
	}
}

func (n *llmCallNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.providerKey == "" {
		return fmt.Errorf("llm_call: 'provider' is required")
	}
	switch n.outputFormat {
	case "", "text":
		if n.jsonSchema != nil {
			return fmt.Errorf("llm_call: 'json_schema' needs output_format json")
		}
	case "json":
	default:
		return fmt.Errorf("llm_call: unknown output_format %q (text or json)", n.outputFormat)
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
	userContent, err := userMessageContent(ctx, prompt, inputs["attachments"])
	if err != nil {
		return nil, fmt.Errorf("llm_call: attachments: %w", err)
	}
	messages = append(messages, service.Message{
		Role:    "user",
		Content: userContent,
	})

	var opts *service.ChatOptions
	if n.outputFormat == "json" {
		opts = &service.ChatOptions{ResponseFormat: jsonResponseFormat(n.jsonSchema)}
	}
	resp, err := provider.Chat(ctx, model, messages, nil, opts)
	if err != nil {
		return nil, fmt.Errorf("llm_call: chat failed: %w", err)
	}

	out := map[string]any{
		"response": resp.Content,
		"files":    []any{},
	}
	if n.outputFormat == "json" {
		parsed, err := parseModelJSON(resp.Content)
		if err != nil {
			return nil, fmt.Errorf("llm_call: %w", err)
		}
		out["json"] = parsed
	}
	if len(resp.InlineImages) > 0 {
		files, err := saveInlineImages(ctx, newRunOutputDir("llm"), 0, resp.InlineImages)
		if err != nil {
			return nil, fmt.Errorf("llm_call: save generated images: %w", err)
		}
		out["files"] = runFileRefsToAny(files)
		out["image"] = files[0].toMap()
	}

	return workflow.NewResult(out), nil
}

// toString converts a value to a string. Maps and slices become JSON — the
// form a model (or a person reading a prompt) can actually parse; Go's
// fmt.Sprint wrote them as map[key:value]. nil returns "".
func toString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case map[string]any, []any, []string, []map[string]any:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return fmt.Sprint(v)
}

func jsonResponseFormat(schema map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "json_object"}
	}
	return map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "response", "schema": schema}}
}

// parseModelJSON accepts the object a JSON-mode answer should be. Providers
// without a native JSON mode (Anthropic) occasionally wrap it in a markdown
// fence, which is removed; anything else that is not one JSON object is an
// error, because routing on a misread answer would pick the wrong branch.
func parseModelJSON(content string) (map[string]any, error) {
	text := strings.TrimSpace(content)
	if strings.HasPrefix(text, "```") {
		if nl := strings.IndexByte(text, '\n'); nl >= 0 {
			text = text[nl+1:]
		}
		text = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), "```"))
	}
	if text == "" {
		return nil, fmt.Errorf("model returned an empty answer, expected a JSON object")
	}
	var parsed map[string]any
	dec := json.NewDecoder(strings.NewReader(text))
	if err := dec.Decode(&parsed); err != nil || parsed == nil {
		return nil, fmt.Errorf("model answer is not a JSON object: %s", truncateForError(content))
	}
	if dec.More() {
		return nil, fmt.Errorf("model answer has text after the JSON object: %s", truncateForError(content))
	}
	return parsed, nil
}

func truncateForError(s string) string {
	const limit = 200
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf("%q…", s[:limit])
}
