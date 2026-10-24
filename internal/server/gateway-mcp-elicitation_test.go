package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestGatewayMCPSessionCapabilities(t *testing.T) {
	g := newGatewayMCPSessions()
	both := g.open("srv", "token:a", map[string]any{"capabilities": map[string]any{"elicitation": map[string]any{"form": map[string]any{}, "url": map[string]any{}}}})
	legacy := g.open("srv", "token:a", map[string]any{"capabilities": map[string]any{"elicitation": map[string]any{}}})
	none := g.open("srv", "token:a", map[string]any{"capabilities": map[string]any{}})
	if s := g.lookup(both, "srv", "token:a"); s == nil || !s.form || !s.url {
		t.Fatalf("both = %+v", s)
	}
	if s := g.lookup(legacy, "srv", "token:a"); s == nil || !s.form || s.url {
		t.Fatalf("legacy empty object means form only: %+v", s)
	}
	if s := g.lookup(none, "srv", "token:a"); s == nil || s.form || s.url {
		t.Fatalf("none = %+v", s)
	}
	if g.lookup(both, "srv", "token:b") != nil || g.lookup(both, "other", "token:a") != nil {
		t.Fatal("a session must be bound to its server and credential")
	}
}

// The relay sends the upstream's question on the call's SSE response and
// passes the client's POSTed answer back.
func TestGatewayMCPElicitationRelay(t *testing.T) {
	s := &Server{gatewayMCP: newGatewayMCPSessions()}
	srv := &service.MCPServer{ID: "srv"}
	sessionID := s.gatewayMCP.open("srv", "anonymous", map[string]any{"capabilities": map[string]any{"elicitation": map[string]any{"form": map[string]any{}}}})

	schema := map[string]any{"type": "object", "properties": map[string]any{"name": map[string]any{"type": "string"}}}
	call := httptest.NewRequest(http.MethodPost, "/gateway/v1/mcp/x", nil)
	call.Header.Set("Accept", "application/json, text/event-stream")
	call.Header.Set("Mcp-Session-Id", sessionID)
	elicitCtx, events := s.gatewayMCPElicitationContext(call.Context(), call, srv)
	if events == nil {
		t.Fatal("relay not installed")
	}

	pr, pw := ioPipeWriter()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveMCPCallEvents(pw, call, 5, nil, events, func(ctx context.Context) (any, *service.MCPError) {
			ask := service.ElicitorFromContext(mergeElicitation(ctx, elicitCtx))
			res, err := ask(ctx, service.ElicitRequest{Mode: service.ElicitModeForm, Message: "Name?", RequestedSchema: schema})
			if err != nil {
				return nil, &service.MCPError{Code: -32000, Message: err.Error()}
			}
			return map[string]any{"answer": res}, nil
		})
		pw.Close()
	}()

	reader := bufio.NewReader(pr)
	var questionID string
	deadline := time.After(5 * time.Second)
	for questionID == "" {
		select {
		case <-deadline:
			t.Fatal("no question on the stream")
		default:
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := strings.CutPrefix(strings.TrimSpace(line), "data: "); ok {
			var msg map[string]any
			_ = json.Unmarshal([]byte(data), &msg)
			if msg["method"] == "elicitation/create" {
				questionID, _ = msg["id"].(string)
			}
		}
	}

	// A response from another session is refused.
	other := s.gatewayMCP.open("srv", "anonymous", nil)
	if s.gatewayMCP.deliver(questionID, other, service.ElicitResult{Action: service.ElicitAccept}) {
		t.Fatal("another session answered the question")
	}
	answer := httptest.NewRequest(http.MethodPost, "/gateway/v1/mcp/x", nil)
	answer.Header.Set("Mcp-Session-Id", sessionID)
	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": questionID, "result": map[string]any{"action": "accept", "content": map[string]any{"name": "ada"}}})
	if !s.gatewayMCPClientResponse(w, answer, srv, body) || w.Code != http.StatusAccepted {
		t.Fatalf("answer status %d", w.Code)
	}
	rest, _ := readAll(reader)
	<-done
	if !strings.Contains(rest, `"id":5`) || !strings.Contains(rest, `"name":"ada"`) {
		t.Fatalf("final event = %s", rest)
	}
}

// Without a session that declared elicitation there is no relay.
func TestGatewayMCPElicitationNeedsSession(t *testing.T) {
	s := &Server{gatewayMCP: newGatewayMCPSessions()}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Header.Set("Accept", "text/event-stream")
	r.Header.Set("Mcp-Session-Id", "unknown")
	if _, events := s.gatewayMCPElicitationContext(r.Context(), r, &service.MCPServer{ID: "srv"}); events != nil {
		t.Fatal("unknown session must not relay")
	}
}
