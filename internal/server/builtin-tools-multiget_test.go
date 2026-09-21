package server

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestMultiGetIDs(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want []string
	}{
		{name: "single string", args: map[string]any{"id": "a"}, want: []string{"a"}},
		{name: "array", args: map[string]any{"id": []any{"a", "b"}}, want: []string{"a", "b"}},
		{name: "typed array", args: map[string]any{"id": []string{"a", "b"}}, want: []string{"a", "b"}},
		{name: "comma separated", args: map[string]any{"id": "a, b ,c"}, want: []string{"a", "b", "c"}},
		{name: "newline separated", args: map[string]any{"id": "a\nb\r\nc"}, want: []string{"a", "b", "c"}},
		{name: "plural alias", args: map[string]any{"ids": []any{"a", "b"}}, want: []string{"a", "b"}},
		{name: "objects carrying the key", args: map[string]any{"id": []any{map[string]any{"id": "a"}, map[string]any{"id": "b"}}}, want: []string{"a", "b"}},
		{name: "duplicates collapse in first-seen order", args: map[string]any{"id": []any{"b", "a", "b"}}, want: []string{"b", "a"}},
		{name: "blanks dropped", args: map[string]any{"id": []any{"a", "", "  ", "b"}}, want: []string{"a", "b"}},
		// Interior spaces survive: some of these arguments accept a name.
		{name: "interior spaces preserved", args: map[string]any{"id": "Main Channel"}, want: []string{"Main Channel"}},
		{name: "missing", args: map[string]any{}, want: nil},
		{name: "explicit null", args: map[string]any{"id": nil}, want: nil},
		{name: "wrong type", args: map[string]any{"id": 42}, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := multiGetIDs(tt.args, "id")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("multiGetIDs = %#v, want %#v", got, tt.want)
			}
		})
	}

	t.Run("non-id key uses its own plural", func(t *testing.T) {
		got := multiGetIDs(map[string]any{"keys": []any{"openai", "anthropic"}}, "key")
		if !reflect.DeepEqual(got, []string{"openai", "anthropic"}) {
			t.Fatalf("multiGetIDs = %#v", got)
		}
	})
}

// A single identifier must keep the historical response and the historical
// error byte-for-byte; only the multi-identifier call gets an envelope.
func TestMultiGetSingleIdentifierIsUnchanged(t *testing.T) {
	body, err := multiGet(context.Background(), map[string]any{"id": "a"}, "id", func(_ context.Context, id string) (string, error) {
		return `{"id":"` + id + `"}`, nil
	})
	if err != nil {
		t.Fatalf("multiGet: %v", err)
	}
	if body != `{"id":"a"}` {
		t.Fatalf("body = %q, want the fetcher output verbatim", body)
	}

	_, err = multiGet(context.Background(), map[string]any{"id": "missing"}, "id", func(context.Context, string) (string, error) {
		return "", fmt.Errorf("agent %q not found", "missing")
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want the fetcher error", err)
	}
}

func TestMultiGetEnvelope(t *testing.T) {
	body, err := multiGet(context.Background(), map[string]any{"id": []any{"a", "missing", "b"}}, "id", func(_ context.Context, id string) (string, error) {
		if id == "missing" {
			return "", fmt.Errorf("agent %q not found", id)
		}
		return `{"name":"` + id + `"}`, nil
	})
	if err != nil {
		t.Fatalf("one failing entry must not fail the call: %v", err)
	}

	var envelope struct {
		Requested int `json:"requested"`
		Found     int `json:"found"`
		Failed    int `json:"failed"`
		Results   []struct {
			ID    string          `json:"id"`
			OK    bool            `json:"ok"`
			Data  json.RawMessage `json:"data"`
			Error string          `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, body)
	}
	if envelope.Requested != 3 || envelope.Found != 2 || envelope.Failed != 1 {
		t.Fatalf("counts = %d/%d/%d, want 3/2/1", envelope.Requested, envelope.Found, envelope.Failed)
	}
	if len(envelope.Results) != 3 {
		t.Fatalf("results = %d entries", len(envelope.Results))
	}
	// Order is the requested order, so the model can pair results with its own list.
	if envelope.Results[0].ID != "a" || envelope.Results[1].ID != "missing" || envelope.Results[2].ID != "b" {
		t.Fatalf("results out of requested order: %s", body)
	}
	// The record is embedded as JSON, not as a re-encoded string the model
	// would have to parse a second time.
	var compact struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(envelope.Results[0].Data, &compact); err != nil {
		t.Fatalf("record must be embedded as JSON, not an escaped string: %v", err)
	}
	if !envelope.Results[0].OK || compact.Name != "a" {
		t.Fatalf("first entry = %+v", envelope.Results[0])
	}
	if envelope.Results[1].OK || !strings.Contains(envelope.Results[1].Error, "not found") {
		t.Fatalf("failing entry must carry its own error: %+v", envelope.Results[1])
	}
}

func TestMultiGetBounds(t *testing.T) {
	t.Run("missing identifier", func(t *testing.T) {
		_, err := multiGet(context.Background(), map[string]any{}, "id", func(context.Context, string) (string, error) {
			t.Fatal("fetcher must not run")
			return "", nil
		})
		if err == nil || !strings.Contains(err.Error(), "id is required") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("over the limit", func(t *testing.T) {
		ids := make([]any, multiGetLimit+1)
		for i := range ids {
			ids[i] = fmt.Sprintf("id-%d", i)
		}
		_, err := multiGet(context.Background(), map[string]any{"id": ids}, "id", func(context.Context, string) (string, error) {
			t.Fatal("fetcher must not run")
			return "", nil
		})
		if err == nil || !strings.Contains(err.Error(), "maximum 25") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("cancellation stops the walk", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		_, err := multiGet(ctx, map[string]any{"id": []any{"a", "b", "c"}}, "id", func(context.Context, string) (string, error) {
			calls++
			cancel()
			return "{}", nil
		})
		if err == nil {
			t.Fatal("want the context error")
		}
		if calls != 1 {
			t.Fatalf("fetcher ran %d times after cancellation", calls)
		}
	})
}

type listAgentStore struct {
	service.AgentStorer
	agents map[string]*service.Agent
}

func (s *listAgentStore) GetAgent(_ context.Context, id string) (*service.Agent, error) {
	return s.agents[id], nil
}

// The reported failure: a model asking for several agents at once sent an
// array into a single-string read and was told "id is required".
func TestAgentGetAcceptsSeveralIDs(t *testing.T) {
	srv := &Server{agentStore: &listAgentStore{agents: map[string]*service.Agent{
		"agent-1": {ID: "agent-1", Name: "first"},
		"agent-2": {ID: "agent-2", Name: "second"},
	}}}

	body, err := srv.execAgentGet(context.Background(), map[string]any{"id": []any{"agent-1", "agent-2"}})
	if err != nil {
		t.Fatalf("execAgentGet: %v", err)
	}
	for _, want := range []string{"agent-1", "agent-2", "first", "second"} {
		if !strings.Contains(body, want) {
			t.Fatalf("response missing %q:\n%s", want, body)
		}
	}

	single, err := srv.execAgentGet(context.Background(), map[string]any{"id": "agent-1"})
	if err != nil {
		t.Fatalf("execAgentGet single: %v", err)
	}
	if strings.Contains(single, "\"results\"") {
		t.Fatalf("a single ID must not be wrapped in an envelope:\n%s", single)
	}
}

// Every multi-capable get must declare the array form, and that declaration
// has to survive the restrictive Gemini schema subset — a stripped branch
// would leave the tool advertising something it no longer accepts.
func TestMultiIDSchemasSurviveProviderSanitization(t *testing.T) {
	multiGetTools := map[string]string{
		"agent_get": "id", "task_get": "id", "org_get": "id", "workflow_get": "id",
		"trigger_get": "id", "skill_get": "id", "mcp_server_get": "id", "mcp_set_get": "id",
		"provider_get": "key", "bot_get": "id", "variable_get": "id", "connection_get": "id",
		"node_config_get": "id", "guide_get": "id", "llm_observation_get": "id",
	}

	byName := make(map[string]builtinToolDef, len(builtinTools))
	for _, tool := range builtinTools {
		byName[tool.Name] = tool
	}

	for name, key := range multiGetTools {
		t.Run(name, func(t *testing.T) {
			tool, ok := byName[name]
			if !ok {
				t.Fatal("tool not registered")
			}
			for label, schema := range map[string]map[string]any{
				"raw":      tool.InputSchema,
				"denylist": service.SanitizeSchema(tool.InputSchema),
				"gemini":   service.SanitizeSchemaForGemini(tool.InputSchema),
			} {
				props, _ := schema["properties"].(map[string]any)
				prop, _ := props[key].(map[string]any)
				if prop == nil {
					t.Fatalf("%s: %q property dropped", label, key)
				}
				branches, _ := prop["anyOf"].([]any)
				if len(branches) != 2 {
					t.Fatalf("%s: anyOf = %#v, want string and array branches", label, prop["anyOf"])
				}
				if branches[0].(map[string]any)["type"] != "string" {
					t.Fatalf("%s: first branch = %#v", label, branches[0])
				}
				array := branches[1].(map[string]any)
				if array["type"] != "array" || array["items"].(map[string]any)["type"] != "string" {
					t.Fatalf("%s: second branch = %#v", label, array)
				}
			}
		})
	}
}
