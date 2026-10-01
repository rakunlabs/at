package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuiltinTodoHTTPSessionIsolation(t *testing.T) {
	s := &Server{todos: newTodoStore()}
	call := func(run, session, name string, args map[string]any) string {
		t.Helper()
		body, err := json.Marshal(builtinCallRequest{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		ctx := nonHostToolContext(t, "u1", "w1", run, "", "todo_read", "todo_write")
		r := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/call-builtin-tool", strings.NewReader(string(body))).WithContext(ctx)
		if session != "" {
			r.Header.Set("X-Session-ID", session)
		}
		w := httptest.NewRecorder()
		s.BuiltinToolCallAPI(w, r)
		var got builtinCallResponse
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusOK || got.Error != "" {
			t.Fatalf("tool call: %d, %+v", w.Code, got)
		}
		return got.Result
	}
	args := map[string]any{"todos": []todoItem{{Content: "conversation A todo", Status: "pending", Priority: "medium"}}}
	call("r1", "conversation-a", "todo_write", args)
	if raw := call("r2", "conversation-a", "todo_read", nil); !strings.Contains(raw, "conversation A todo") {
		t.Fatalf("same conversation lost todos: %s", raw)
	}
	if raw := call("r3", "conversation-b", "todo_read", nil); strings.Contains(raw, "conversation A todo") {
		t.Fatalf("foreign conversation saw todos: %s", raw)
	}
	call("r4", "", "todo_write", args)
	if raw := call("r5", "", "todo_read", nil); strings.Contains(raw, "conversation A todo") {
		t.Fatalf("missing session shared todos: %s", raw)
	}
}
