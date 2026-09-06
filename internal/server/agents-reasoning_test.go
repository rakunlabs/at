package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestAgentReasoningEffortAPI(t *testing.T) {
	for _, operation := range []string{"create", "update", "import", "preview"} {
		for _, effort := range []string{"", "low", "medium", "high", "xhigh", "HIGH", " high", "none"} {
			t.Run(operation+"/"+effort, func(t *testing.T) {
				store := &capturingBuiltinAgentStore{existing: builtinAgentFixture()}
				s := &Server{agentStore: store}
				agent := service.Agent{Name: "agent", Config: service.AgentConfig{Provider: "provider-key", Model: "original-model", ReasoningEffort: effort}}
				data, err := json.Marshal(agent)
				if err != nil {
					t.Fatal(err)
				}
				body := string(data)
				handler := s.CreateAgentAPI
				wantStatus := http.StatusCreated
				switch operation {
				case "update":
					handler, wantStatus = s.UpdateAgentAPI, http.StatusOK
				case "import", "preview":
					body = "---\nname: agent\nprovider: provider-key\nmodel: original-model\nreasoning_effort: '" + effort + "'\n---\nPrompt"
					handler = s.ImportAgentAPI
					if operation == "preview" {
						handler, wantStatus = s.PreviewImportAgentAPI, http.StatusOK
					}
				}
				valid := service.ValidateReasoningEffort(effort) == nil
				if !valid {
					wantStatus = http.StatusBadRequest
				}
				r := httptest.NewRequest(http.MethodPost, "/api/v1/agents", strings.NewReader(body))
				r.SetPathValue("id", "agent-1")
				w := httptest.NewRecorder()
				handler(w, r)
				if w.Code != wantStatus {
					t.Fatalf("status = %d, want %d: %s", w.Code, wantStatus, w.Body.String())
				}
				wantWrites := 0
				if valid && operation != "preview" {
					wantWrites = 1
				}
				if store.writes != wantWrites {
					t.Fatalf("writes = %d, want %d", store.writes, wantWrites)
				}
				if valid {
					var returned service.Agent
					if err := json.Unmarshal(w.Body.Bytes(), &returned); err != nil {
						t.Fatal(err)
					}
					if returned.Config.ReasoningEffort != effort || returned.Config.Model != agent.Config.Model {
						t.Fatalf("unexpected response config: %+v", returned.Config)
					}
				}
			})
		}
	}
}

func TestAgentReasoningEffortExports(t *testing.T) {
	agent := builtinAgentFixture()
	converted := mdToAgent(agentToMD(agent))
	if converted.Config.ReasoningEffort != agent.Config.ReasoningEffort || converted.Config.Model != agent.Config.Model {
		t.Fatalf("conversion lost effort or changed model: %+v", converted)
	}
	s := &Server{agentStore: &capturingBuiltinAgentStore{existing: agent}}
	for name, handler := range map[string]http.HandlerFunc{"get": s.GetAgentAPI, "json": s.ExportAgentJSONAPI, "markdown": s.ExportAgentAPI} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.SetPathValue("id", agent.ID)
			w := httptest.NewRecorder()
			handler(w, r)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "reasoning_effort") || !strings.Contains(w.Body.String(), "high") {
				t.Fatalf("export = %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBuiltinAgentReasoningEffort(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		for _, input := range []string{`{}`, `{"reasoning_effort":""}`, `{"reasoning_effort":"low"}`, `{"reasoning_effort":"medium"}`, `{"reasoning_effort":"high"}`, `{"reasoning_effort":"xhigh"}`, `{"reasoning_effort":"HIGH"}`, `{"reasoning_effort":" high"}`, `{"reasoning_effort":"none"}`, `{"reasoning_effort":null}`, `{"reasoning_effort":1}`, `{"reasoning_effort":true}`, `{"reasoning_effort":[]}`, `{"reasoning_effort":{}}`} {
			t.Run(operation+"/"+input, func(t *testing.T) {
				var args map[string]any
				if err := json.Unmarshal([]byte(input), &args); err != nil {
					t.Fatal(err)
				}
				args["id"], args["name"], args["description"] = "agent-1", "changed", "changed"
				store := &capturingBuiltinAgentStore{existing: builtinAgentFixture()}
				before, _ := json.Marshal(store.existing)
				s := &Server{agentStore: store}
				handler := s.execAgentCreate
				want := ""
				if operation == "update" {
					handler, want = s.execAgentUpdate, "high"
				}
				valid := true
				if raw, supplied := args["reasoning_effort"]; supplied {
					value, ok := raw.(string)
					valid = ok && service.ValidateReasoningEffort(value) == nil
					want = value
				}
				result, err := handler(context.Background(), args)
				if (err == nil) != valid {
					t.Fatalf("error = %v, want valid %v", err, valid)
				}
				after, _ := json.Marshal(store.existing)
				if string(before) != string(after) {
					t.Fatal("mutated fetched agent")
				}
				if !valid {
					if store.writes != 0 {
						t.Fatal("invalid effort caused a write")
					}
					return
				}
				if store.writes != 1 || store.written.Config.ReasoningEffort != want {
					t.Fatalf("unexpected stored effort: %+v", store.written)
				}
				var returned service.Agent
				if err := json.Unmarshal([]byte(result), &returned); err != nil || returned.Config.ReasoningEffort != want {
					t.Fatalf("unexpected result: %s, %v", result, err)
				}
			})
		}
	}
}

func TestBuiltinAgentReasoningEffortSchemas(t *testing.T) {
	for _, name := range []string{"agent_create", "agent_update"} {
		t.Run(name, func(t *testing.T) {
			i := slices.IndexFunc(builtinTools, func(tool builtinToolDef) bool { return tool.Name == name })
			if i < 0 {
				t.Fatal("missing tool")
			}
			schema := builtinTools[i].InputSchema
			if slices.Contains(schema["required"].([]string), "reasoning_effort") {
				t.Fatal("effort must be optional")
			}
			field := schema["properties"].(map[string]any)["reasoning_effort"].(map[string]any)
			if field["type"] != "string" || !reflect.DeepEqual(field["enum"], []string{"", "low", "medium", "high", "xhigh"}) {
				t.Fatalf("unexpected schema: %#v", field)
			}
		})
	}
}

func TestBuiltinAgentCreateReasoningEffortProviderValidation(t *testing.T) {
	for _, tt := range []struct {
		provider string
		effort   string
		valid    bool
	}{
		{"openai", "xhigh", true},
		{"anthropic", "high", true},
		{"anthropic", "xhigh", false},
		{"bedrock", "low", false},
		{"cohere", "", true},
		{"unknown", "high", false},
	} {
		t.Run(tt.provider+"/"+tt.effort, func(t *testing.T) {
			providers := newFakeProviderStore()
			provider := &service.ProviderRecord{Key: "provider-key"}
			provider.Config.Type = tt.provider
			provider.Config.Models = []string{"original-model"}
			providers.providers[provider.Key] = provider
			agents := &capturingBuiltinAgentStore{}
			s := &Server{store: providers, agentStore: agents}
			_, err := s.execAgentCreate(context.Background(), map[string]any{
				"name": "agent", "provider": provider.Key, "model": "original-model", "reasoning_effort": tt.effort,
			})
			if (err == nil) != tt.valid {
				t.Fatalf("error = %v, want valid %v", err, tt.valid)
			}
			if !tt.valid {
				if agents.writes != 0 {
					t.Fatal("unsupported effort caused a write")
				}
				return
			}
			if agents.writes != 1 || agents.written.Config.Model != "original-model" || agents.written.Config.ReasoningEffort != tt.effort {
				t.Fatalf("unexpected stored agent: %+v", agents.written)
			}
		})
	}
}

func TestAgentReasoningProviderAPI(t *testing.T) {
	for _, operation := range []string{"create", "update", "import", "preview"} {
		for _, tt := range []struct {
			provider, effort string
			valid            bool
		}{
			{"claude-key", "xhigh", false},
			{"claude-key", "high", true},
			{"claude-key", "", true},
			{"missing", "high", false},
			{"unknown-type", "high", false},
			{"missing", "", true},
		} {
			t.Run(operation+"/"+tt.provider+"/"+tt.effort, func(t *testing.T) {
				providers := newFakeProviderStore()
				provider := &service.ProviderRecord{Key: "claude-key"}
				provider.Config.Type = "anthropic"
				providers.providers[provider.Key] = provider
				providers.providers["unknown-type"] = &service.ProviderRecord{Key: "unknown-type"}
				agents := &capturingBuiltinAgentStore{existing: builtinAgentFixture()}
				s := &Server{store: providers, agentStore: agents}
				data, err := json.Marshal(service.Agent{Name: "agent", Config: service.AgentConfig{Provider: tt.provider, ReasoningEffort: tt.effort}})
				if err != nil {
					t.Fatal(err)
				}
				body := string(data)
				handler, status := s.CreateAgentAPI, http.StatusCreated
				switch operation {
				case "update":
					handler, status = s.UpdateAgentAPI, http.StatusOK
				case "import", "preview":
					body = "---\nname: agent\nprovider: " + tt.provider + "\nreasoning_effort: '" + tt.effort + "'\n---\nPrompt"
					handler = s.ImportAgentAPI
					if operation == "preview" {
						handler, status = s.PreviewImportAgentAPI, http.StatusOK
					}
				}
				if !tt.valid {
					status = http.StatusBadRequest
				}
				r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
				r.SetPathValue("id", "agent-1")
				w := httptest.NewRecorder()
				handler(w, r)
				if w.Code != status {
					t.Fatalf("status = %d, want %d: %s", w.Code, status, w.Body.String())
				}
				wantWrites := 0
				if tt.valid && operation != "preview" {
					wantWrites = 1
				}
				if agents.writes != wantWrites {
					t.Fatalf("writes = %d, want %d", agents.writes, wantWrites)
				}
			})
		}
	}
}

func TestBuiltinAgentUpdateReasoningProvider(t *testing.T) {
	for _, tt := range []struct {
		name, provider, effort, patch, want string
		valid                               bool
	}{
		{"reject effort", "claude-key", "high", `{"reasoning_effort":"xhigh"}`, "", false},
		{"reject provider change", "openai-key", "xhigh", `{"provider":"claude-key"}`, "", false},
		{"reject existing unsupported", "claude-key", "xhigh", `{}`, "", false},
		{"preserve omitted", "claude-key", "high", `{}`, "high", true},
		{"clear unsupported", "claude-key", "xhigh", `{"reasoning_effort":""}`, "", true},
		{"change and clear", "openai-key", "xhigh", `{"provider":"claude-key","reasoning_effort":""}`, "", true},
		{"change both", "openai-key", "xhigh", `{"provider":"claude-key","reasoning_effort":"high"}`, "high", true},
		{"unknown provider", "openai-key", "high", `{"provider":"missing"}`, "", false},
		{"missing provider", "openai-key", "high", `{"provider":""}`, "", false},
		{"clear without lookup", "missing", "high", `{"reasoning_effort":""}`, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			providers := newFakeProviderStore()
			for key, providerType := range map[string]string{"claude-key": "anthropic", "openai-key": "openai"} {
				provider := &service.ProviderRecord{Key: key}
				provider.Config.Type = providerType
				providers.providers[key] = provider
			}
			agents := &capturingBuiltinAgentStore{existing: builtinAgentFixture()}
			agents.existing.Config.Provider, agents.existing.Config.ReasoningEffort = tt.provider, tt.effort
			before, _ := json.Marshal(agents.existing)
			s := &Server{store: providers, agentStore: agents}
			var args map[string]any
			if err := json.Unmarshal([]byte(tt.patch), &args); err != nil {
				t.Fatal(err)
			}
			args["id"], args["name"] = "agent-1", "changed"
			_, err := s.execAgentUpdate(context.Background(), args)
			if (err == nil) != tt.valid {
				t.Fatalf("error = %v, want valid %v", err, tt.valid)
			}
			after, _ := json.Marshal(agents.existing)
			if string(before) != string(after) {
				t.Fatal("mutated fetched agent")
			}
			if !tt.valid {
				if agents.writes != 0 {
					t.Fatal("unsupported combination was saved")
				}
				return
			}
			if agents.writes != 1 || agents.written.Config.ReasoningEffort != tt.want {
				t.Fatalf("unexpected saved agent: %+v", agents.written)
			}
		})
	}
}

func TestValidateAgentReasoningConfigRegistry(t *testing.T) {
	s := &Server{providers: map[string]ProviderInfo{
		"claude-key": {providerType: "anthropic"},
		"unknown":    {providerType: "unknown"},
	}}
	for _, tt := range []struct {
		provider, effort string
		valid            bool
	}{
		{"claude-key", "xhigh", false},
		{"claude-key", "high", true},
		{"unknown", "high", false},
		{"missing", "high", false},
		{"", "high", false},
		{"missing", "", true},
	} {
		t.Run(tt.provider+"/"+tt.effort, func(t *testing.T) {
			err := s.validateAgentReasoningConfig(context.Background(), service.AgentConfig{Provider: tt.provider, ReasoningEffort: tt.effort})
			if (err == nil) != tt.valid {
				t.Fatalf("error = %v, want valid %v", err, tt.valid)
			}
		})
	}
	// The database is authoritative even if the loaded registry differs.
	providers := newFakeProviderStore()
	provider := &service.ProviderRecord{Key: "claude-key"}
	provider.Config.Type = "openai"
	providers.providers[provider.Key] = provider
	s.store = providers
	if err := s.validateAgentReasoningConfig(context.Background(), service.AgentConfig{Provider: provider.Key, ReasoningEffort: "xhigh"}); err != nil {
		t.Fatal(err)
	}
}

type failingReasoningProviderStore struct {
	service.Storer
	calls int
	err   error
}

func (s *failingReasoningProviderStore) GetProvider(context.Context, string) (*service.ProviderRecord, error) {
	s.calls++
	return nil, s.err
}

func TestValidateAgentReasoningConfigLookupFailure(t *testing.T) {
	store := &failingReasoningProviderStore{err: errors.New("lookup failed")}
	s := &Server{store: store, providers: map[string]ProviderInfo{"key": {providerType: "openai"}}}
	if err := s.validateAgentReasoningConfig(context.Background(), service.AgentConfig{Provider: "key"}); err != nil || store.calls != 0 {
		t.Fatalf("empty effort must skip lookup: calls=%d, error=%v", store.calls, err)
	}
	err := s.validateAgentReasoningConfig(context.Background(), service.AgentConfig{Provider: "key", ReasoningEffort: "high"})
	if !errors.Is(err, store.err) || store.calls != 1 {
		t.Fatalf("lookup failure must not fall back to registry: calls=%d, error=%v", store.calls, err)
	}
}
