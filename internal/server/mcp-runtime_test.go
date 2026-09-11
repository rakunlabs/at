package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

type fakeRuntimeMCPClient struct {
	tools      []service.Tool
	listErr    error
	callResult string
	callErr    error
	listCtx    context.Context
	callCtx    context.Context
	calls      []string
	closed     int
}

func (f *fakeRuntimeMCPClient) ListTools(ctx context.Context) ([]service.Tool, error) {
	f.listCtx = ctx
	return f.tools, f.listErr
}

func (f *fakeRuntimeMCPClient) CallTool(ctx context.Context, name string, _ map[string]any) (string, error) {
	f.callCtx = ctx
	f.calls = append(f.calls, name)
	return f.callResult, f.callErr
}

func (f *fakeRuntimeMCPClient) Close() error {
	f.closed++
	return nil
}

func TestMCPRuntimeDeterministicRoutingAndOwnership(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("request"), "request-1")
	first := &fakeRuntimeMCPClient{
		tools:      []service.Tool{{Name: "duplicate"}},
		callResult: "first",
	}
	second := &fakeRuntimeMCPClient{
		tools:      []service.Tool{{Name: "duplicate"}, {Name: "second_only"}},
		callResult: "second",
	}

	runtime := newMCPRuntime()
	runtime.addClient(ctx, mcpClientLease{client: first, owned: true}, "first upstream")
	runtime.addClient(ctx, mcpClientLease{client: second, owned: false}, "shared stdio upstream")

	tools := runtime.Tools()
	if len(tools) != 2 || tools[0].Name != "duplicate" || tools[1].Name != "second_only" {
		t.Fatalf("tools = %#v, want first-wins de-duplicated order", tools)
	}
	result, err := runtime.CallTool(ctx, "duplicate", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != "first" || len(first.calls) != 1 || len(second.calls) != 0 {
		t.Fatalf("duplicate routed to result %q, first calls %v, second calls %v", result, first.calls, second.calls)
	}
	if first.listCtx != ctx || first.callCtx != ctx || second.listCtx != ctx {
		t.Fatal("runtime did not propagate caller context to discovery and dispatch")
	}

	if err := runtime.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if first.closed != 1 {
		t.Fatalf("owned client closed %d times, want 1", first.closed)
	}
	if second.closed != 0 {
		t.Fatalf("borrowed stdio client closed %d times, want 0", second.closed)
	}
}

func TestMCPRuntimeDuplicateRouteFallsThroughOnError(t *testing.T) {
	first := &fakeRuntimeMCPClient{
		tools:   []service.Tool{{Name: "duplicate"}},
		callErr: errors.New("first unavailable"),
	}
	second := &fakeRuntimeMCPClient{
		tools:      []service.Tool{{Name: "duplicate"}},
		callResult: "second",
	}
	runtime := newMCPRuntime()
	runtime.addClientSource("first upstream", func(context.Context) (mcpClientLease, error) {
		return mcpClientLease{client: first, owned: true}, nil
	})
	runtime.addClientSource("second upstream", func(context.Context) (mcpClientLease, error) {
		return mcpClientLease{client: second, owned: true}, nil
	})
	defer runtime.Close(context.Background())

	result, err := runtime.CallTool(context.Background(), "duplicate", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != "second" || len(first.calls) != 1 || len(second.calls) != 1 {
		t.Fatalf("result = %q, first calls = %v, second calls = %v", result, first.calls, second.calls)
	}
	if tools := runtime.Tools(); len(tools) != 1 || tools[0].Name != "duplicate" {
		t.Fatalf("tools = %#v, want one de-duplicated definition", tools)
	}
}

func TestMCPRuntimePreservesUpstreamErrors(t *testing.T) {
	client := &fakeRuntimeMCPClient{
		tools:   []service.Tool{{Name: "generate"}},
		callErr: errors.New("upstream quota exhausted"),
	}
	runtime := newMCPRuntime()
	runtime.addClient(context.Background(), mcpClientLease{client: client, owned: true}, "video upstream")
	defer runtime.Close(context.Background())

	_, err := runtime.CallTool(context.Background(), "generate", nil)
	if err == nil || !strings.Contains(err.Error(), "upstream quota exhausted") || !strings.Contains(err.Error(), "video upstream") {
		t.Fatalf("CallTool error = %v, want upstream identity and cause", err)
	}
}

func TestMCPRuntimeUnknownToolIncludesDiscoveryFailure(t *testing.T) {
	client := &fakeRuntimeMCPClient{listErr: errors.New("connection reset")}
	runtime := newMCPRuntime()
	runtime.addClient(context.Background(), mcpClientLease{client: client, owned: true}, "broken upstream")
	defer runtime.Close(context.Background())

	_, err := runtime.CallTool(context.Background(), "missing", nil)
	var notFound *mcpToolNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("CallTool error = %T %v, want mcpToolNotFoundError", err, err)
	}
	if !strings.Contains(err.Error(), "connection reset") || !strings.Contains(err.Error(), "broken upstream") {
		t.Fatalf("CallTool error = %v, want retained discovery failure", err)
	}
}

type runtimeMCPSetStore struct {
	service.MCPSetStorer
	set *service.MCPSet
	ctx context.Context
}

func (s *runtimeMCPSetStore) GetMCPSetByName(ctx context.Context, _ string) (*service.MCPSet, error) {
	s.ctx = ctx
	return s.set, nil
}

type runtimeSkillStore struct {
	service.SkillStorer
	skill *service.Skill
	ctx   context.Context
}

func (s *runtimeSkillStore) GetSkillByName(ctx context.Context, _ string) (*service.Skill, error) {
	s.ctx = ctx
	return s.skill, nil
}

func TestMCPSetRuntimePropagatesContextAndPreservesPrecedence(t *testing.T) {
	type contextKey string
	ctx := context.WithValue(executiontest.Context(t), contextKey("request"), "request-2")
	setStore := &runtimeMCPSetStore{set: &service.MCPSet{
		Name: "media",
		Config: service.MCPServerConfig{
			EnabledSkills: []string{"media-skill"},
			MCPUpstreams:  []service.MCPUpstream{{URL: "https://mcp.example"}},
			HTTPTools:     []service.MCPHTTPTool{{Name: "render"}},
		},
	}}
	skillStore := &runtimeSkillStore{skill: &service.Skill{
		Name:  "media-skill",
		Tools: []service.Tool{{Name: "render", Handler: `return "skill";`}},
	}}
	upstream := &fakeRuntimeMCPClient{tools: []service.Tool{{Name: "render"}}, callResult: "upstream"}
	s := &Server{mcpSetStore: setStore, skillStore: skillStore}
	builder := s.newMCPRuntimeBuilder()
	builder.acquireUpstream = func(got context.Context, _ service.MCPUpstream) (mcpClientLease, error) {
		if got != ctx {
			t.Fatalf("upstream acquisition context differs from caller context")
		}
		return mcpClientLease{client: upstream, owned: true}, nil
	}

	runtime, err := builder.buildSet(ctx, "media")
	if err != nil {
		t.Fatalf("buildSet: %v", err)
	}
	defer runtime.Close(context.Background())
	if setStore.ctx != ctx || skillStore.ctx != ctx {
		t.Fatal("set lookup or skill lookup lost caller context")
	}
	if len(runtime.Tools()) != 1 || runtime.routes["render"][0].source != `skill "media-skill"` {
		t.Fatalf("render route = %#v, want skill to win direct-set precedence", runtime.routes["render"])
	}
	result, err := runtime.CallTool(ctx, "render", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != "skill" || len(upstream.calls) != 0 {
		t.Fatalf("result = %q, upstream calls = %v; want skill route", result, upstream.calls)
	}
	runtime.ListTools(ctx)
	if upstream.listCtx != ctx {
		t.Fatal("upstream discovery lost caller context")
	}
}

func TestGatewayMCPRuntimeHTTPToolWinsDuplicateName(t *testing.T) {
	skillStore := &runtimeSkillStore{skill: &service.Skill{
		Name:  "duplicate-skill",
		Tools: []service.Tool{{Name: "duplicate", Handler: `return "skill";`}},
	}}
	upstream := &fakeRuntimeMCPClient{tools: []service.Tool{{Name: "duplicate"}}}
	s := &Server{skillStore: skillStore}
	builder := s.newMCPRuntimeBuilder()
	builder.acquireUpstream = func(context.Context, service.MCPUpstream) (mcpClientLease, error) {
		return mcpClientLease{client: upstream, owned: true}, nil
	}
	srv := &service.MCPServer{Config: service.MCPServerConfig{
		HTTPTools:     []service.MCPHTTPTool{{Name: "duplicate"}},
		EnabledSkills: []string{"duplicate-skill"},
		MCPUpstreams:  []service.MCPUpstream{{URL: "https://mcp.example"}},
	}}

	runtime := builder.buildGateway(context.Background(), srv)
	defer runtime.Close(context.Background())
	if len(runtime.Tools()) != 1 || runtime.routes["duplicate"][0].source != "HTTP tool" {
		t.Fatalf("duplicate route = %#v, want gateway HTTP precedence", runtime.routes["duplicate"])
	}
}

func TestGatewayMCPRuntimeLocalToolSkipsUnavailableUpstream(t *testing.T) {
	skillStore := &runtimeSkillStore{skill: &service.Skill{
		Name:  "local-skill",
		Tools: []service.Tool{{Name: "local", Handler: `return "local result";`}},
	}}
	s := &Server{skillStore: skillStore}
	builder := s.newMCPRuntimeBuilder()
	acquired := false
	builder.acquireUpstream = func(ctx context.Context, _ service.MCPUpstream) (mcpClientLease, error) {
		acquired = true
		<-ctx.Done()
		return mcpClientLease{}, ctx.Err()
	}
	runtime := builder.buildGateway(context.Background(), &service.MCPServer{Config: service.MCPServerConfig{
		EnabledSkills: []string{"local-skill"},
		MCPUpstreams:  []service.MCPUpstream{{URL: "https://unavailable.example"}},
	}})
	defer runtime.Close(context.Background())

	ctx, cancel := context.WithTimeout(executiontest.Context(t), 100*time.Millisecond)
	defer cancel()
	result, err := runtime.CallTool(ctx, "local", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result != "local result" || acquired {
		t.Fatalf("result = %q, upstream acquired = %v; want prompt local dispatch", result, acquired)
	}
}

type blockingCloseMCPClient struct {
	release chan struct{}
}

func (c *blockingCloseMCPClient) ListTools(context.Context) ([]service.Tool, error) { return nil, nil }
func (c *blockingCloseMCPClient) CallTool(context.Context, string, map[string]any) (string, error) {
	return "", nil
}
func (c *blockingCloseMCPClient) Close() error {
	<-c.release
	return nil
}

func TestMCPRuntimeCloseIsBounded(t *testing.T) {
	client := &blockingCloseMCPClient{release: make(chan struct{})}
	runtime := newMCPRuntime()
	runtime.addClient(context.Background(), mcpClientLease{client: client, owned: true}, "blocking client")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runtime.Close(ctx)
	close(client.release)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Close took %v, want bounded cleanup", elapsed)
	}
}

func TestGatewayMCPCallRetainsUpstreamJSONRPCError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req service.MCPRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		switch req.Method {
		case "initialize":
			mcpResult(w, req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]string{"name": "fake", "version": "1"},
			})
		case "notifications/initialized", "notifications/cancelled":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			mcpResult(w, req.ID, map[string]any{"tools": []service.Tool{{Name: "generate"}}})
		case "tools/call":
			mcpError(w, req.ID, -32099, "upstream quota exhausted")
		default:
			t.Errorf("unexpected upstream method %q", req.Method)
		}
	}))
	defer upstream.Close()

	s := &Server{}
	req := service.MCPRequest{
		Jsonrpc: "2.0",
		ID:      42,
		Method:  "tools/call",
		Params:  map[string]any{"name": "generate", "arguments": map[string]any{}},
	}
	httpReq := httptest.NewRequest(http.MethodPost, "/gateway/v1/mcp/test", nil)
	recorder := httptest.NewRecorder()
	s.gwGenMCPCallTool(recorder, httpReq, req, &service.MCPServer{Config: service.MCPServerConfig{
		MCPUpstreams: []service.MCPUpstream{{URL: upstream.URL}},
	}})

	var response service.MCPResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode gateway response: %v", err)
	}
	if recorder.Code != http.StatusOK || response.Jsonrpc != "2.0" || response.ID != 42 {
		t.Fatalf("gateway response = status %d, %#v", recorder.Code, response)
	}
	if response.Error == nil || response.Error.Code != -32000 || !strings.Contains(response.Error.Message, "upstream quota exhausted") {
		t.Fatalf("gateway error = %#v, want retained upstream JSON-RPC failure", response.Error)
	}
}
