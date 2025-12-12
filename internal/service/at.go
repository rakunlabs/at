package service

import (
	"context"
	"fmt"
)

// Generic LLM Interface
type LLMProvider interface {
	Chat(ctx context.Context, messages []Message, tools []Tool) (*LLMResponse, error)
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"` // Can be string or array of content blocks
}

type ContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
}

type LLMResponse struct {
	Content   string
	ToolCalls []ToolCall
	Finished  bool
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
}

// Agent orchestrates MCP and LLM
type Agent struct {
	mcp      *HTTPMCPClient
	provider LLMProvider
	messages []Message

	Tools []Tool
}

func NewAgent(mcp *HTTPMCPClient, provider LLMProvider) *Agent {
	return &Agent{
		mcp:      mcp,
		provider: provider,
		messages: []Message{},
		Tools:    []Tool{},
	}
}

func (a *Agent) SetTools(ctx context.Context) error {
	tools, err := a.mcp.ListTools(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\nAvailable tools: %d\n", len(tools))
	for _, tool := range tools {
		fmt.Printf("  - %s: %s\n", tool.Name, tool.Description)
	}

	a.Tools = tools

	return nil
}

func (a *Agent) Run(ctx context.Context, userMessage string) error {
	a.messages = append(a.messages, Message{
		Role:    "user",
		Content: userMessage,
	})

	for {
		resp, err := a.provider.Chat(ctx, a.messages, a.Tools)
		if err != nil {
			return err
		}

		if resp.Content != "" {
			fmt.Printf("\n🤖 Assistant: %s\n", resp.Content)
		}

		// Build assistant message content
		var assistantContent []ContentBlock
		if resp.Content != "" {
			assistantContent = append(assistantContent, ContentBlock{
				Type: "text",
				Text: resp.Content,
			})
		}
		for _, tc := range resp.ToolCalls {
			assistantContent = append(assistantContent, ContentBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Name,
				Input: tc.Arguments,
			})
		}

		a.messages = append(a.messages, Message{
			Role:    "assistant",
			Content: assistantContent,
		})

		if resp.Finished {
			break
		}

		// Execute tool calls
		if len(resp.ToolCalls) > 0 {
			var toolResults []ContentBlock
			for _, tc := range resp.ToolCalls {
				fmt.Printf("\n🔧 [Tool Call: %s]\n", tc.Name)
				fmt.Printf("   Arguments: %v\n", tc.Arguments)

				result, err := a.mcp.CallTool(ctx, tc.Name, tc.Arguments)
				if err != nil {
					result = fmt.Sprintf("Error: %v", err)
				}
				fmt.Printf("   ✅ Result: %s\n", result)

				toolResults = append(toolResults, ContentBlock{
					Type:      "tool_result",
					ToolUseID: tc.ID,
					Content:   result,
				})
			}

			a.messages = append(a.messages, Message{
				Role:    "user",
				Content: toolResults,
			})
		}
	}

	return nil
}
