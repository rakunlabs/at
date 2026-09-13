package cohere

import (
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestAgentToolHistory(t *testing.T) {
	msgs := translateMessagesToCohere([]service.Message{
		{Role: "developer", Content: "instructions"},
		{Role: "assistant", Content: []service.ContentBlock{{Type: "text", Text: "checking"}, {Type: "tool_use", ID: "call_1", Name: "ping"}}},
		{Role: "user", Content: []service.ContentBlock{{Type: "tool_result", ToolUseID: "call_1", Content: "pong"}}},
	})
	if len(msgs) != 3 || msgs[0].Role != "system" || len(msgs[1].ToolCalls) != 1 || msgs[2].Role != "tool" || msgs[2].ToolCallID != "call_1" {
		t.Fatalf("invalid tool history: %+v", msgs)
	}
	if msgs[1].ToolCalls[0].Function.Arguments != "{}" {
		t.Fatal("nil arguments not normalized")
	}
}
