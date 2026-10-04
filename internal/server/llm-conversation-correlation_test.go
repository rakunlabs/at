package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayConversationCorrelation(t *testing.T) {
	for _, tt := range []struct {
		name, header, value string
		metadata            map[string]any
		want                string
	}{
		{"AT header", "X-AT-Session-ID", "at-chat", map[string]any{"session_id": "metadata-chat"}, "at-chat"},
		{"SDK header", "X-Session-Id", "sdk-chat", nil, "sdk-chat"},
		{"OpenCode header", "X-Opencode-Session", "ses-1", nil, "ses-1"},
		{"session metadata", "", "", map[string]any{"session_id": "metadata-chat"}, "metadata-chat"},
		{"conversation metadata", "", "", map[string]any{"conversation_id": "conversation-1"}, "conversation-1"},
		{"no cache inference", "", "", map[string]any{"prompt_cache_key": "shared-prompt", "user": "same-user"}, ""},
		{"non string metadata", "", "", map[string]any{"session_id": 42}, ""},
		{"invalid correlation does not break persistence", "", "", map[string]any{"session_id": "bad\x00id"}, ""},
		{"bounded correlation", "", "", map[string]any{"session_id": strings.Repeat("x", 257)}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/gateway/v1/chat/completions", nil)
			if tt.header != "" {
				r.Header.Set(tt.header, tt.value)
			}
			_, session := auditTraceInfo(r, tt.metadata)
			if session != tt.want {
				t.Fatalf("session %q want %q", session, tt.want)
			}
		})
	}
}

func TestConversationTurnTraceID(t *testing.T) {
	turn1 := `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"fix the bug"}]}`
	turn1Step2 := `{"messages":[{"role":"system","content":"sys"},{"role":"user","content":"fix the bug"},
		{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read","arguments":"{}"}}]},
		{"role":"tool","tool_call_id":"c1","content":"file"}]}`
	turn2 := `{"messages":[{"role":"user","content":"fix the bug"},{"role":"assistant","content":"done"},{"role":"user","content":"thanks"}]}`
	anthropicStep := `{"messages":[{"role":"user","content":[{"type":"text","text":"fix the bug"}]},
		{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"file"}]}]}`
	anthropicTurn1 := `{"messages":[{"role":"user","content":[{"type":"text","text":"fix the bug"}]}]}`
	responsesStep := `{"input":[{"role":"user","content":[{"type":"input_text","text":"fix the bug"}]},{"type":"function_call_output","call_id":"c1","output":"x"}]}`

	id := conversationTurnTraceID("ws\x00tok", "ses-1", []byte(turn1))
	if id == "" {
		t.Fatal("expected a derived trace ID")
	}
	if got := conversationTurnTraceID("ws\x00tok", "ses-1", []byte(turn1Step2)); got != id {
		t.Fatalf("tool step of the same turn got %q, want %q", got, id)
	}
	if got := conversationTurnTraceID("ws\x00tok", "ses-1", []byte(turn2)); got == id || got == "" {
		t.Fatalf("next user turn must start a new trace, got %q", got)
	}
	if got := conversationTurnTraceID("ws\x00tok", "ses-2", []byte(turn1)); got == id {
		t.Fatal("another session must not share the trace")
	}
	if got := conversationTurnTraceID("ws\x00other", "ses-1", []byte(turn1)); got == id {
		t.Fatal("another token must not share the trace")
	}
	if got := conversationTurnTraceID("ws\x00tok", "", []byte(turn1)); got != "" {
		t.Fatalf("no session must not derive a trace, got %q", got)
	}
	if a, b := conversationTurnTraceID("s", "x", []byte(anthropicTurn1)), conversationTurnTraceID("s", "x", []byte(anthropicStep)); a == "" || a != b {
		t.Fatalf("anthropic tool_result must not open a turn: %q vs %q", a, b)
	}
	if a, b := conversationTurnTraceID("s", "x", []byte(`{"input":"fix the bug"}`)), conversationTurnTraceID("s", "x", []byte(responsesStep)); a == "" || a != b {
		t.Fatalf("responses function_call_output must not open a turn: %q vs %q", a, b)
	}
	if got := gatewayTurnTrace("client-trace", "ses-1", nil, []byte(turn1)); got != "client-trace" {
		t.Fatalf("explicit trace ID must win, got %q", got)
	}
}

func TestAdminChatConversationCorrelation(t *testing.T) {
	s, costs, calls := meteredProxyServer(t, &countingStreamProvider{name: "reply"}, "")
	seenTraces := map[string]bool{}
	for i, session := range []string{"conversation-1", "conversation-1", "conversation-2"} {
		body := fmt.Sprintf(`{"model":"anthropic/claude-3-5-sonnet","stream":true,"messages":[{"role":"user","content":"hello"}],"metadata":{"session_id":%q}}`, session)
		r := httptest.NewRequest(http.MethodPost, "/api/v1/chat/completions", strings.NewReader(body))
		w := httptest.NewRecorder()
		s.AdminChatCompletions(w, r)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "reply") {
			t.Fatalf("chat response %d: %s", w.Code, w.Body)
		}
		_, observations := waitForRecords(t, costs, calls, 0, i+1)
		call := observations[i]
		if call.SessionID != session || call.Source != "chat" {
			t.Fatalf("chat correlation: session=%q source=%q, want %q/chat", call.SessionID, call.Source, session)
		}
		if call.TraceID == "" || seenTraces[call.TraceID] {
			t.Fatalf("each request needs its own trace, got %q", call.TraceID)
		}
		seenTraces[call.TraceID] = true
	}
}
