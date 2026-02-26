package nodes

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// mcpConfigNode is a resource configuration node that outputs a list of
// MCP server URLs. It is designed to be connected to the bottom "mcp" handle
// of an agent_call node.
//
// Config (node.Data):
//
//	"mcp_urls": []string — MCP server URLs to pass downstream
//
// Input ports:  (none)
// Output ports: "mcp_urls" — []string of MCP server URLs
type mcpConfigNode struct {
	mcpURLs []string
}

func init() {
	workflow.RegisterNodeType("mcp_config", newMCPConfigNode)
}

func newMCPConfigNode(node service.WorkflowNode) (workflow.Noder, error) {
	var mcpURLs []string
	if raw, ok := node.Data["mcp_urls"].([]any); ok {
		for _, u := range raw {
			if s, ok := u.(string); ok && s != "" {
				mcpURLs = append(mcpURLs, s)
			}
		}
	}

	return &mcpConfigNode{mcpURLs: mcpURLs}, nil
}

func (n *mcpConfigNode) Type() string { return "mcp_config" }

func (n *mcpConfigNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

func (n *mcpConfigNode) Run(_ context.Context, _ *workflow.Registry, _ map[string]any) (workflow.NodeResult, error) {
	return workflow.NewResult(map[string]any{
		"mcp_urls": n.mcpURLs,
	}), nil
}
