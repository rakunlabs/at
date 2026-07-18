package server

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestResponsesInputPreservesEncryptedReasoningForFunctionCall(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-state"},
		{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"go\"}"},
		{"type":"function_call_output","call_id":"call_1","output":"result"}
	]`)
	messages, err := responsesInputToOpenAIMessages(raw, "")
	if err != nil {
		t.Fatalf("responsesInputToOpenAIMessages: %v", err)
	}
	if len(messages) != 2 || len(messages[0].ToolCalls) != 1 {
		t.Fatalf("messages = %#v", messages)
	}
	signature := messages[0].ToolCalls[0].ThoughtSignature
	if signature == "" {
		t.Fatal("encrypted reasoning was not attached to the function call")
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(signature), &item); err != nil || item["encrypted_content"] != "opaque-state" {
		t.Fatalf("thought signature = %q, error = %v", signature, err)
	}
}

func TestBuildResponsesResponseIncludesEncryptedReasoning(t *testing.T) {
	signature := `{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"opaque-state"}`
	response := buildResponsesResponse("codex", nil, nil, &service.LLMResponse{
		ReasoningContent: "summary",
		ToolCalls: []service.ToolCall{{
			ID:               "call_1",
			Name:             "lookup",
			ThoughtSignature: signature,
		}},
	})
	if len(response.Output) != 2 || response.Output[0].Type != "reasoning" || response.Output[0].EncryptedContent != "opaque-state" {
		t.Fatalf("output = %#v", response.Output)
	}
	if len(response.Output[0].Summary) != 1 || response.Output[0].Summary[0].Text != "summary" {
		t.Fatalf("reasoning summary = %#v", response.Output[0].Summary)
	}
}

func TestResponsesRoundTripPreservesMultipleReasoningToolPairs(t *testing.T) {
	response := buildResponsesResponse("codex", nil, nil, &service.LLMResponse{
		ToolCalls: []service.ToolCall{
			{ID: "call_1", Name: "first", ThoughtSignature: `{"type":"reasoning","id":"rs_1","encrypted_content":"state-1"}`},
			{ID: "call_2", Name: "second", ThoughtSignature: `{"type":"reasoning","id":"rs_2","encrypted_content":"state-2"}`},
		},
	})
	if len(response.Output) != 4 || response.Output[0].EncryptedContent != "state-1" || response.Output[1].CallID != "call_1" ||
		response.Output[2].EncryptedContent != "state-2" || response.Output[3].CallID != "call_2" {
		t.Fatalf("interleaved output = %#v", response.Output)
	}
	raw, err := json.Marshal(response.Output)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	messages, err := responsesInputToOpenAIMessages(raw, "")
	if err != nil {
		t.Fatalf("responsesInputToOpenAIMessages: %v", err)
	}
	if len(messages) != 2 || messages[0].ToolCalls[0].ThoughtSignature == "" || messages[1].ToolCalls[0].ThoughtSignature == "" {
		t.Fatalf("round-tripped messages = %#v", messages)
	}
	if messages[0].ToolCalls[0].ID != "call_1" || messages[1].ToolCalls[0].ID != "call_2" {
		t.Fatalf("tool calls were reordered: %#v", messages)
	}
}
