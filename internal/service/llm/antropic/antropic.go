package antropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/worldline-go/klient"

	"github.com/rakunlabs/at/internal/service"
)

type Provider struct {
	APIKey string
	Model  string

	client *klient.Client
}

type AnthropicResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Error      Error          `json:"error"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	Model      string         `json:"model"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
}

type Error struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Response structures
type ContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func New(apiKey, model string) (*Provider, error) {
	client, err := klient.New(klient.WithBaseURL("https://api.anthropic.com"), klient.WithHeaderSet(http.Header{
		"X-Api-Key":         []string{apiKey},
		"Anthropic-Version": []string{"2023-06-01"},
		"Content-Type":      []string{"application/json"},
	}))
	if err != nil {
		return nil, err
	}

	return &Provider{
		APIKey: apiKey,
		Model:  model,
		client: client,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, messages []service.Message, tools []service.Tool) (*service.LLMResponse, error) {
	anthropicTools := make([]map[string]any, len(tools))
	for i, tool := range tools {
		anthropicTools[i] = map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": tool.InputSchema,
		}
	}

	reqBody := map[string]any{
		"model":      p.Model,
		"max_tokens": 1024,
		"messages":   messages,
	}
	if len(tools) > 0 {
		reqBody["tools"] = anthropicTools
	}

	jsonData, _ := json.Marshal(reqBody)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/v1/messages", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	var result AnthropicResponse
	if err := p.client.Do(req, func(r *http.Response) error {
		bodyData, err := io.ReadAll(r.Body)
		if err != nil {
			return err
		}

		fmt.Printf("Anthropic response body: %s\n", string(bodyData))

		if err := json.Unmarshal(bodyData, &result); err != nil {
			return err
		}

		return nil
	}); err != nil {
		return nil, err
	}

	llmResp := &service.LLMResponse{
		Finished: result.StopReason != "tool_use",
	}

	if result.Type == "error" {
		llmResp.Content = fmt.Sprintf("Error from Anthropic: %s", result.Error.Message)

		return llmResp, nil
	}

	for _, block := range result.Content {
		switch block.Type {
		case "text":
			llmResp.Content += block.Text
		case "tool_use":
			llmResp.ToolCalls = append(llmResp.ToolCalls, service.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
			})
		}
	}

	return llmResp, nil
}
