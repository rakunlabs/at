package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// fakeSkillStore is a minimal in-memory implementation of service.SkillStorer
// used to verify that the new skill_* / mcp_*_* dispatch routes call into
// the store the way the HTTP handlers do. We don't try to test the store
// itself — that's covered by internal/store/postgres — only the executor
// glue around it.
type fakeSkillStore struct {
	skills  map[string]*service.Skill
	created []service.Skill
	updated []service.Skill
	deleted []string
}

func newFakeSkillStore() *fakeSkillStore {
	return &fakeSkillStore{skills: map[string]*service.Skill{}}
}

func (f *fakeSkillStore) ListSkills(_ context.Context, _ *query.Query) (*service.ListResult[service.Skill], error) {
	out := make([]service.Skill, 0, len(f.skills))
	for _, sk := range f.skills {
		out = append(out, *sk)
	}
	return &service.ListResult[service.Skill]{Data: out, Meta: service.ListMeta{Total: uint64(len(out))}}, nil
}

func (f *fakeSkillStore) GetSkill(_ context.Context, id string) (*service.Skill, error) {
	if sk, ok := f.skills[id]; ok {
		return sk, nil
	}
	return nil, nil
}

func (f *fakeSkillStore) GetSkillByName(_ context.Context, name string) (*service.Skill, error) {
	for _, sk := range f.skills {
		if sk.Name == name {
			return sk, nil
		}
	}
	return nil, nil
}

func (f *fakeSkillStore) CreateSkill(_ context.Context, sk service.Skill) (*service.Skill, error) {
	f.created = append(f.created, sk)
	if sk.ID == "" {
		sk.ID = "skill-" + sk.Name
	}
	stored := sk
	f.skills[sk.ID] = &stored
	return &stored, nil
}

func (f *fakeSkillStore) UpdateSkill(_ context.Context, id string, sk service.Skill) (*service.Skill, error) {
	if _, ok := f.skills[id]; !ok {
		return nil, nil
	}
	sk.ID = id
	f.updated = append(f.updated, sk)
	stored := sk
	f.skills[id] = &stored
	return &stored, nil
}

func (f *fakeSkillStore) DeleteSkill(_ context.Context, id string) error {
	delete(f.skills, id)
	f.deleted = append(f.deleted, id)
	return nil
}

// TestDispatch_SkillCRUD confirms the new skill_* tools route through
// dispatchBuiltinTool to the SkillStorer. We hit create → get → update
// → delete and verify the store sees each call.
func TestDispatch_SkillCRUD(t *testing.T) {
	store := newFakeSkillStore()
	s := &Server{skillStore: store}
	ctx := context.Background()

	// skill_create
	out, err := s.dispatchBuiltinTool(ctx, "skill_create", map[string]any{
		"name":          "weather",
		"description":   "Get the weather",
		"system_prompt": "You can fetch weather.",
		"tools": []any{
			map[string]any{
				"name":         "get_weather",
				"description":  "Get current weather for a city",
				"input_schema": map[string]any{"type": "object"},
				"handler":      "return 'sunny';",
				"handler_type": "js",
			},
		},
	})
	if err != nil {
		t.Fatalf("skill_create error: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created skill, got %d", len(store.created))
	}
	created := store.created[0]
	if created.Name != "weather" {
		t.Errorf("name = %q, want %q", created.Name, "weather")
	}
	if len(created.Tools) != 1 || created.Tools[0].Name != "get_weather" {
		t.Errorf("tools not decoded correctly: %+v", created.Tools)
	}
	if created.Tools[0].Handler != "return 'sunny';" {
		t.Errorf("handler not preserved: %q", created.Tools[0].Handler)
	}
	if created.Tools[0].InputSchema == nil {
		t.Error("input_schema should be decoded into InputSchema")
	}

	// Pull the ID out of the result so subsequent calls reference the same record.
	var createdRecord service.Skill
	if err := json.Unmarshal([]byte(out), &createdRecord); err != nil {
		t.Fatalf("unmarshal create result: %v", err)
	}
	id := createdRecord.ID
	if id == "" {
		t.Fatal("created skill has no ID")
	}

	// skill_get
	getOut, err := s.dispatchBuiltinTool(ctx, "skill_get", map[string]any{"id": id})
	if err != nil {
		t.Fatalf("skill_get error: %v", err)
	}
	if !strings.Contains(getOut, "weather") {
		t.Errorf("skill_get result missing name: %s", getOut)
	}

	// skill_update — full replacement.
	if _, err := s.dispatchBuiltinTool(ctx, "skill_update", map[string]any{
		"id":            id,
		"name":          "weather",
		"description":   "Updated description",
		"system_prompt": "Updated prompt.",
		"tools":         []any{},
	}); err != nil {
		t.Fatalf("skill_update error: %v", err)
	}
	if len(store.updated) != 1 {
		t.Fatalf("expected 1 update, got %d", len(store.updated))
	}
	if store.updated[0].Description != "Updated description" {
		t.Errorf("description not updated: %q", store.updated[0].Description)
	}

	// skill_delete
	if _, err := s.dispatchBuiltinTool(ctx, "skill_delete", map[string]any{"id": id}); err != nil {
		t.Fatalf("skill_delete error: %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != id {
		t.Errorf("delete didn't reach store: %v", store.deleted)
	}
}

// TestDispatch_SkillCreate_RequiresName verifies the schema-level "name
// required" guard fires before we hit the store.
func TestDispatch_SkillCreate_RequiresName(t *testing.T) {
	s := &Server{skillStore: newFakeSkillStore()}
	if _, err := s.dispatchBuiltinTool(context.Background(), "skill_create", map[string]any{}); err == nil {
		t.Fatal("expected error when name is missing")
	}
}

// TestDecodeSkillTools_AcceptsBothSchemaKeys ensures that LLMs which emit
// `inputSchema` (the canonical service.Tool JSON tag) and LLMs which emit
// `input_schema` (what the at-management tool docs publish) both work.
func TestDecodeSkillTools_AcceptsBothSchemaKeys(t *testing.T) {
	cases := []struct{ name, key string }{
		{"snake_case", "input_schema"},
		{"camelCase", "inputSchema"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tools, err := decodeSkillTools([]any{
				map[string]any{
					"name":        "t",
					"description": "d",
					tc.key:        map[string]any{"type": "object"},
				},
			})
			if err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if len(tools) != 1 {
				t.Fatalf("expected 1 tool, got %d", len(tools))
			}
			if tools[0].InputSchema == nil {
				t.Errorf("InputSchema should be populated for key %q", tc.key)
			}
		})
	}
}

// fakeMCPServerStore tracks calls to verify mcp_server_* dispatch.
type fakeMCPServerStore struct {
	servers map[string]*service.MCPServer
	created []service.MCPServer
	updated []service.MCPServer
	deleted []string
}

func newFakeMCPServerStore() *fakeMCPServerStore {
	return &fakeMCPServerStore{servers: map[string]*service.MCPServer{}}
}

func (f *fakeMCPServerStore) ListMCPServers(_ context.Context, _ *query.Query) (*service.ListResult[service.MCPServer], error) {
	out := make([]service.MCPServer, 0, len(f.servers))
	for _, s := range f.servers {
		out = append(out, *s)
	}
	return &service.ListResult[service.MCPServer]{Data: out}, nil
}

func (f *fakeMCPServerStore) GetMCPServer(_ context.Context, id string) (*service.MCPServer, error) {
	if s, ok := f.servers[id]; ok {
		return s, nil
	}
	return nil, nil
}

func (f *fakeMCPServerStore) GetMCPServerByName(_ context.Context, name string) (*service.MCPServer, error) {
	for _, s := range f.servers {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, nil
}

func (f *fakeMCPServerStore) CreateMCPServer(_ context.Context, srv service.MCPServer) (*service.MCPServer, error) {
	f.created = append(f.created, srv)
	if srv.ID == "" {
		srv.ID = "mcp-" + srv.Name
	}
	stored := srv
	f.servers[srv.ID] = &stored
	return &stored, nil
}

func (f *fakeMCPServerStore) UpdateMCPServer(_ context.Context, id string, srv service.MCPServer) (*service.MCPServer, error) {
	if _, ok := f.servers[id]; !ok {
		return nil, nil
	}
	srv.ID = id
	f.updated = append(f.updated, srv)
	stored := srv
	f.servers[id] = &stored
	return &stored, nil
}

func (f *fakeMCPServerStore) DeleteMCPServer(_ context.Context, id string) error {
	delete(f.servers, id)
	f.deleted = append(f.deleted, id)
	return nil
}

// TestDispatch_MCPServerCreate_DecodesNestedConfig confirms the embedded
// MCPServerConfig — which carries arrays of upstream MCPs, HTTP tools,
// etc. — round-trips through the JSON dance in decodeMCPServerConfig.
// This is the contract that lets agents wire up a new MCP backend in
// one call instead of going through the HTTP API directly.
func TestDispatch_MCPServerCreate_DecodesNestedConfig(t *testing.T) {
	store := newFakeMCPServerStore()
	s := &Server{mcpServerStore: store}

	_, err := s.dispatchBuiltinTool(context.Background(), "mcp_server_create", map[string]any{
		"name":        "my-mcp",
		"description": "Test",
		"config": map[string]any{
			"description":           "An MCP that exposes a single HTTP tool",
			"enabled_builtin_tools": []any{"http_request"},
			"http_tools": []any{
				map[string]any{
					"name":         "ping",
					"description":  "Ping a host",
					"method":       "GET",
					"url":          "https://example.test/ping",
					"input_schema": map[string]any{"type": "object"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("mcp_server_create error: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created mcp server, got %d", len(store.created))
	}
	got := store.created[0]
	if got.Name != "my-mcp" {
		t.Errorf("name = %q, want %q", got.Name, "my-mcp")
	}
	if len(got.Config.EnabledBuiltinTools) != 1 || got.Config.EnabledBuiltinTools[0] != "http_request" {
		t.Errorf("enabled_builtin_tools not decoded: %+v", got.Config.EnabledBuiltinTools)
	}
	if len(got.Config.HTTPTools) != 1 || got.Config.HTTPTools[0].Name != "ping" {
		t.Errorf("http_tools not decoded: %+v", got.Config.HTTPTools)
	}
}

// fakeMCPSetStore mirrors fakeMCPServerStore for the MCPSet half of the API.
type fakeMCPSetStore struct {
	sets    map[string]*service.MCPSet
	created []service.MCPSet
	updated []service.MCPSet
	deleted []string
}

func newFakeMCPSetStore() *fakeMCPSetStore {
	return &fakeMCPSetStore{sets: map[string]*service.MCPSet{}}
}

func (f *fakeMCPSetStore) ListMCPSets(_ context.Context, _ *query.Query) (*service.ListResult[service.MCPSet], error) {
	out := make([]service.MCPSet, 0, len(f.sets))
	for _, s := range f.sets {
		out = append(out, *s)
	}
	return &service.ListResult[service.MCPSet]{Data: out}, nil
}

func (f *fakeMCPSetStore) GetMCPSet(_ context.Context, id string) (*service.MCPSet, error) {
	if s, ok := f.sets[id]; ok {
		return s, nil
	}
	return nil, nil
}

func (f *fakeMCPSetStore) GetMCPSetByName(_ context.Context, name string) (*service.MCPSet, error) {
	for _, s := range f.sets {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, nil
}

func (f *fakeMCPSetStore) CreateMCPSet(_ context.Context, set service.MCPSet) (*service.MCPSet, error) {
	f.created = append(f.created, set)
	if set.ID == "" {
		set.ID = "set-" + set.Name
	}
	stored := set
	f.sets[set.ID] = &stored
	return &stored, nil
}

func (f *fakeMCPSetStore) UpdateMCPSet(_ context.Context, id string, set service.MCPSet) (*service.MCPSet, error) {
	if _, ok := f.sets[id]; !ok {
		return nil, nil
	}
	set.ID = id
	f.updated = append(f.updated, set)
	stored := set
	f.sets[id] = &stored
	return &stored, nil
}

func (f *fakeMCPSetStore) DeleteMCPSet(_ context.Context, id string) error {
	delete(f.sets, id)
	f.deleted = append(f.deleted, id)
	return nil
}

// TestDispatch_MCPSetCreate_NormalizesEmptyArrays mirrors the HTTP
// handler's behaviour: when callers omit `servers` / `urls`, we
// persist empty arrays rather than nil so downstream consumers get
// a stable shape.
func TestDispatch_MCPSetCreate_NormalizesEmptyArrays(t *testing.T) {
	store := newFakeMCPSetStore()
	s := &Server{mcpSetStore: store}

	if _, err := s.dispatchBuiltinTool(context.Background(), "mcp_set_create", map[string]any{
		"name":        "scratch-set",
		"description": "Test",
		"config": map[string]any{
			"enabled_builtin_tools": []any{"task_create"},
		},
	}); err != nil {
		t.Fatalf("mcp_set_create error: %v", err)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created mcp set, got %d", len(store.created))
	}
	got := store.created[0]
	if got.Servers == nil {
		t.Error("Servers should be []string{}, not nil")
	}
	if got.URLs == nil {
		t.Error("URLs should be []string{}, not nil")
	}
}

// TestAtManagementTemplate_HasNewTools is a regression guard: every tool
// we just added to builtin-tools.go must also be listed in the
// at-management template, otherwise the canonical management MCP that
// gets installed in fresh deployments won't expose them.
func TestAtManagementTemplate_HasNewTools(t *testing.T) {
	data, err := mcpTemplateFS.ReadFile("mcp_templates/at-management.json")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	var tmpl MCPTemplate
	if err := json.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("parse template: %v", err)
	}

	expected := []string{
		// Phase 1: Skills authoring
		"skill_get", "skill_create", "skill_update", "skill_delete",
		"skill_test_handler", "skill_export", "skill_import",
		"skill_import_url", "skill_import_skillmd",
		// Phase 1: MCP server / set CRUD
		"mcp_server_list", "mcp_server_get", "mcp_server_create",
		"mcp_server_update", "mcp_server_delete",
		"mcp_set_list", "mcp_set_get", "mcp_set_create",
		"mcp_set_update", "mcp_set_delete",
		// Phase 1: Triggers and bots that were already implemented but missing from the template
		"trigger_create", "trigger_get", "trigger_update", "trigger_delete",
		"bot_list", "bot_get", "bot_update",
		// Phase 2: Bot lifecycle
		"bot_create", "bot_delete", "bot_start", "bot_stop", "bot_status",
		// Phase 2: Provider write
		"provider_create", "provider_update", "provider_delete", "provider_discover_models",
		// Phase 2: API tokens
		"apitoken_list", "apitoken_create", "apitoken_update", "apitoken_delete",
		"apitoken_get_usage", "apitoken_reset_usage",
		// Phase 2: Variables / Connections / Node configs / Guides
		"variable_list", "variable_get", "variable_create", "variable_update", "variable_delete",
		"connection_list", "connection_get", "connection_create", "connection_update",
		"connection_delete", "connection_import_from_variables",
		"node_config_list", "node_config_get", "node_config_create",
		"node_config_update", "node_config_delete",
		"guide_list", "guide_get", "guide_create", "guide_update", "guide_delete",
		// Phase 2: Destructive / lifecycle
		"agent_delete",
		"org_update", "org_delete", "org_list_agents", "org_update_agent", "org_remove_agent",
		"task_wait", "task_delete", "task_cancel", "active_delegation_list",
		// LLM traces and observations
		"llm_trace_list", "llm_trace_get", "llm_observation_get",
	}

	enabled := map[string]bool{}
	for _, name := range tmpl.MCPServer.Config.EnabledBuiltinTools {
		enabled[name] = true
	}

	for _, name := range expected {
		if !enabled[name] {
			t.Errorf("at-management template is missing builtin tool %q", name)
		}
	}
}

func TestUVXMCPTemplates_HaveWritableRuntimeDirs(t *testing.T) {
	files := []string{"elevenlabs.json", "minimax.json", "minimax-search.json"}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := mcpTemplateFS.ReadFile("mcp_templates/" + file)
			if err != nil {
				t.Fatalf("read template: %v", err)
			}
			var tmpl MCPTemplate
			if err := json.Unmarshal(data, &tmpl); err != nil {
				t.Fatalf("parse template: %v", err)
			}
			if len(tmpl.MCPServer.Config.MCPUpstreams) != 1 {
				t.Fatalf("upstream count = %d, want 1", len(tmpl.MCPServer.Config.MCPUpstreams))
			}
			env := tmpl.MCPServer.Config.MCPUpstreams[0].Env
			for _, key := range []string{"UV_CACHE_DIR", "UV_TOOL_DIR"} {
				if env[key] == "" {
					t.Errorf("template is missing %s", key)
				}
			}
		})
	}
}

// TestDispatch_NewToolsHaveDefinitions guards against the "tool listed in
// dispatch but missing from builtinTools schema" footgun. Every name the
// dispatcher routes for the new domains must appear in builtinTools so
// the MCP ListTools call advertises it.
func TestDispatch_NewToolsHaveDefinitions(t *testing.T) {
	wanted := []string{
		// Phase 1
		"skill_get", "skill_create", "skill_update", "skill_delete",
		"skill_test_handler", "skill_export", "skill_import",
		"skill_import_url", "skill_import_skillmd",
		"mcp_server_list", "mcp_server_get", "mcp_server_create",
		"mcp_server_update", "mcp_server_delete",
		"mcp_set_list", "mcp_set_get", "mcp_set_create",
		"mcp_set_update", "mcp_set_delete",
		// Phase 2
		"bot_create", "bot_delete", "bot_start", "bot_stop", "bot_status",
		"provider_create", "provider_update", "provider_delete", "provider_discover_models",
		"apitoken_list", "apitoken_create", "apitoken_update", "apitoken_delete",
		"apitoken_get_usage", "apitoken_reset_usage",
		"variable_list", "variable_get", "variable_create", "variable_update", "variable_delete",
		"connection_list", "connection_get", "connection_create", "connection_update",
		"connection_delete", "connection_import_from_variables",
		"node_config_list", "node_config_get", "node_config_create",
		"node_config_update", "node_config_delete",
		"guide_list", "guide_get", "guide_create", "guide_update", "guide_delete",
		"agent_delete", "org_update", "org_delete", "org_list_agents",
		"org_update_agent", "org_remove_agent",
		"task_delete", "task_cancel", "active_delegation_list",
		"llm_trace_list", "llm_trace_get", "llm_observation_get",
	}
	defined := map[string]bool{}
	for _, def := range builtinTools {
		defined[def.Name] = true
	}
	for _, name := range wanted {
		if !defined[name] {
			t.Errorf("tool %q is dispatched but has no schema in builtinTools", name)
		}
	}
}
