package bedrock

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/common"
)

// Converse requires the first message to be a user turn; a windowed chat
// history can begin with an assistant tool call.
func TestConverseOpensWithUserTurn(t *testing.T) {
	p := &Provider{}
	req := p.buildConverseRequest([]service.Message{
		{Role: "system", Content: "sys"},
		{Role: "assistant", Content: []service.ContentBlock{{Type: "tool_use", ID: "t1", Name: "lookup"}}},
		{Role: "user", Content: []service.ContentBlock{{Type: "tool_result", ToolUseID: "t1", Content: "ok"}}},
		{Role: "user", Content: "next"},
	}, nil, nil)

	if len(req.Messages) < 3 || req.Messages[0].Role != "user" || req.Messages[0].Content[0].Text != common.LeadingUserPlaceholder {
		t.Fatalf("leading user turn missing: %+v", req.Messages)
	}
	if req.Messages[1].Role != "assistant" || req.Messages[2].Content[0].ToolResult == nil {
		t.Fatalf("tool pair must stay adjacent: %+v", req.Messages)
	}

	plain := p.buildConverseRequest([]service.Message{{Role: "user", Content: "hi"}}, nil, nil)
	if len(plain.Messages) != 1 {
		t.Fatalf("user-first history must be unchanged: %+v", plain.Messages)
	}
}
