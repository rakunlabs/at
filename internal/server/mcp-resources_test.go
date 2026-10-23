package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// fakeFeatureMCP serves one tool, two resources, one prompt and instructions.
func fakeFeatureMCP(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req service.MCPRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		params, _ := req.Params.(map[string]any)
		result := `{}`
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","instructions":"Always pass the project id.","capabilities":{"tools":{},"resources":{},"prompts":{}},"serverInfo":{"name":"docs","version":"1"}}`
		case "tools/list":
			result = `{"tools":[{"name":"search_docs","description":"Search.","inputSchema":{"type":"object"}}]}`
		case "resources/list":
			if params["cursor"] == "p2" {
				result = `{"resources":[{"uri":"docs://b","name":"B","mimeType":"image/png"}]}`
			} else {
				result = `{"resources":[{"uri":"docs://a","name":"A","mimeType":"text/markdown"}],"nextCursor":"p2"}`
			}
		case "resources/templates/list":
			result = `{"resourceTemplates":[{"uriTemplate":"docs://page/{id}","name":"Page"}]}`
		case "resources/read":
			if params["uri"] == "docs://b" {
				result = `{"contents":[{"uri":"docs://b","mimeType":"image/png","blob":"AAAA"}]}`
			} else {
				result = `{"contents":[{"uri":"docs://a","mimeType":"text/markdown","text":"# Hello"}]}`
			}
		case "prompts/list":
			result = `{"prompts":[{"name":"review","description":"Review a PR","arguments":[{"name":"pr","required":true}]}]}`
		case "prompts/get":
			args, _ := params["arguments"].(map[string]any)
			result = `{"messages":[{"role":"user","content":{"type":"text","text":"Review PR ` + args["pr"].(string) + `"}}]}`
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(service.MCPResponse{Jsonrpc: "2.0", ID: req.ID, Result: json.RawMessage(result)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMCPFeatureSetResourcesAndPrompts(t *testing.T) {
	mcp := fakeFeatureMCP(t)
	client, err := service.NewHTTPMCPClient(t.Context(), mcp.URL)
	if err != nil {
		t.Fatal(err)
	}
	var set mcpFeatureSet
	set.add(client, "fallback")

	if !strings.Contains(set.Prompt(), `<mcp_instructions server="docs">`) || !strings.Contains(set.Prompt(), "Always pass the project id.") {
		t.Fatalf("instructions prompt = %q", set.Prompt())
	}
	if len(set.Tools()) != 2 || !set.Owns(mcpReadResourceTool) {
		t.Fatalf("resource tools = %v", set.Tools())
	}

	list, err := set.Call(t.Context(), mcpListResourcesTool, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"uri": "docs://a"`, `"uri": "docs://b"`, `"server": "docs"`, `"uriTemplate": "docs://page/{id}"`} {
		if !strings.Contains(list, want) {
			t.Fatalf("missing %s in %s", want, list)
		}
	}
	text, err := set.Call(t.Context(), mcpReadResourceTool, map[string]any{"uri": "docs://a", "server": "docs"})
	if err != nil || !strings.Contains(text, "# Hello") {
		t.Fatalf("read = %q %v", text, err)
	}
	blob, err := set.Call(t.Context(), mcpReadResourceTool, map[string]any{"uri": "docs://b"})
	if err != nil || !strings.Contains(blob, "binary content, image/png") {
		t.Fatalf("blob = %q %v", blob, err)
	}
	if _, err := set.Call(t.Context(), mcpReadResourceTool, map[string]any{"uri": "docs://a", "server": "nope"}); err == nil {
		t.Fatal("unknown server must fail")
	}

	prompts := set.Prompts(t.Context())
	if len(prompts) != 1 || prompts[0].Server != "docs" || prompts[0].Name != "review" {
		t.Fatalf("prompts = %+v", prompts)
	}
	rendered, err := set.GetPrompt(t.Context(), "docs", "review", map[string]string{"pr": "42"})
	if err != nil || rendered != "Review PR 42" {
		t.Fatalf("prompt = %q %v", rendered, err)
	}
}

// A Sessions agent sees the server's instructions and can read its resources.
func TestChatSessionMCPInstructionsAndResources(t *testing.T) {
	mcp := fakeFeatureMCP(t)
	provider := &fakeObsProvider{responses: []*service.LLMResponse{
		{ToolCalls: []service.ToolCall{{ID: "c1", Name: mcpReadResourceTool, Arguments: map[string]any{"uri": "docs://a"}}}},
		{Content: "done", Finished: true},
	}}
	agents := map[string]*service.Agent{
		"agent-a": {ID: "agent-a", Name: "Alpha", Config: service.AgentConfig{Provider: "prov1", Model: "m1", MaxIterations: 5, MCPs: []string{mcp.URL}}},
	}
	s, _ := newObsTestServer(t, provider, &fakeLLMCallStore{}, agents, nil)
	s.chatSessionStore = &fakeChatSessionStore{session: service.ChatSession{ID: "sess-1", AgentID: "agent-a"}}
	var result string
	if err := s.RunAgenticLoop(s.ctx, "sess-1", "go", func(ev AgenticEvent) {
		if ev.Type == "tool_result" && ev.ToolName == mcpReadResourceTool {
			result = ev.Result
		}
	}); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	system, _ := provider.requests[0][0].Content.(string)
	if !strings.Contains(system, "Always pass the project id.") {
		t.Fatalf("system prompt lacks MCP instructions:\n%s", system)
	}
	var names []string
	for _, tool := range provider.tools[0] {
		names = append(names, tool.Name)
	}
	if !strings.Contains(strings.Join(names, ","), mcpListResourcesTool) {
		t.Fatalf("tools = %v", names)
	}
	if !strings.Contains(result, "# Hello") {
		t.Fatalf("resource result = %q", result)
	}
}

func TestMCPPromptText(t *testing.T) {
	got := service.MCPPromptText([]service.MCPPromptMessage{
		{Role: "user", Content: json.RawMessage(`{"type":"text","text":"Question"}`)},
		{Role: "assistant", Content: json.RawMessage(`{"type":"resource","resource":{"uri":"x://y","text":"Body"}}`)},
		{Role: "user", Content: json.RawMessage(`{"type":"image","data":"AA","mimeType":"image/png"}`)},
	})
	want := "[user] Question\n\n[assistant] Body\n\n[user] [image content omitted]"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
