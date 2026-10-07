package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func chatCommandsRequest(s *Server, token, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/chats/"+path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-AT-Workspace-ID", service.DefaultWorkspaceID)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)

	return w
}

// Personal commands are per account with server-assigned identity; built-in
// names cannot be shadowed, so `/compact` always means compaction.
func TestChatCommandsOwnerScopeAndValidation(t *testing.T) {
	s, _, tokens := playgroundFixture(t)

	if w := chatCommandsRequest(s, "", "GET", "commands", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body)
	}
	if w := chatCommandsRequest(s, tokens[0], "GET", "commands", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"commands":[]}` {
		t.Fatalf("empty list: %d %s", w.Code, w.Body)
	}

	w := chatCommandsRequest(s, tokens[0], "PUT", "commands", `{"commands":[{"id":"forged","name":"/Review","description":" Review a diff ","template":"Review this: $ARGUMENTS"}]}`)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	var saved chatCommandRegistry
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Commands) != 1 || saved.Commands[0].ID == "forged" || saved.Commands[0].ID == "" || saved.Commands[0].Name != "review" || saved.Commands[0].Description != "Review a diff" {
		t.Fatalf("normalization/identity: %+v", saved.Commands)
	}
	if w := chatCommandsRequest(s, tokens[1], "GET", "commands", ""); strings.TrimSpace(w.Body.String()) != `{"commands":[]}` {
		t.Fatalf("other account sees commands: %s", w.Body)
	}

	for name, body := range map[string]string{
		"reserved":  `{"commands":[{"name":"compact","template":"x"}]}`,
		"invalid":   `{"commands":[{"name":"has space","template":"x"}]}`,
		"duplicate": `{"commands":[{"name":"a","template":"x"},{"name":"A","template":"y"}]}`,
		"empty":     `{"commands":[{"name":"a","template":"  "}]}`,
	} {
		if w := chatCommandsRequest(s, tokens[0], "PUT", "commands", body); w.Code != 400 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
}

// Workspace commands are visible to every member; only the creator changes them.
func TestWorkspaceChatCommandSharingAndOwnership(t *testing.T) {
	s, owners, tokens := playgroundFixture(t)

	w := chatCommandsRequest(s, tokens[0], "POST", "workspace-commands", `{"name":"standup","description":"Daily summary","template":"Summarise yesterday for $1"}`)
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created service.WorkspaceChatCommand
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.OwnerUserID != owners[0] || !created.CanEdit {
		t.Fatalf("owner stamp: %+v", created)
	}

	w = chatCommandsRequest(s, tokens[1], "GET", "workspace-commands", "")
	var list workspaceChatCommandList
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Commands) != 1 || list.Commands[0].CanEdit {
		t.Fatalf("member list: %d %s", w.Code, w.Body)
	}
	if denied := chatCommandsRequest(s, tokens[1], "PUT", "workspace-commands/"+created.ID, `{"name":"standup","template":"hijacked"}`); denied.Code != 404 {
		t.Fatalf("foreign update: %d %s", denied.Code, denied.Body)
	}
	if denied := chatCommandsRequest(s, tokens[1], "DELETE", "workspace-commands/"+created.ID, ""); denied.Code != 404 {
		t.Fatalf("foreign delete: %d %s", denied.Code, denied.Body)
	}
	if conflict := chatCommandsRequest(s, tokens[1], "POST", "workspace-commands", `{"name":"standup","template":"x"}`); conflict.Code != 409 {
		t.Fatalf("name conflict: %d %s", conflict.Code, conflict.Body)
	}
	if w := chatCommandsRequest(s, tokens[0], "PUT", "workspace-commands/"+created.ID, `{"name":"standup","template":"Updated $ARGUMENTS"}`); w.Code != 200 || !strings.Contains(w.Body.String(), "Updated") {
		t.Fatalf("owner update: %d %s", w.Code, w.Body)
	}
	if w := chatCommandsRequest(s, tokens[0], "DELETE", "workspace-commands/"+created.ID, ""); w.Code != 204 {
		t.Fatalf("owner delete: %d %s", w.Code, w.Body)
	}
}
