package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeElicitingMCP asks for a username during tools/call and returns what
// the client answered.
func fakeElicitingMCP(t *testing.T) (*httptest.Server, *map[string]any) {
	t.Helper()
	var mu sync.Mutex
	answers := make(chan map[string]any, 1)
	var initCaps map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg map[string]any
		_ = json.NewDecoder(r.Body).Decode(&msg)
		method, _ := msg["method"].(string)
		id := msg["id"]
		switch {
		case method == "initialize":
			params, _ := msg["params"].(map[string]any)
			mu.Lock()
			initCaps, _ = params["capabilities"].(map[string]any)
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"asker","version":"1"}}}`, id)
		case strings.HasPrefix(method, "notifications/"):
			w.WriteHeader(http.StatusAccepted)
		case method == "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher := w.(http.Flusher)
			fmt.Fprint(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":\"q1\",\"method\":\"elicitation/create\",\"params\":{\"message\":\"Which user?\",\"requestedSchema\":{\"type\":\"object\",\"properties\":{\"name\":{\"type\":\"string\"},\"admin\":{\"type\":\"boolean\"}},\"required\":[\"name\"]}}}\n\n")
			flusher.Flush()
			var answer map[string]any
			select {
			case answer = <-answers:
			case <-time.After(5 * time.Second):
				t.Error("no elicitation answer")
			}
			text, _ := json.Marshal(answer)
			resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}}})
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
			flusher.Flush()
		case method == "" && msg["id"] == "q1":
			// The client's answer to our question.
			result, _ := msg["result"].(map[string]any)
			if result == nil {
				result = map[string]any{"error": msg["error"]}
			}
			answers <- result
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &initCaps
}

func TestHTTPMCPClientElicitation(t *testing.T) {
	srv, caps := fakeElicitingMCP(t)
	client, err := NewHTTPMCPClient(t.Context(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := (*caps)["elicitation"]; !ok {
		t.Fatalf("client did not declare elicitation: %v", *caps)
	}
	var asked ElicitRequest
	ctx := ContextWithElicitor(t.Context(), func(_ context.Context, req ElicitRequest) (ElicitResult, error) {
		asked = req
		return ElicitResult{Action: ElicitAccept, Content: map[string]any{"name": "ada", "admin": true}}, nil
	})
	got, err := client.CallTool(ctx, "who", nil)
	if err != nil {
		t.Fatal(err)
	}
	if asked.Server != "asker" || asked.Message != "Which user?" {
		t.Fatalf("asked = %+v", asked)
	}
	if !strings.Contains(got, `"action":"accept"`) || !strings.Contains(got, `"name":"ada"`) {
		t.Fatalf("result = %s", got)
	}

	// Without a person the question is cancelled rather than left waiting.
	got, err = client.CallTool(t.Context(), "who", nil)
	if err != nil || !strings.Contains(got, `"action":"cancel"`) {
		t.Fatalf("unattended = %s %v", got, err)
	}

	// An answer that does not fit the schema becomes cancel.
	bad := ContextWithElicitor(t.Context(), func(context.Context, ElicitRequest) (ElicitResult, error) {
		return ElicitResult{Action: ElicitAccept, Content: map[string]any{"admin": "yes"}}, nil
	})
	if got, _ = client.CallTool(bad, "who", nil); !strings.Contains(got, `"action":"cancel"`) {
		t.Fatalf("invalid answer = %s", got)
	}
}

func TestNormalizeElicitResult(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"name":  map[string]any{"type": "string"},
		"size":  map[string]any{"type": "string", "enum": []any{"s", "m"}},
		"count": map[string]any{"type": "integer"},
		"ok":    map[string]any{"type": "boolean"},
	}, "required": []any{"name"}}
	if err := ValidateElicitSchema(schema); err != nil {
		t.Fatal(err)
	}
	if err := ValidateElicitSchema(map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "object"}}}); err == nil {
		t.Fatal("nested objects are not allowed")
	}
	ok, err := NormalizeElicitResult(schema, ElicitResult{Action: ElicitAccept, Content: map[string]any{"name": "a", "size": "m", "count": float64(3), "ok": false}})
	if err != nil || ok.Content["count"] != float64(3) {
		t.Fatalf("%+v %v", ok, err)
	}
	for _, content := range []map[string]any{
		{"size": "s"},               // missing required
		{"name": "a", "size": "xl"}, // not in enum
		{"name": "a", "count": 1.5}, // not an integer
		{"name": "a", "other": "x"}, // unknown field
	} {
		if _, err := NormalizeElicitResult(schema, ElicitResult{Action: ElicitAccept, Content: content}); err == nil {
			t.Errorf("expected error for %v", content)
		}
	}
	if r, err := NormalizeElicitResult(schema, ElicitResult{Action: ElicitDecline, Content: map[string]any{"name": "x"}}); err != nil || r.Content != nil {
		t.Fatalf("decline carries no content: %+v %v", r, err)
	}
	if _, err := NormalizeElicitResult(schema, ElicitResult{Action: "maybe"}); err == nil {
		t.Fatal("unknown action")
	}
}

func TestStdioMCPClientElicitation(t *testing.T) {
	path := t.TempDir() + "/asker.sh"
	// On tools/call: ask, then echo the client's answer line back as the result.
	script := `#!/bin/sh
while read line; do
  case "$line" in
    *'"method":"initialize"'*) printf '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18","serverInfo":{"name":"asker","version":"1"}}}\n' ;;
    *'"method":"tools/call"'*)
      id=$(printf '%s' "$line" | sed 's/.*"id":\([0-9]*\).*/\1/')
      printf '{"jsonrpc":"2.0","id":"q","method":"elicitation/create","params":{"message":"Name?","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}}}\n'
      read answer
      case "$answer" in
        *'"name":"ada"'*) text=got-ada ;;
        *) text=no-answer ;;
      esac
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"%s"}]}}\n' "$id" "$text" ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	client, err := NewStdioMCPClient(t.Context(), "/bin/sh", []string{path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close() //nolint:errcheck
	ctx := ContextWithElicitor(t.Context(), func(context.Context, ElicitRequest) (ElicitResult, error) {
		return ElicitResult{Action: ElicitAccept, Content: map[string]any{"name": "ada"}}, nil
	})
	got, err := client.CallTool(ctx, "who", nil)
	if err != nil || got != "got-ada" {
		t.Fatalf("result = %q %v", got, err)
	}
}
