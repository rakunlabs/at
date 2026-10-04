package nodes_test

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

// TestAgentCall_UsesAgentMCPSets verifies that a stored agent's MCP sets are
// offered to the model and dispatched through the MCP-set caller, together
// with the agent's system prompt.
func TestAgentCall_UsesAgentMCPSets(t *testing.T) {
	ctx := executiontest.Context(t)
	calls := 0
	mp := &mockProvider{chatFunc: func(_ context.Context, _ string, messages []service.Message, tools []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		calls++
		if calls == 1 {
			if len(messages) == 0 || messages[0].Role != "system" || messages[0].Content != "agent prompt" {
				t.Fatalf("agent system prompt not used: %+v", messages)
			}
			if len(tools) != 1 || tools[0].Name != "lookup" {
				t.Fatalf("agent MCP set tools not offered: %+v", tools)
			}
			return &service.LLMResponse{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "lookup", Arguments: map[string]any{"q": "x"}}}}, nil
		}
		return &service.LLMResponse{Content: "done", Finished: true}, nil
	}}
	reg := newTestRegistryWithProvider(mp)
	reg.AgentLookup = func(context.Context, string) (*service.Agent, error) {
		return &service.Agent{ID: "a1", Config: service.AgentConfig{
			Provider: "test-provider", SystemPrompt: "agent prompt", MCPSets: []string{"docs", "docs"},
		}}, nil
	}
	listed := 0
	reg.MCPSetToolLister = func(_ context.Context, set string) ([]service.Tool, error) {
		listed++
		if set != "docs" {
			t.Fatalf("unexpected set %q", set)
		}
		return []service.Tool{{Name: "lookup"}}, nil
	}
	var calledSet string
	reg.MCPSetToolCaller = func(_ context.Context, set, tool string, _ map[string]any) (string, error) {
		calledSet = set + "/" + tool
		return "found", nil
	}

	node := makeNode(t, "agent_call", map[string]any{"agent_id": "a1", "max_iterations": float64(3)})
	res, err := node.Run(ctx, reg, map[string]any{"prompt": "find x"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if listed != 1 || calledSet != "docs/lookup" || res.Data()["response"] != "done" {
		t.Fatalf("listed=%d called=%q result=%+v", listed, calledSet, res.Data())
	}
}

// TestAgentCall_UsesMCPConfigSets verifies that MCP sets selected on an
// mcp_config node reach the agent through the "mcp" port and are dispatched
// through the MCP-set runtime, not dialled as URLs.
func TestAgentCall_UsesMCPConfigSets(t *testing.T) {
	ctx := executiontest.Context(t)

	cfg := makeNode(t, "mcp_config", map[string]any{"mcp_sets": []any{"docs", "docs", ""}})
	cfgRes, err := cfg.Run(ctx, nil, nil)
	if err != nil {
		t.Fatalf("mcp_config Run: %v", err)
	}
	port, ok := cfgRes.Data()["mcp_urls"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_config output = %#v", cfgRes.Data())
	}
	if sets, _ := port["mcp_sets"].([]string); len(sets) != 1 || sets[0] != "docs" {
		t.Fatalf("mcp_sets = %#v", port["mcp_sets"])
	}

	calls := 0
	mp := &mockProvider{chatFunc: func(_ context.Context, _ string, _ []service.Message, tools []service.Tool, _ *service.ChatOptions) (*service.LLMResponse, error) {
		calls++
		if calls == 1 {
			if len(tools) != 1 || tools[0].Name != "lookup" {
				t.Fatalf("MCP config set tools not offered: %+v", tools)
			}
			return &service.LLMResponse{ToolCalls: []service.ToolCall{{ID: "tc1", Name: "lookup", Arguments: map[string]any{}}}}, nil
		}
		return &service.LLMResponse{Content: "done", Finished: true}, nil
	}}
	reg := newTestRegistryWithProvider(mp)
	reg.MCPSetToolLister = func(_ context.Context, set string) ([]service.Tool, error) {
		if set != "docs" {
			t.Fatalf("unexpected set %q", set)
		}
		return []service.Tool{{Name: "lookup"}}, nil
	}
	var calledSet string
	reg.MCPSetToolCaller = func(_ context.Context, set, tool string, _ map[string]any) (string, error) {
		calledSet = set + "/" + tool
		return "found", nil
	}

	node := makeNode(t, "agent_call", map[string]any{"provider": "test-provider", "max_iterations": float64(3)})
	res, err := node.Run(ctx, reg, map[string]any{"prompt": "find x", "mcp": port})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calledSet != "docs/lookup" || res.Data()["response"] != "done" {
		t.Fatalf("called=%q result=%+v", calledSet, res.Data())
	}
}
