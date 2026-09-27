package antropic

import (
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func firstMessageText(c any) string {
	if s, ok := c.(string); ok {
		return s
	}
	if blocks := contentToAnySlice(c); len(blocks) > 0 {
		if b, ok := blocks[0].(map[string]any); ok {
			s, _ := b["text"].(string)
			return s
		}
	}
	return ""
}

// A chat history windowed to start with an assistant tool_use used to get the
// relocated system text prepended to the tool_result message, so tool_result no
// longer led that message and Anthropic answered 400 "tool_use ids were found
// without tool_result blocks immediately after".
func TestOAuthSystemRelocationKeepsToolResultFirst(t *testing.T) {
	p := &Provider{MaxTokens: 1024, tokenSource: NewStaticTokenSource("token")}
	messages := []service.Message{
		{Role: "system", Content: "task system prompt"},
		{Role: "assistant", Content: []service.ContentBlock{
			{Type: "text", Text: "Refreshing token."},
			{Type: "tool_use", ID: "toolu_1", Name: "refresh_youtube_token", Input: map[string]any{}},
		}},
		{Role: "user", Content: []service.ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_1", Content: "invalid_grant"},
		}},
		{Role: "assistant", Content: "[BLOCKED] token refresh failed"},
		{Role: "user", Content: "Upload"},
	}
	tools := []service.Tool{{Name: "refresh_youtube_token", InputSchema: map[string]any{"type": "object"}}}

	body := p.buildRequestBody("claude-sonnet-5", messages, tools, nil)
	msgs, ok := body["messages"].([]any)
	if !ok {
		t.Fatalf("messages type = %T", body["messages"])
	}
	if len(msgs) != 5 {
		t.Fatalf("got %d messages, want 5: %#v", len(msgs), msgs)
	}

	first := msgs[0].(map[string]any)
	if first["role"] != "user" || hasToolResultBlock(first["content"]) {
		t.Fatalf("first message must be a plain user message, got %#v", first)
	}
	if !strings.HasPrefix(firstMessageText(first["content"]), "task system prompt") {
		t.Fatalf("relocated system text missing: %#v", first["content"])
	}
	if msgs[1].(map[string]any)["role"] != "assistant" {
		t.Fatalf("second message must be the assistant tool_use: %#v", msgs[1])
	}

	resultBlocks := contentToAnySlice(msgs[2].(map[string]any)["content"])
	if len(resultBlocks) == 0 || resultBlocks[0].(map[string]any)["type"] != "tool_result" {
		t.Fatalf("tool_result must lead the message after tool_use: %#v", resultBlocks)
	}
}

// With a static API key the system prompt stays top-level, but Anthropic still
// requires the first message to be a user turn.
func TestStaticKeyHistoryOpensWithUserTurn(t *testing.T) {
	p := &Provider{MaxTokens: 1024}
	body := p.buildRequestBody("claude-sonnet-5", []service.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: []service.ContentBlock{
			{Type: "tool_use", ID: "toolu_1", Name: "lookup", Input: map[string]any{}},
		}},
		{Role: "user", Content: []service.ContentBlock{
			{Type: "tool_result", ToolUseID: "toolu_1", Content: "ok"},
		}},
		{Role: "user", Content: "next"},
	}, nil, nil)

	msgs := body["messages"].([]service.Message)
	if len(msgs) != 3 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("leading user turn missing: %#v", msgs)
	}
}

func TestOAuthSystemRelocationPrependsToLeadingUser(t *testing.T) {
	p := &Provider{MaxTokens: 1024, tokenSource: NewStaticTokenSource("token")}
	body := p.buildRequestBody("claude-sonnet-5", []service.Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hi"},
	}, nil, nil)
	msgs := body["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	blocks := contentToAnySlice(msgs[0].(map[string]any)["content"])
	if len(blocks) != 1 || blocks[0].(map[string]any)["text"] != "sys\n\nhi" {
		t.Fatalf("content = %#v", msgs[0])
	}
}
