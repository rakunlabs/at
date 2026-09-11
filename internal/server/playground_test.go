package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/password"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

// playgroundFixture returns a server plus the user IDs and bearer tokens of
// "admin-a", "admin-b" and a nonadministrator "reader".
func playgroundFixture(t *testing.T) (*Server, []string, []string) {
	t.Helper()
	p := postgrestest.New(t, nil)
	cfg := nativeTestConfig()
	cfg.Workspace = &config.Workspace{Root: t.TempDir(), TTLHours: -1}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	s, err := New(ctx, cfg, nil, p, "postgres", nil, nil, "test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	owners, tokens := []string{}, []string{}
	for i, name := range []string{"admin-a", "admin-b", "reader"} {
		// The first administrator claims the installation.
		u, err := p.CreateAuthUser(ctx, service.AuthUser{Username: name, Admin: name != "reader", PasswordHash: password.Dummy}, i == 0)
		if err != nil {
			t.Fatal(err)
		}
		access, refresh, err := nativeCredentialPair()
		if err != nil {
			t.Fatal(err)
		}
		if err := p.CreateAuthSession(ctx, service.AuthSession{Hash: name, UserID: u.ID, Version: u.SessionVersion, Transport: "mobile", AccessHash: nativeSessionHash(access), RefreshHash: nativeSessionHash(refresh), ExpiresAt: time.Now().Add(time.Hour), AccessExpiresAt: time.Now().Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
		owners = append(owners, u.ID)
		tokens = append(tokens, access)
	}
	return s, owners, tokens
}

func playgroundRequest(s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/playground/conversations"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	return w
}

func playgroundNewConversation(t *testing.T, s *Server, token string) service.PlaygroundConversation {
	t.Helper()
	w := playgroundRequest(s, token, "POST", "", `{"title":"Scratch","system_prompt":"be brief","provider_key":"test","model":"text-model","config":{"tools":["bash_execute"]}}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var c service.PlaygroundConversation
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func playgroundMessages(t *testing.T, w *httptest.ResponseRecorder) []service.PlaygroundMessage {
	t.Helper()
	var out struct {
		Data []service.PlaygroundMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return out.Data
}

func TestPlaygroundHTTPContract(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)

	for _, tc := range []struct {
		name, token string
		status      int
	}{{"anonymous", "", 401}, {"nonadministrator", tokens[2], 403}} {
		t.Run(tc.name, func(t *testing.T) {
			if w := playgroundRequest(s, tc.token, "GET", "", ""); w.Code != tc.status {
				t.Fatal(w.Code, w.Body)
			}
		})
	}

	c := playgroundNewConversation(t, s, tokens[0])
	if c.OwnerUserID != owners[0] || c.Title != "Scratch" || c.Config["tools"] == nil {
		t.Fatalf("create: %+v", c)
	}

	w := playgroundRequest(s, tokens[0], "GET", "?limit=1", "")
	var list struct {
		Data []service.PlaygroundConversation `json:"data"`
		Meta struct {
			Limit      uint   `json:"limit"`
			NextBefore string `json:"next_before"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(list.Data) != 1 || list.Data[0].ID != c.ID || list.Meta.Limit != 1 || list.Meta.NextBefore != c.ID {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}

	w = playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"user","provider_key":"test","model":"text-model","data":{"content":"hello"}},{"role":"assistant","provider_key":"test","model":"text-model","data":{"content":"hi","tool_calls":[]}},{"role":"tool","data":{"tool_call_id":"call_1","content":"ok"}}]}`)
	if w.Code != 201 {
		t.Fatalf("append: %d %s", w.Code, w.Body)
	}
	appended := playgroundMessages(t, w)
	if len(appended) != 3 || appended[0].Sequence != 1 || appended[1].Sequence != 2 || appended[2].Sequence != 3 {
		t.Fatalf("sequences: %+v", appended)
	}

	w = playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", "")
	stored := playgroundMessages(t, w)
	if w.Code != 200 || len(stored) != 3 || stored[0].Data["content"] != "hello" || stored[2].Role != "tool" {
		t.Fatalf("list messages: %d %s", w.Code, w.Body)
	}

	if w = playgroundRequest(s, tokens[0], "PATCH", "/"+c.ID, `{"title":"Renamed","config":{"mcp_sets":["local"]}}`); w.Code != 200 {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	var patched service.PlaygroundConversation
	if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Title != "Renamed" || patched.Config["mcp_sets"] == nil {
		t.Fatalf("patch body: %+v", patched)
	}

	if w = playgroundRequest(s, tokens[0], "DELETE", "/"+c.ID+"/messages?from_sequence=2", ""); w.Code != 204 {
		t.Fatalf("truncate: %d %s", w.Code, w.Body)
	}
	kept := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", ""))
	if len(kept) != 1 || kept[0].Sequence != 1 {
		t.Fatalf("truncate kept: %+v", kept)
	}

	if w = playgroundRequest(s, tokens[0], "GET", "/"+c.ID, ""); w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	if w = playgroundRequest(s, tokens[0], "DELETE", "/"+c.ID, ""); w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w = playgroundRequest(s, tokens[0], "GET", "/"+c.ID, ""); w.Code != 404 {
		t.Fatalf("get after delete: %d %s", w.Code, w.Body)
	}
}

func TestPlaygroundHTTPOwnerIsolation(t *testing.T) {
	s, _, tokens := playgroundFixture(t)
	c := playgroundNewConversation(t, s, tokens[0])
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"user","data":{"content":"private"}}]}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	// Every foreign access is a 404, never a 403: existence must not leak.
	for _, tc := range []struct{ name, method, path, body string }{
		{"get", "GET", "/" + c.ID, ""},
		{"patch", "PATCH", "/" + c.ID, `{"title":"stolen"}`},
		{"delete", "DELETE", "/" + c.ID, ""},
		{"list messages", "GET", "/" + c.ID + "/messages", ""},
		{"append", "POST", "/" + c.ID + "/messages", `{"messages":[{"role":"user","data":{}}]}`},
		{"truncate", "DELETE", "/" + c.ID + "/messages?from_sequence=1", ""},
		{"fork", "POST", "/" + c.ID + "/fork", `{"from_sequence":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := playgroundRequest(s, tokens[1], tc.method, tc.path, tc.body); w.Code != 404 {
				t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
			}
		})
	}
	if w := playgroundRequest(s, tokens[1], "GET", "", ""); w.Code != 200 || strings.Contains(w.Body.String(), c.ID) {
		t.Fatalf("foreign listing leaked: %d %s", w.Code, w.Body)
	}
	// The owner still sees everything after the failed attempts.
	if kept := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", "")); len(kept) != 1 {
		t.Fatalf("owner history damaged: %+v", kept)
	}
}

func TestPlaygroundHTTPValidation(t *testing.T) {
	s, _, tokens := playgroundFixture(t)
	c := playgroundNewConversation(t, s, tokens[0])
	oversized, err := json.Marshal(map[string]any{"messages": []any{map[string]any{"role": "user", "data": map[string]any{"content": strings.Repeat("x", playgroundPayloadMaxBytes+1)}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"invalid role", "POST", "/" + c.ID + "/messages", `{"messages":[{"role":"system","data":{}}]}`, 400},
		{"empty batch", "POST", "/" + c.ID + "/messages", `{"messages":[]}`, 400},
		{"unknown field", "POST", "/" + c.ID + "/messages", `{"messages":[{"role":"user","sequence":9}]}`, 400},
		{"oversized data", "POST", "/" + c.ID + "/messages", string(oversized), 413},
		{"empty patch", "PATCH", "/" + c.ID, `{}`, 400},
		{"patch unknown field", "PATCH", "/" + c.ID, `{"owner_user_id":"stolen"}`, 400},
		{"truncate without cursor", "DELETE", "/" + c.ID + "/messages", "", 400},
		{"truncate zero", "DELETE", "/" + c.ID + "/messages?from_sequence=0", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := playgroundRequest(s, tokens[0], tc.method, tc.path, tc.body)
			if w.Code != tc.status {
				t.Fatalf("%s: got %d want %d: %s", tc.name, w.Code, tc.status, w.Body)
			}
		})
	}
	// An oversized payload must be rejected, not silently truncated.
	if kept := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", "")); len(kept) != 0 {
		t.Fatalf("rejected payloads were stored: %+v", kept)
	}
	// The limit is clamped rather than rejected.
	if w := playgroundRequest(s, tokens[0], "GET", "?limit=100000", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"limit":200`) {
		t.Fatalf("limit clamp: %d %s", w.Code, w.Body)
	}
}

// playgroundSeedMessages appends n user messages through the API, respecting
// the per-request batch cap.
func playgroundSeedMessages(t *testing.T, s *Server, token, id string, n int) {
	t.Helper()
	for sent := 0; sent < n; {
		batch := min(n-sent, playgroundMessageBatchMax)
		items := make([]any, 0, batch)
		for i := range batch {
			items = append(items, map[string]any{"role": "user", "data": map[string]any{"content": sent + i}})
		}
		body, err := json.Marshal(map[string]any{"messages": items})
		if err != nil {
			t.Fatal(err)
		}
		if w := playgroundRequest(s, token, "POST", "/"+id+"/messages", string(body)); w.Code != 201 {
			t.Fatalf("seed: %d %s", w.Code, w.Body)
		}
		sent += batch
	}
}

func TestPlaygroundHTTPFork(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)
	c := playgroundNewConversation(t, s, tokens[0])
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/messages", `{"messages":[{"role":"user","provider_key":"test","model":"text-model","data":{"content":"one"}},{"role":"assistant","provider_key":"test","model":"text-model","data":{"content":"two"}},{"role":"user","data":{"content":"three"}}]}`); w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}

	w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/fork", `{"from_sequence":2}`)
	if w.Code != 201 {
		t.Fatalf("fork: %d %s", w.Code, w.Body)
	}
	var forked service.PlaygroundConversation
	if err := json.Unmarshal(w.Body.Bytes(), &forked); err != nil {
		t.Fatal(err)
	}
	if forked.ID == c.ID || forked.OwnerUserID != owners[0] {
		t.Fatalf("fork identity: %+v", forked)
	}
	if forked.ForkedFromID != c.ID || forked.ForkedFromSequence != 2 || forked.Title != "Scratch (fork)" {
		t.Fatalf("fork lineage: %+v", forked)
	}
	if forked.SystemPrompt != "be brief" || forked.ProviderKey != "test" || forked.Model != "text-model" || forked.Config["tools"] == nil {
		t.Fatalf("fork settings: %+v", forked)
	}
	copied := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+forked.ID+"/messages", ""))
	if len(copied) != 2 || copied[0].Sequence != 1 || copied[1].Sequence != 2 || copied[0].Data["content"] != "one" || copied[1].Data["content"] != "two" {
		t.Fatalf("copied prefix: %+v", copied)
	}
	// The source keeps every message it had.
	if kept := playgroundMessages(t, playgroundRequest(s, tokens[0], "GET", "/"+c.ID+"/messages", "")); len(kept) != 3 {
		t.Fatalf("source damaged: %+v", kept)
	}
	// A conversation with no lineage omits the fields entirely.
	if body := playgroundRequest(s, tokens[0], "GET", "/"+c.ID, "").Body.String(); strings.Contains(body, "forked_from") {
		t.Fatalf("unforked conversation advertises lineage: %s", body)
	}

	// An explicit title wins, and a fork of a fork points at its parent.
	w = playgroundRequest(s, tokens[0], "POST", "/"+forked.ID+"/fork", `{"from_sequence":1,"title":"Branch B"}`)
	if w.Code != 201 {
		t.Fatalf("fork of fork: %d %s", w.Code, w.Body)
	}
	var second service.PlaygroundConversation
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.ForkedFromID != forked.ID || second.ForkedFromSequence != 1 || second.Title != "Branch B" {
		t.Fatalf("fork of fork: %+v", second)
	}

	for _, tc := range []struct {
		name, path, body string
		status           int
	}{
		{"missing from_sequence", "/" + c.ID + "/fork", `{}`, 400},
		{"zero", "/" + c.ID + "/fork", `{"from_sequence":0}`, 400},
		{"negative", "/" + c.ID + "/fork", `{"from_sequence":-1}`, 400},
		{"empty body", "/" + c.ID + "/fork", ``, 400},
		{"unknown field", "/" + c.ID + "/fork", `{"from_sequence":1,"owner_user_id":"stolen"}`, 400},
		{"past the end", "/" + c.ID + "/fork", `{"from_sequence":9}`, 409},
		{"unknown conversation", "/missing/fork", `{"from_sequence":1}`, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if w := playgroundRequest(s, tokens[0], "POST", tc.path, tc.body); w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body)
			}
		})
	}
	// Foreign forks are 404, never 403: existence must not leak.
	if w := playgroundRequest(s, tokens[1], "POST", "/"+c.ID+"/fork", `{"from_sequence":1}`); w.Code != 404 {
		t.Fatalf("foreign fork: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		name, token string
		status      int
	}{{"anonymous", "", 401}, {"nonadministrator", tokens[2], 403}} {
		t.Run(tc.name, func(t *testing.T) {
			if w := playgroundRequest(s, tc.token, "POST", "/"+c.ID+"/fork", `{"from_sequence":1}`); w.Code != tc.status {
				t.Fatal(w.Code, w.Body)
			}
		})
	}
}

func TestPlaygroundHTTPForkTooLarge(t *testing.T) {
	s, _, tokens := playgroundFixture(t)
	c := playgroundNewConversation(t, s, tokens[0])
	playgroundSeedMessages(t, s, tokens[0], c.ID, service.PlaygroundForkMaxMessages+1)

	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/fork", `{"from_sequence":`+strconv.Itoa(service.PlaygroundForkMaxMessages+1)+`}`); w.Code != 413 {
		t.Fatalf("oversized prefix: %d %s", w.Code, w.Body)
	}
	// The boundary itself is still forkable.
	if w := playgroundRequest(s, tokens[0], "POST", "/"+c.ID+"/fork", `{"from_sequence":`+strconv.Itoa(service.PlaygroundForkMaxMessages)+`}`); w.Code != 201 {
		t.Fatalf("cap boundary: %d %s", w.Code, w.Body)
	}
}

func TestPlaygroundNativeAuthOff(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.PlaygroundConversationsAPI(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "requires native authentication") {
		t.Fatal(w.Code, w.Body)
	}
}
