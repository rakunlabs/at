package agentloop

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestIsToolPairingError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "anthropic does not follow", err: errors.New("tool call result does not follow tool call"), want: true},
		{name: "anthropic tool_use block", err: errors.New("unexpected tool_use content block"), want: true},
		{name: "anthropic tool_result", err: errors.New("tool_result block does not follow a tool_use"), want: true},
		{name: "openai tool id", err: errors.New("tool id (call_1) not found"), want: true},
		{name: "openai tool_call_id", err: errors.New("tool_call_id call_1 not found"), want: true},
		{name: "openai role tool", err: errors.New("messages with role 'tool' must be a response to a preceding message with 'tool_calls'"), want: true},
		{name: "openai must be followed", err: errors.New("an assistant message with 'tool_calls' must be followed by tool messages"), want: true},
		{name: "unrelated", err: errors.New("rate limit exceeded"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsToolPairingError(tt.err); got != tt.want {
				t.Fatalf("IsToolPairingError() = %v, want %v", got, tt.want)
			}
		})
	}
	if IsToolPairingMessage("") {
		t.Fatal("empty message must not match")
	}
}

func chatCall(ids ...string) service.ChatMessage {
	calls := make([]any, 0, len(ids))
	for _, id := range ids {
		calls = append(calls, map[string]any{"id": id})
	}
	return service.ChatMessage{Role: "assistant", Data: service.ChatMessageData{ToolCalls: calls}}
}

func chatResult(id string) service.ChatMessage {
	return service.ChatMessage{Role: "tool", Data: service.ChatMessageData{ToolCallID: id}}
}

func chatText(role, content string) service.ChatMessage {
	return service.ChatMessage{Role: role, Data: service.ChatMessageData{Content: content}}
}

func TestSanitizeChatHistory(t *testing.T) {
	tests := []struct {
		name string
		in   []service.ChatMessage
		want []service.ChatMessage
	}{
		{name: "empty", in: nil, want: nil},
		{
			name: "complete pair kept",
			in:   []service.ChatMessage{chatText("user", "hi"), chatCall("a"), chatResult("a"), chatText("assistant", "done")},
			want: []service.ChatMessage{chatText("user", "hi"), chatCall("a"), chatResult("a"), chatText("assistant", "done")},
		},
		{
			name: "partial results drop the whole block",
			in:   []service.ChatMessage{chatText("user", "hi"), chatCall("a", "b"), chatResult("a"), chatText("user", "next")},
			want: []service.ChatMessage{chatText("user", "hi"), chatText("user", "next")},
		},
		{
			name: "orphan and empty-id results dropped",
			in:   []service.ChatMessage{chatText("user", "hi"), chatResult("ghost"), chatResult("")},
			want: []service.ChatMessage{chatText("user", "hi")},
		},
		{
			name: "typed map slice and ID key variants",
			in: []service.ChatMessage{
				{Role: "assistant", Data: service.ChatMessageData{ToolCalls: []map[string]any{{"ID": "x"}}}},
				chatResult("x"),
			},
			want: []service.ChatMessage{
				{Role: "assistant", Data: service.ChatMessageData{ToolCalls: []map[string]any{{"ID": "x"}}}},
				chatResult("x"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeChatHistory(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SanitizeChatHistory() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func msgUse(ids ...string) service.Message {
	blocks := []service.ContentBlock{{Type: "text", Text: "calling"}}
	for _, id := range ids {
		blocks = append(blocks, service.ContentBlock{Type: "tool_use", ID: id, Name: "t"})
	}
	return service.Message{Role: "assistant", Content: blocks}
}

func msgResult(ids ...string) service.Message {
	blocks := make([]service.ContentBlock, 0, len(ids))
	for _, id := range ids {
		blocks = append(blocks, service.ContentBlock{Type: "tool_result", ToolUseID: id, Content: "ok"})
	}
	return service.Message{Role: "user", Content: blocks}
}

func TestSanitizeMessages(t *testing.T) {
	system := service.Message{Role: "system", Content: "sys"}
	user := service.Message{Role: "user", Content: "do it"}

	t.Run("intact history returned as is", func(t *testing.T) {
		in := []service.Message{system, user, msgUse("a"), msgResult("a")}
		got := SanitizeMessages(in)
		if &got[0] != &in[0] || len(got) != len(in) {
			t.Fatal("intact history must be returned unchanged")
		}
	})

	t.Run("unanswered call and its partial results dropped", func(t *testing.T) {
		in := []service.Message{system, user, msgUse("a", "b"), msgResult("a"), msgUse("c"), msgResult("c")}
		want := []service.Message{system, user, msgUse("c"), msgResult("c")}
		if got := SanitizeMessages(in); !reflect.DeepEqual(got, want) {
			t.Fatalf("SanitizeMessages() = %#v, want %#v", got, want)
		}
	})

	t.Run("orphan result dropped once something is broken", func(t *testing.T) {
		in := []service.Message{user, msgResult("ghost"), msgUse("a")}
		want := []service.Message{user}
		if got := SanitizeMessages(in); !reflect.DeepEqual(got, want) {
			t.Fatalf("SanitizeMessages() = %#v, want %#v", got, want)
		}
	})
}

type fakeMCPClient struct {
	result string
	err    error
	calls  int
}

func (f *fakeMCPClient) CallTool(_ context.Context, _ string, _ map[string]any) (string, error) {
	f.calls++
	return f.result, f.err
}

func (f *fakeMCPClient) ListTools(context.Context) ([]service.Tool, error) { return nil, nil }

func (f *fakeMCPClient) Close() error { return nil }

func TestCallMCPTool(t *testing.T) {
	failing := &fakeMCPClient{err: errors.New("unknown tool")}
	answering := &fakeMCPClient{result: "ok"}
	unreached := &fakeMCPClient{result: "late"}

	got, err := CallMCPTool(context.Background(), []service.MCPClient{failing, answering, unreached}, "t", nil)
	if err != nil || got != "ok" {
		t.Fatalf("CallMCPTool() = %q, %v; want ok", got, err)
	}
	if unreached.calls != 0 {
		t.Fatal("clients after the first answer must not be called")
	}

	if _, err := CallMCPTool(context.Background(), []service.MCPClient{failing}, "t", nil); err == nil {
		t.Fatal("expected error when no client answers")
	}
}
