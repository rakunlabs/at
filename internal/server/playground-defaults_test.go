package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func playgroundDefaultsRequest(s *Server, token, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/chats/defaults", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-AT-Workspace-ID", "legacy-default")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)
	return w
}

func TestPlaygroundFrontendToolsDefaultsRoundTrip(t *testing.T) {
	for _, input := range []string{`{}`, `{"frontend_tools":[]}`, `{"frontend_tools":["question"]}`} {
		t.Run(input, func(t *testing.T) {
			var prefs playgroundDefaults
			if err := json.Unmarshal([]byte(input), &prefs); err != nil {
				t.Fatal(err)
			}
			stored, err := json.Marshal(prefs)
			if err != nil {
				t.Fatal(err)
			}
			if string(stored) != input {
				t.Fatalf("default vs explicit tool choice lost: got %s, want %s", stored, input)
			}
		})
	}
}

// The saved workbench preset is per account: it is keyed on the authenticated
// subject, never on a user ID in the request, so one account's setup cannot be
// read or overwritten through another's session. An account that never saved
// one reads the empty preset rather than a 404, because the page always asks.
func TestPlaygroundDefaultsOwnerScope(t *testing.T) {
	s, _, tokens := playgroundFixture(t)

	if w := playgroundDefaultsRequest(s, "", "GET", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body)
	}

	w := playgroundDefaultsRequest(s, tokens[0], "GET", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("empty preset: %d %s", w.Code, w.Body)
	}

	saved := `{"model":"test/text-model","agent_id":"agent-1","mcp_sets":["ops"],"builtin_tools":["todo_write"]}`
	if w = playgroundDefaultsRequest(s, tokens[0], "PUT", saved); w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}

	w = playgroundDefaultsRequest(s, tokens[0], "GET", "")
	var got playgroundDefaults
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "test/text-model" || got.AgentID != "agent-1" || len(got.MCPSets) != 1 || got.MCPSets[0] != "ops" {
		t.Fatalf("round trip: %+v", got)
	}

	// A second administrator has their own preset, not this one.
	w = playgroundDefaultsRequest(s, tokens[1], "GET", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "{}" {
		t.Fatalf("preset leaked across accounts: %d %s", w.Code, w.Body)
	}
	if w = playgroundDefaultsRequest(s, tokens[1], "PUT", `{"model":"other/model"}`); w.Code != 200 {
		t.Fatalf("second save: %d %s", w.Code, w.Body)
	}
	w = playgroundDefaultsRequest(s, tokens[0], "GET", "")
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Model != "test/text-model" {
		t.Fatalf("second account overwrote the first: %+v", got)
	}

	// Explicitly disabling all browser tools must survive storage; an absent
	// field instead opts into the UI's shipped defaults.
	if w = playgroundDefaultsRequest(s, tokens[0], "PUT", `{"frontend_tools":[]}`); w.Code != 200 {
		t.Fatalf("disable chat tools: %d %s", w.Code, w.Body)
	}
	w = playgroundDefaultsRequest(s, tokens[0], "GET", "")
	var disabled map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &disabled); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || string(disabled["frontend_tools"]) != "[]" {
		t.Fatalf("empty selection was lost: %d %s", w.Code, w.Body)
	}

	// Unknown fields are refused like the rest of the Playground surface.
	if w = playgroundDefaultsRequest(s, tokens[0], "PUT", `{"nope":1}`); w.Code != 400 {
		t.Fatalf("unknown field: %d %s", w.Code, w.Body)
	}
}

// The Playground's tool plane must be reachable by an ordinary member, and the
// catalogs it needs to offer a choice must be readable — otherwise the page
// renders tool pickers whose every request answers 403. Management of those
// same resources stays installation administration.
func TestPlaygroundToolPlaneAdmission(t *testing.T) {
	policies := map[string]string{}
	for _, p := range workspaceBusinessPolicies() {
		policies[p.Method+" "+p.Pattern] = p.Capability
	}
	for pattern, want := range map[string]string{
		"GET /mcp/builtin-tools":          "models.use",
		"POST /mcp/call-builtin-tool":     "models.use",
		"POST /mcp/call-skill-tool":       "models.use",
		"GET /mcp/set-tools/{name}":       "mcp.use",
		"POST /mcp/set-tools/{name}/call": "mcp.use",
		"GET /skills":                     "skills.read",
		"GET /mcp/sets":                   "mcp.read",
		"GET /connections":                "connections.read",
		"GET /chats/defaults":             "models.use",
		"PUT /chats/defaults":             "models.use",
	} {
		if got := policies[pattern]; got != want {
			t.Errorf("%s admitted on %q, want %q", pattern, got, want)
		}
	}
	// Managing skills and connections is not opened by the read policies above.
	// MCP Sets deliberately use their workspace mcp.write capability.
	for _, pattern := range []string{"POST /skills", "POST /connections", "PUT /skills/{id}"} {
		if got, ok := policies[pattern]; ok {
			t.Errorf("%s became capability-admitted on %q; management must stay installation administration", pattern, got)
		}
	}
	for pattern, want := range map[string]string{
		"POST /mcp/sets":                        "mcp.write",
		"PUT /mcp/sets/{id}":                    "mcp.write",
		"DELETE /mcp/sets/{id}":                 "mcp.write",
		"GET /mcp/sets/{id}":                    "mcp.read",
		"GET /mcp/sets/{id}/export":             "mcp.read",
		"POST /mcp/sets/{id}/inspect-upstreams": "mcp.read",
		"POST /mcp-templates/{slug}/install":    "mcp.write",
	} {
		if got := policies[pattern]; got != want {
			t.Errorf("%s admitted on %q, want %q", pattern, got, want)
		}
	}
}
