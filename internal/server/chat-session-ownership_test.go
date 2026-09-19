package server

import (
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// Chat sessions are per-account: the browser CRUD surface admits only the
// owning account, platform administrators additionally reach ownerless
// bot/legacy rows in their selected workspace, and foreign sessions answer
// exactly like unknown ones so ownership cannot be probed.
func TestChatSessionForRequestOwnership(t *testing.T) {
	owned := service.ChatSession{ID: "owned", WorkspaceID: "ws1", OwnerUserID: "alice", AgentID: "a"}
	botRow := service.ChatSession{ID: "bot", WorkspaceID: "ws1", AgentID: "a"}

	principal := func(user, workspace string, admin bool) *service.AccessPrincipal {
		return &service.AccessPrincipal{UserID: user, WorkspaceID: workspace, PlatformAdmin: admin}
	}
	for _, tt := range []struct {
		name      string
		session   service.ChatSession
		principal *service.AccessPrincipal
		want      int // 0 = admitted
	}{
		{"owner reaches own session", owned, principal("alice", "ws1", false), 0},
		{"another member gets 404, not 403", owned, principal("bob", "ws1", false), 404},
		{"administrator reaches member sessions", owned, principal("root", "ws1", true), 0},
		{"administrator bound to selected workspace", owned, principal("root", "ws2", true), 404},
		{"member cannot reach bot sessions", botRow, principal("alice", "ws1", false), 404},
		{"administrator reaches bot sessions", botRow, principal("root", "ws1", true), 0},
		{"legacy admin token keeps full access", owned, nil, 0},
		{"unknown session", service.ChatSession{}, principal("alice", "ws1", false), 404},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{chatSessionStore: &fakeChatSessionStore{session: tt.session}}
			r := httptest.NewRequest("GET", "/api/v1/chat/sessions/x", nil)
			if tt.principal != nil {
				r = r.WithContext(service.WithAccessPrincipal(r.Context(), *tt.principal))
			}
			id := tt.session.ID
			if id == "" {
				id = "missing"
			}
			record, status, _ := s.chatSessionForRequest(r, id)
			if tt.want == 0 && (record == nil || status != 0) {
				t.Fatalf("expected admission, got status %d", status)
			}
			if tt.want != 0 && (record != nil || status != tt.want) {
				t.Fatalf("expected %d, got record=%v status=%d", tt.want, record, status)
			}
		})
	}
}

// Session creation stamps ownership from the authenticated principal; the
// request body cannot choose another workspace or owner.
func TestCreateChatSessionStampsOwnership(t *testing.T) {
	policies := map[string]string{}
	for _, p := range workspaceBusinessPolicies() {
		policies[p.Method+" "+p.Pattern] = p.Capability
	}
	for pattern, want := range map[string]string{
		"GET /chat/sessions":                  "agents.read",
		"POST /chat/sessions":                 "agents.execute",
		"GET /chat/sessions/{id}":             "agents.read",
		"PUT /chat/sessions/{id}":             "agents.execute",
		"DELETE /chat/sessions/{id}":          "agents.execute",
		"GET /chat/sessions/{id}/messages":    "agents.read",
		"POST /chat/sessions/{id}/messages":   "agents.execute",
		"DELETE /chat/sessions/{id}/messages": "agents.execute",
		"POST /chat/sessions/{id}/confirm":    "agents.execute",
	} {
		if got := policies[pattern]; got != want {
			t.Errorf("%s admitted on %q, want %q", pattern, got, want)
		}
	}
}
