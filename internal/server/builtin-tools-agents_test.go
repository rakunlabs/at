package server

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

type capturingBuiltinAgentStore struct {
	service.AgentStorer
	existing *service.Agent
	written  *service.Agent
	writes   int
	updateID string
}

func (s *capturingBuiltinAgentStore) GetAgent(context.Context, string) (*service.Agent, error) {
	// Return the actual stored pointer to detect mutations before validation.
	return s.existing, nil
}

func (s *capturingBuiltinAgentStore) CreateAgent(_ context.Context, agent service.Agent) (*service.Agent, error) {
	s.writes++
	s.written = &agent
	return &agent, nil
}

func (s *capturingBuiltinAgentStore) UpdateAgent(_ context.Context, id string, agent service.Agent) (*service.Agent, error) {
	s.updateID = id
	return s.CreateAgent(context.Background(), agent)
}

func builtinAgentFixture() *service.Agent {
	return &service.Agent{
		ID: "agent-1", Name: "original", CreatedBy: "owner",
		Config: service.AgentConfig{
			Provider: "provider", Model: "model", SystemPrompt: "prompt", Description: "description",
			ReasoningEffort: "high",
			Skills:          []service.SkillRef{{ID: "old-skill", Connections: map[string]string{"youtube": "old-skill-connection"}}},
			Connections:     map[string]string{"youtube": "old-default", "google": "old-google"},
			MCPSets:         []string{"mcp"}, BuiltinTools: []string{"agent_get"}, MaxIterations: 12, ToolTimeout: 30,
		},
	}
}

func TestBuiltinAgentBindingSchemas(t *testing.T) {
	for _, name := range []string{"agent_create", "agent_update"} {
		t.Run(name, func(t *testing.T) {
			index := slices.IndexFunc(builtinTools, func(tool builtinToolDef) bool { return tool.Name == name })
			if index < 0 {
				t.Fatal("tool not registered")
			}
			schema := builtinTools[index].InputSchema
			props := schema["properties"].(map[string]any)
			required := schema["required"].([]string)
			if slices.Contains(required, "skills") || slices.Contains(required, "connections") {
				t.Fatal("skills and connections must be optional")
			}
			skills := props["skills"].(map[string]any)
			if skills["type"] != "array" {
				t.Fatalf("skills type = %v", skills["type"])
			}
			branches := skills["items"].(map[string]any)["anyOf"].([]any)
			if len(branches) != 2 || branches[0].(map[string]any)["type"] != "string" {
				t.Fatalf("expected string and object branches, got %#v", branches)
			}
			object := branches[1].(map[string]any)
			if object["type"] != "object" || !reflect.DeepEqual(object["required"], []string{"id"}) {
				t.Fatalf("skill object must require only id: %#v", object)
			}
			fields := object["properties"].(map[string]any)
			if fields["id"].(map[string]any)["type"] != "string" {
				t.Fatal("skill id must be a string")
			}
			for _, binding := range []any{props["connections"], fields["connections"]} {
				m := binding.(map[string]any)
				if m["type"] != "object" || m["additionalProperties"].(map[string]any)["type"] != "string" {
					t.Fatalf("connections must be a string-valued map: %#v", m)
				}
			}
			for field, clear := range map[string]string{"skills": "[]", "connections": "{}"} {
				description := strings.ToLower(props[field].(map[string]any)["description"].(string))
				for _, phrase := range []string{"full replacement", "omission preserves", clear, "per-skill connections override agent defaults"} {
					if !strings.Contains(description, phrase) {
						t.Errorf("%s description missing %q", field, phrase)
					}
				}
			}
		})
	}
}

func TestBuiltinAgentBindings(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		skills      []service.SkillRef
		connections map[string]string
	}{
		{name: "omitted", input: `{}`},
		{name: "strings", input: `{"skills":["one","two"]}`, skills: []service.SkillRef{{ID: "one"}, {ID: "two"}}},
		{name: "objects", input: `{"skills":[{"id":"one"},{"id":"two","connections":{}}]}`, skills: []service.SkillRef{{ID: "one"}, {ID: "two"}}},
		{
			name: "mixed with overrides", input: `{"skills":["one",{"id":"two","connections":{"youtube":"skill-connection"}}],"connections":{"youtube":"default-connection"}}`,
			skills:      []service.SkillRef{{ID: "one"}, {ID: "two", Connections: map[string]string{"youtube": "skill-connection"}}},
			connections: map[string]string{"youtube": "default-connection"},
		},
		{name: "connections only", input: `{"connections":{"fal":"unverified-connection-id"}}`, connections: map[string]string{"fal": "unverified-connection-id"}},
		{name: "clear skills", input: `{"skills":[]}`},
		{name: "clear connections", input: `{"connections":{}}`},
		{name: "clear both", input: `{"skills":[],"connections":{}}`},
	}
	for _, operation := range []string{"create", "update"} {
		for _, tt := range tests {
			t.Run(operation+"/"+tt.name, func(t *testing.T) {
				var args map[string]any
				if err := json.Unmarshal([]byte(tt.input), &args); err != nil {
					t.Fatal(err)
				}
				store := &capturingBuiltinAgentStore{existing: builtinAgentFixture()}
				s := &Server{agentStore: store}
				want := service.Agent{Name: "new-agent"}
				var result string
				var err error
				if operation == "create" {
					args["name"] = want.Name
					result, err = s.execAgentCreate(context.Background(), args)
				} else {
					want = *builtinAgentFixture()
					args["id"] = want.ID
					result, err = s.execAgentUpdate(context.Background(), args)
				}
				if err != nil {
					t.Fatal(err)
				}
				if store.writes != 1 || store.written == nil {
					t.Fatalf("expected one write, got %d", store.writes)
				}
				if operation == "update" && store.updateID != want.ID {
					t.Errorf("update ID = %q, want %q", store.updateID, want.ID)
				}
				if _, supplied := args["skills"]; supplied {
					want.Config.Skills = tt.skills
				}
				if _, supplied := args["connections"]; supplied {
					want.Config.Connections = tt.connections
				}
				// Compare wire representations so nil and empty cleared collections are equivalent.
				wantJSON, _ := json.Marshal(want)
				gotJSON, _ := json.Marshal(store.written)
				if string(gotJSON) != string(wantJSON) {
					t.Errorf("written agent = %s, want %s", gotJSON, wantJSON)
				}
				var returned service.Agent
				if err := json.Unmarshal([]byte(result), &returned); err != nil {
					t.Fatalf("invalid result JSON: %v", err)
				}
				returnedJSON, _ := json.Marshal(returned)
				if string(returnedJSON) != string(gotJSON) {
					t.Errorf("result differs from stored agent: %s", result)
				}
			})
		}
	}
}

func TestBuiltinAgentBindingsRejectInvalidWithoutMutation(t *testing.T) {
	tests := []struct{ name, input string }{
		{"null skills", `{"skills":null}`},
		{"skills not array", `{"skills":"one"}`},
		{"empty skill", `{"skills":[""]}`},
		{"whitespace skill", `{"skills":[" \t "]}`},
		{"empty object id", `{"skills":[{"id":""}]}`},
		{"whitespace object id", `{"skills":[{"id":" \n"}]}`},
		{"missing id", `{"skills":[{}]}`},
		{"null id", `{"skills":[{"id":null}]}`},
		{"numeric id", `{"skills":[{"id":1}]}`},
		{"null entry", `{"skills":[null]}`},
		{"numeric entry", `{"skills":[1]}`},
		{"boolean entry", `{"skills":[true]}`},
		{"array entry", `{"skills":[[]]}`},
		{"invalid after valid", `{"skills":["valid",{"id":""}],"connections":{"fal":"valid"}}`},
	}
	for _, binding := range []struct{ name, value string }{
		{"null", `null`},
		{"array", `[]`},
		{"string", `"connection"`},
		{"number", `42`},
		{"numeric value", `{"fal":1}`},
		{"boolean value", `{"fal":false}`},
		{"null value", `{"fal":null}`},
		{"object value", `{"fal":{}}`},
		{"array value", `{"fal":[]}`},
		{"empty slug", `{"":"connection"}`},
		{"whitespace slug", `{" \t":"connection"}`},
		{"empty connection id", `{"fal":""}`},
		{"whitespace connection id", `{"fal":" \n"}`},
	} {
		tests = append(tests,
			struct{ name, input string }{"agent connections " + binding.name, `{"skills":["valid"],"connections":` + binding.value + `}`},
			struct{ name, input string }{"skill connections " + binding.name, `{"skills":[{"id":"valid","connections":` + binding.value + `}],"connections":{"fal":"valid"}}`},
		)
	}
	for _, operation := range []string{"create", "update"} {
		for _, tt := range tests {
			t.Run(operation+"/"+tt.name, func(t *testing.T) {
				var args map[string]any
				if err := json.Unmarshal([]byte(tt.input), &args); err != nil {
					t.Fatal(err)
				}
				args["name"] = "changed"
				args["description"] = "changed"
				args["id"] = "agent-1"
				store := &capturingBuiltinAgentStore{existing: builtinAgentFixture()}
				before, _ := json.Marshal(store.existing)
				s := &Server{agentStore: store}
				var err error
				if operation == "create" {
					_, err = s.execAgentCreate(context.Background(), args)
				} else {
					_, err = s.execAgentUpdate(context.Background(), args)
				}
				if err == nil {
					t.Error("expected validation error")
				}
				if store.writes != 0 {
					t.Errorf("invalid input caused %d writes", store.writes)
				}
				after, _ := json.Marshal(store.existing)
				if string(after) != string(before) {
					t.Errorf("invalid input mutated existing agent: before %s, after %s", before, after)
				}
			})
		}
	}
}
