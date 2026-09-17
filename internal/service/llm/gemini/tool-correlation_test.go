package gemini

import (
	"context"
	"net/http"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// Gemini's own functionCall.id must survive into service.ToolCall.ID, because
// echoing it on the functionResponse is the only way to pair results with
// calls when one turn calls the same function more than once.
func TestUpstreamFunctionCallIDsArePreserved(t *testing.T) {
	resp, err := parseResponse(&generateContentResponse{
		Candidates: []candidate{{
			Content: &content{Parts: []part{
				{FunctionCall: &functionCall{ID: "fc-1", Name: "read_file", Args: map[string]any{"p": "a"}}},
				{FunctionCall: &functionCall{ID: "fc-2", Name: "read_file", Args: map[string]any{"p": "b"}}},
			}},
			FinishReason: "STOP",
		}},
	}, http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 2 {
		t.Fatalf("tool calls = %d, want 2", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].ID != "fc-1" || resp.ToolCalls[1].ID != "fc-2" {
		t.Fatalf("upstream ids replaced: %q, %q", resp.ToolCalls[0].ID, resp.ToolCalls[1].ID)
	}
}

// Models that send no id still need a unique caller-facing handle.
func TestMissingFunctionCallIDGetsSyntheticID(t *testing.T) {
	resp, err := parseResponse(&generateContentResponse{
		Candidates: []candidate{{
			Content: &content{Parts: []part{
				{FunctionCall: &functionCall{Name: "read_file"}},
				{FunctionCall: &functionCall{Name: "read_file"}},
			}},
			FinishReason: "STOP",
		}},
	}, http.Header{})
	if err != nil {
		t.Fatal(err)
	}
	first, second := resp.ToolCalls[0].ID, resp.ToolCalls[1].ID
	if first == "" || second == "" || first == second {
		t.Fatalf("synthetic ids not unique: %q, %q", first, second)
	}
	// Synthetic ids are never replayed upstream.
	if got := geminiEchoableCallID(first); got != "" {
		t.Fatalf("synthetic id %q would be echoed as %q", first, got)
	}
}

func TestGeminiEchoableCallID(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"gemini id", "fc-123", "fc-123"},
		{"client id", "call_abc123", "call_abc123"},
		{"empty", "", ""},
		{"synthetic", toolCallID(""), ""},
		// Right prefix, wrong length: not ours, so it is a caller id.
		{"short ulid", "call_0123456789", "call_0123456789"},
		// Right length, not valid Crockford base32.
		{"invalid ulid", "call_!!!!!!!!!!!!!!!!!!!!!!!!!!", "call_!!!!!!!!!!!!!!!!!!!!!!!!!!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := geminiEchoableCallID(tt.in); got != tt.want {
				t.Errorf("geminiEchoableCallID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// Full round trip through the agent-loop message shape: two concurrent calls
// to one function must come back as two distinguishable functionResponse
// parts carrying the ids Gemini issued.
func TestParallelToolResultsCarryCallIDs(t *testing.T) {
	p := &Provider{}
	body := p.buildRequest(context.Background(), "gemini-3-pro-preview", []service.Message{
		{Role: "user", Content: "read both"},
		{Role: "assistant", Content: []service.ContentBlock{
			{Type: "tool_use", ID: "fc-1", Name: "read_file", Input: map[string]any{"p": "a"}},
			{Type: "tool_use", ID: "fc-2", Name: "read_file", Input: map[string]any{"p": "b"}},
		}},
		{Role: "user", Content: []service.ContentBlock{
			{Type: "tool_result", ToolUseID: "fc-1", Content: "A"},
			{Type: "tool_result", ToolUseID: "fc-2", Content: "B"},
		}},
	}, nil, nil)

	calls := body.Contents[1].Parts
	if len(calls) != 2 || calls[0].FunctionCall.ID != "fc-1" || calls[1].FunctionCall.ID != "fc-2" {
		t.Fatalf("functionCall ids lost: %+v", calls)
	}
	results := body.Contents[2].Parts
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].FunctionResponse.ID != "fc-1" || results[1].FunctionResponse.ID != "fc-2" {
		t.Fatalf("functionResponse ids lost: %+v, %+v", results[0].FunctionResponse, results[1].FunctionResponse)
	}
	// Names still resolve so name-matching models keep working.
	if results[0].FunctionResponse.Name != "read_file" || results[1].FunctionResponse.Name != "read_file" {
		t.Fatalf("tool names lost: %+v", results)
	}
}

// The OpenAI-envelope path (gateway requests) must carry ids symmetrically on
// both the call and the result, so the pairing stays self-consistent even for
// ids minted by another provider earlier in the conversation.
func TestGatewayEnvelopeToolIDsRoundTrip(t *testing.T) {
	p := &Provider{}
	body := p.buildRequest(context.Background(), "gemini-2.5-pro", []service.Message{
		{Role: "user", Content: map[string]any{"role": "user", "content": "go"}},
		{Role: "assistant", Content: map[string]any{
			"role": "assistant",
			"tool_calls": []any{map[string]any{
				"id":       "call_openai_1",
				"function": map[string]any{"name": "lookup", "arguments": `{"q":"x"}`},
			}},
		}},
		{Role: "tool", Content: map[string]any{
			"role":         "tool",
			"tool_call_id": "call_openai_1",
			"content":      "found",
		}},
	}, nil, nil)

	call := body.Contents[1].Parts[0].FunctionCall
	if call == nil || call.ID != "call_openai_1" {
		t.Fatalf("envelope functionCall id lost: %+v", call)
	}
	result := body.Contents[2].Parts[0].FunctionResponse
	if result == nil || result.ID != "call_openai_1" {
		t.Fatalf("envelope functionResponse id lost: %+v", result)
	}
	if result.Name != "lookup" {
		t.Fatalf("envelope tool name lost: %+v", result)
	}
}

// A synthetic id round-tripping through history must not be sent upstream as
// a correlation handle Gemini never issued.
func TestSyntheticIDsAreNotEchoedUpstream(t *testing.T) {
	p := &Provider{}
	synthetic := toolCallID("")
	body := p.buildRequest(context.Background(), "gemini-2.5-pro", []service.Message{
		{Role: "assistant", Content: []service.ContentBlock{
			{Type: "tool_use", ID: synthetic, Name: "lookup"},
		}},
		{Role: "user", Content: []service.ContentBlock{
			{Type: "tool_result", ToolUseID: synthetic, Content: "ok"},
		}},
	}, nil, nil)

	if got := body.Contents[0].Parts[0].FunctionCall.ID; got != "" {
		t.Fatalf("synthetic functionCall id echoed: %q", got)
	}
	if got := body.Contents[1].Parts[0].FunctionResponse.ID; got != "" {
		t.Fatalf("synthetic functionResponse id echoed: %q", got)
	}
}
