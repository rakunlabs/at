package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rakunlabs/at"
)

type Provider struct {
	Model   string
	BaseURL string
}

func New(model string) *Provider {
	return &Provider{
		Model:   model,
		BaseURL: "http://localhost:11434/api/chat",
	}
}

func (p *Provider) Chat(ctx context.Context, messages []at.Message, tools []at.Tool) (*at.LLMResponse, error) {
	openaiTools := make([]map[string]any, len(tools))
	for i, tool := range tools {
		openaiTools[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			},
		}
	}

	reqBody := map[string]any{
		"model":    p.Model,
		"messages": messages,
		"stream":   false,
	}
	if len(tools) > 0 {
		reqBody["tools"] = openaiTools
	}

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", p.BaseURL, bytes.NewBuffer(jsonData))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Message struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	llmResp := &at.LLMResponse{
		Content:  result.Message.Content,
		Finished: len(result.Message.ToolCalls) == 0,
	}

	for i, tc := range result.Message.ToolCalls {
		var args map[string]any
		json.Unmarshal([]byte(tc.Function.Arguments), &args)
		llmResp.ToolCalls = append(llmResp.ToolCalls, at.ToolCall{
			ID:        fmt.Sprintf("call_%d", i),
			Name:      tc.Function.Name,
			Arguments: args,
		})
	}

	return llmResp, nil
}
