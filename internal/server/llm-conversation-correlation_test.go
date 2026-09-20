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
