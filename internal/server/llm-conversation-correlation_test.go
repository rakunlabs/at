package server

import (
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
