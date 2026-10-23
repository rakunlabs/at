package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// fakeToolMCP serves one tool, "remote_echo", over plain HTTP JSON-RPC.
func fakeToolMCP(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req service.MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		result := `{}`
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}`
		case "tools/list":
			result = `{"tools":[{"name":"remote_echo","description":"Echo the input back. Second sentence.","inputSchema":{"type":"object","properties":{"text":{"type":"string"}}}}]}`
		case "tools/call":
			params, _ := req.Params.(map[string]any)
			args, _ := params["arguments"].(map[string]any)
			text, _ := args["text"].(string)
			result = `{"content":[{"type":"text","text":"echo:` + text + `"}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(service.MCPResponse{Jsonrpc: "2.0", ID: req.ID, Result: json.RawMessage(result)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// With MCP code mode an agent's MCP tools leave the model's tool list; the
// model gets code_execute and a catalog, and the script reaches the tool.
func TestChatSessionMCPCodeModeHidesToolsBehindCodeExecute(t *testing.T) {
	mcp := fakeToolMCP(t)
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "c1", Name: codeModeToolName, Arguments: map[string]any{
			"code": `const r = await tools["remote_echo"]({text: "hi"}); return r;`,
		}}}},
		{Content: "done", Finished: true},
	}}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{
			Provider: "prov1", Model: "m1", MaxIterations: 5, MCPs: []string{mcp.URL}, MCPCodeMode: true,
		}},
	}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, agents, nil)
	s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "sess-1", AgentID: "agent-a"}}

	var toolResult string
	if err := s.RunAgenticLoop(s.ctx, "sess-1", "go", func(ev AgenticEvent) {
		if ev.Type == "tool_result" && ev.ToolName == codeModeToolName {
			toolResult = ev.Result
		}
	}); err != nil {
		t.Fatal(err)
	}

	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.tools) == 0 {
		t.Fatal("provider was not called")
	}
	var names []string
	for _, tool := range provider.tools[0] {
		names = append(names, tool.Name)
	}
	if strings.Contains(strings.Join(names, ","), "remote_echo") || !strings.Contains(strings.Join(names, ","), codeModeToolName) {
		t.Fatalf("tool list = %v; want code_execute without remote_echo", names)
	}
	system, _ := provider.requests[0][0].Content.(string)
	if !strings.Contains(system, "- remote_echo: Echo the input back.") {
		t.Fatalf("system prompt lacks the MCP catalog:\n%s", system)
	}
	if !strings.Contains(toolResult, "echo:hi") {
		t.Fatalf("code_execute result = %q", toolResult)
	}
}

func TestChatSessionWithoutMCPCodeModeListsTools(t *testing.T) {
	mcp := fakeToolMCP(t)
	provider := &fakeObsProvider{responses: []*service.LLMResponse{{Content: "done", Finished: true}}}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5, MCPs: []string{mcp.URL}}},
	}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, agents, nil)
	s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "sess-1", AgentID: "agent-a"}}
	if err := s.RunAgenticLoop(s.ctx, "sess-1", "go", func(AgenticEvent) {}); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	found := false
	for _, tool := range provider.tools[0] {
		found = found || tool.Name == "remote_echo"
	}
	if !found {
		t.Fatal("MCP tools must be listed when code mode is off")
	}
}
