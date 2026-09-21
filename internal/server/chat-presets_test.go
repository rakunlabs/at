package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func chatPresetsRequest(s *Server, token, method, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/at/api/v1/chats/presets", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("X-AT-Workspace-ID", "legacy-default")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.server.ServeHTTP(w, r)

	return w
}

func decodeChatPresets(t *testing.T, w *httptest.ResponseRecorder) []service.ChatPreset {
	t.Helper()
	var got chatPresetRegistry
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}

	return got.Presets
}

// Named presets are per account, and their identity is the server's to assign:
// a client submits the whole list, so it must not be able to claim another
// entry's id, backdate a creation time or read another account's setups.
func TestChatPresetsOwnerScopeAndIdentity(t *testing.T) {
	s, _, tokens := playgroundFixture(t)

	if w := chatPresetsRequest(s, "", "GET", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d %s", w.Code, w.Body)
	}

	// An account that never saved one reads `[]`, not `null` and not a 404 —
	// the page iterates the list without a nil check.
	w := chatPresetsRequest(s, tokens[0], "GET", "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"presets":[]}` {
		t.Fatalf("empty list: %d %s", w.Code, w.Body)
	}

	saved := `{"presets":[
		{"id":"","name":"Research","model":"test/text-model","skills":["web"],"frontend_tools":[]},
		{"id":"","name":"Coding","model":"test/text-model","agent_id":"agent-1","builtin_tools":["todo_write"]}
	]}`
	w = chatPresetsRequest(s, tokens[0], "PUT", saved)
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	presets := decodeChatPresets(t, w)
	if len(presets) != 2 {
		t.Fatalf("saved %d presets: %s", len(presets), w.Body)
	}
	if presets[0].ID == "" || presets[1].ID == "" || presets[0].ID == presets[1].ID {
		t.Fatalf("ids were not minted distinctly: %+v", presets)
	}
	if presets[0].CreatedAt == "" || presets[0].UpdatedAt == "" {
		t.Fatalf("timestamps were not stamped: %+v", presets[0])
	}

	// An explicit "no browser tools" must survive storage; an absent field
	// instead opts into the UI's shipped defaults. This is the one selection
	// where nil and empty mean different things.
	var raw struct {
		Presets []map[string]json.RawMessage `json:"presets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.Presets[0]["frontend_tools"]) != "[]" {
		t.Fatalf("explicit empty selection was lost: %s", w.Body)
	}
	if _, present := raw.Presets[1]["frontend_tools"]; present {
		t.Fatalf("absent selection became explicit: %s", w.Body)
	}

	// Editing one entry keeps its id and creation time and refreshes only the
	// update time; a delete is the same call with the entry left out.
	kept := presets[0]
	edited := `{"presets":[{"id":"` + kept.ID + `","name":"Research v2","model":"test/text-model"}]}`
	w = chatPresetsRequest(s, tokens[0], "PUT", edited)
	if w.Code != 200 {
		t.Fatalf("edit: %d %s", w.Code, w.Body)
	}
	presets = decodeChatPresets(t, w)
	if len(presets) != 1 || presets[0].ID != kept.ID || presets[0].Name != "Research v2" {
		t.Fatalf("edit did not preserve identity: %+v", presets)
	}
	if presets[0].CreatedAt != kept.CreatedAt {
		t.Fatalf("creation time was rewritten: %q want %q", presets[0].CreatedAt, kept.CreatedAt)
	}

	// A submitted id that belongs to nothing stored is not adopted: it is a
	// new entry with a server-minted id, so one account cannot pin an id it
	// observed elsewhere.
	w = chatPresetsRequest(s, tokens[0], "PUT", `{"presets":[{"id":"forged-id","name":"Forged"}]}`)
	if w.Code != 200 {
		t.Fatalf("forged id: %d %s", w.Code, w.Body)
	}
	if presets = decodeChatPresets(t, w); len(presets) != 1 || presets[0].ID == "forged-id" {
		t.Fatalf("client id was adopted: %+v", presets)
	}

	// A second administrator has their own list, not this one.
	if w = chatPresetsRequest(s, tokens[1], "GET", ""); strings.TrimSpace(w.Body.String()) != `{"presets":[]}` {
		t.Fatalf("presets leaked across accounts: %d %s", w.Code, w.Body)
	}
}

func TestChatPresetsValidation(t *testing.T) {
	s, _, tokens := playgroundFixture(t)

	for name, body := range map[string]string{
		"unknown field":  `{"presets":[{"name":"A"}],"nope":1}`,
		"missing name":   `{"presets":[{"name":"   "}]}`,
		"duplicate name": `{"presets":[{"name":"Ops"},{"name":"ops"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if w := chatPresetsRequest(s, tokens[0], "PUT", body); w.Code != 400 {
				t.Fatalf("accepted %s: %d %s", name, w.Code, w.Body)
			}
		})
	}

	// A rejected write leaves the stored list untouched.
	if w := chatPresetsRequest(s, tokens[0], "GET", ""); strings.TrimSpace(w.Body.String()) != `{"presets":[]}` {
		t.Fatalf("rejected write was persisted: %s", w.Body)
	}
}

// A preset is a default with a name. If the two payloads could drift, a setup
// saved through one surface would come back incomplete through the other.
func TestChatPresetSharesDefaultsPayload(t *testing.T) {
	var defaults playgroundDefaults
	if _, ok := any(defaults).(service.ChatWorkbenchSetup); !ok {
		t.Fatal("playgroundDefaults no longer shares the preset selection payload")
	}

	setup := service.ChatWorkbenchSetup{Model: "p/m", AgentID: "a", SystemPrompt: "s", Skills: []string{"x"}}
	fromSetup, err := json.Marshal(setup)
	if err != nil {
		t.Fatal(err)
	}
	fromPreset, err := json.Marshal(service.ChatPreset{ID: "id", Name: "n", ChatWorkbenchSetup: setup})
	if err != nil {
		t.Fatal(err)
	}
	// The preset adds only identity fields: every selection key must appear
	// with the same name and value.
	var flat map[string]json.RawMessage
	if err := json.Unmarshal(fromPreset, &flat); err != nil {
		t.Fatal(err)
	}
	var selections map[string]json.RawMessage
	if err := json.Unmarshal(fromSetup, &selections); err != nil {
		t.Fatal(err)
	}
	for key, want := range selections {
		if string(flat[key]) != string(want) {
			t.Fatalf("preset key %q is %s, defaults say %s", key, flat[key], want)
		}
	}
}
