package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/service"
)

type collectionStore struct {
	service.Storer
	err       error
	nilResult bool
	agent     *service.Agent
}

func collectionResult[T any](s *collectionStore) (*service.ListResult[T], error) {
	if s.err != nil || s.nilResult {
		return nil, s.err
	}
	return &service.ListResult[T]{Meta: service.ListMeta{Offset: 20, Limit: 10}}, nil
}
func (s *collectionStore) ListAgents(context.Context, *query.Query) (*service.ListResult[service.Agent], error) {
	return collectionResult[service.Agent](s)
}
func (s *collectionStore) ListWorkflows(context.Context, *query.Query) (*service.ListResult[service.Workflow], error) {
	return collectionResult[service.Workflow](s)
}
func (s *collectionStore) ListProviders(context.Context, *query.Query) (*service.ListResult[service.ProviderRecord], error) {
	return collectionResult[service.ProviderRecord](s)
}
func (s *collectionStore) ListAPITokens(context.Context, *query.Query) (*service.ListResult[service.APIToken], error) {
	return collectionResult[service.APIToken](s)
}
func (s *collectionStore) ListOrganizations(context.Context, *query.Query) (*service.ListResult[service.Organization], error) {
	return collectionResult[service.Organization](s)
}
func (s *collectionStore) ListProjects(context.Context, *query.Query) (*service.ListResult[service.Project], error) {
	return collectionResult[service.Project](s)
}
func (s *collectionStore) ListGoals(context.Context, *query.Query) (*service.ListResult[service.Goal], error) {
	return collectionResult[service.Goal](s)
}
func (s *collectionStore) ListTasks(context.Context, *query.Query) (*service.ListResult[service.Task], error) {
	return collectionResult[service.Task](s)
}
func (s *collectionStore) ListMCPServers(context.Context, *query.Query) (*service.ListResult[service.MCPServer], error) {
	return collectionResult[service.MCPServer](s)
}
func (s *collectionStore) ListMCPSets(context.Context, *query.Query) (*service.ListResult[service.MCPSet], error) {
	return collectionResult[service.MCPSet](s)
}
func (s *collectionStore) GetAgent(context.Context, string) (*service.Agent, error) {
	return s.agent, s.err
}
func (s *collectionStore) GetAgentBudget(context.Context, string) (*service.AgentBudget, error) {
	return nil, s.err
}

func TestCollectionEndpointContract(t *testing.T) {
	store := &collectionStore{}
	s := &Server{store: store, agentStore: store, workflowStore: store, tokenStore: store, organizationStore: store, projectStore: store, goalStore: store, taskStore: store, mcpServerStore: store, mcpSetStore: store}
	for name, handler := range map[string]http.HandlerFunc{
		"agents": s.ListAgentsAPI, "workflows": s.ListWorkflowsAPI, "providers": s.ListProvidersAPI,
		"tokens": s.ListAPITokensAPI, "organizations": s.ListOrganizationsAPI, "projects": s.ListProjectsAPI,
		"goals": s.ListGoalsAPI, "tasks": s.ListTasksAPI, "mcp_servers": s.ListMCPServersAPI, "mcp_sets": s.ListMCPSetsAPI,
	} {
		t.Run(name, func(t *testing.T) {
			for _, nilResult := range []bool{false, true} {
				store.nilResult, store.err = nilResult, nil
				w := httptest.NewRecorder()
				handler(w, httptest.NewRequest("GET", "/?_offset=20&_limit=10", nil))
				if w.Code != 200 {
					t.Fatalf("empty collection: status %d: %s", w.Code, w.Body.String())
				}
				var body struct {
					Data json.RawMessage   `json:"data"`
					Meta map[string]uint64 `json:"meta"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if string(body.Data) != "[]" {
					t.Fatalf("empty collection must be []: %s", w.Body.String())
				}
				for _, key := range []string{"total", "offset", "limit"} {
					if _, ok := body.Meta[key]; !ok {
						t.Fatalf("missing meta.%s: %s", key, w.Body.String())
					}
				}
				if !nilResult && (body.Meta["offset"] != 20 || body.Meta["limit"] != 10) {
					t.Fatalf("lost pagination: %s", w.Body.String())
				}
			}
			store.err = errors.New("database unavailable")
			w := httptest.NewRecorder()
			handler(w, httptest.NewRequest("GET", "/", nil))
			if w.Code != 500 {
				t.Fatalf("store failure became status %d", w.Code)
			}
			store.err = nil
		})
	}
}

func TestCollectionJSONPositions(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value any
		want  string
	}{
		{"bare slice", []string(nil), `[]`},
		{"envelope", map[string]any{"items": []string(nil), "optional": (*string)(nil)}, `{"items":[],"optional":null}`},
		{"optional record", (*service.Agent)(nil), `null`},
		{"bytes", []byte(nil), `null`},
		{"populated", []string{"one"}, `["one"]`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			httpResponseJSON(w, tt.value, 200)
			if w.Body.String() != tt.want {
				t.Fatalf("got %s, want %s", w.Body.String(), tt.want)
			}
		})
	}
}

func TestUnconfiguredAgentBudgetIsNormalState(t *testing.T) {
	store := &collectionStore{agent: &service.Agent{ID: "agent"}}
	s := &Server{agentStore: store, agentBudgetStore: store, costEventStore: store}
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/agents/agent/budget", nil)
		r.SetPathValue("id", "agent")
		w := httptest.NewRecorder()
		s.GetAgentBudgetAPI(w, r)
		return w
	}
	if w := request(); w.Code != 200 || w.Body.String() != "null" {
		t.Fatalf("unconfigured budget: %d %s", w.Code, w.Body.String())
	}
	store.agent = nil
	if w := request(); w.Code != 404 {
		t.Fatalf("missing agent: %d", w.Code)
	}
	store.err = errors.New("database unavailable")
	if w := request(); w.Code != 500 {
		t.Fatalf("failed budget lookup: %d", w.Code)
	}
}

func TestDisabledFeatureIsNotAnEmptyCollection(t *testing.T) {
	s := &Server{featureStore: &fakeFeatureStore{key: service.FeatureAgents, enabled: false}}
	called := false
	handler := s.featureGateMiddleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/agents", nil))
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if called || w.Code != 404 || body["code"] != "feature_disabled" || body["feature"] != service.FeatureAgents {
		t.Fatalf("disabled feature must remain a typed admission failure: %d %s", w.Code, w.Body.String())
	}
}
