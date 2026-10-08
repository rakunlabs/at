package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestServeMCPCallKeepsSlowCallsAlive(t *testing.T) {
	previous := mcpCallKeepAlive
	mcpCallKeepAlive = 20 * time.Millisecond
	t.Cleanup(func() { mcpCallKeepAlive = previous })

	slow := func(ctx context.Context) (any, *service.MCPError) {
		time.Sleep(90 * time.Millisecond)
		return map[string]any{"content": []service.ToolContent{{Type: "text", Text: "done"}}}, nil
	}
	fast := func(ctx context.Context) (any, *service.MCPError) {
		return map[string]any{"content": []service.ToolContent{{Type: "text", Text: "done"}}}, nil
	}

	tests := []struct {
		name     string
		accept   string
		call     func(context.Context) (any, *service.MCPError)
		wantType string
		wantBody []string
	}{
		{name: "fast call stays JSON", accept: "application/json, text/event-stream", call: fast, wantType: "application/json", wantBody: []string{`"id":7`, `"done"`}},
		{name: "slow call switches to SSE with progress", accept: "application/json, text/event-stream", call: slow, wantType: "text/event-stream", wantBody: []string{": keep-alive", "notifications/progress", `"progressToken":"tok"`, `"id":7`, `"done"`}},
		{name: "JSON-only client waits for the result", accept: "application/json", call: slow, wantType: "application/json", wantBody: []string{`"id":7`, `"done"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/gateway/v1/mcp/test", bytes.NewReader(nil))
			r.Header.Set("Accept", tt.accept)
			w := httptest.NewRecorder()
			serveMCPCall(w, r, 7, "tok", tt.call)
			if got := w.Header().Get("Content-Type"); got != tt.wantType {
				t.Fatalf("content type = %q, want %q", got, tt.wantType)
			}
			body := w.Body.String()
			for _, want := range tt.wantBody {
				if !strings.Contains(body, want) {
					t.Fatalf("body %q does not contain %q", body, want)
				}
			}
			if tt.wantType == "text/event-stream" && strings.Index(body, "notifications/progress") > strings.Index(body, `"id":7`) {
				t.Fatalf("result must be the final event: %q", body)
			}
		})
	}
}
