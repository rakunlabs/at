package nodes

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// mcpConfigNode is a resource configuration node that selects registered MCP
// sets for an agent_call node. It is designed to be connected to the bottom
// "mcp" handle of an agent_call node.
//
// MCP servers are registered once on the MCP page (MCP sets) and only
// referenced here, so credentials, stdio processes and execution admission
// travel with the set instead of being re-entered per workflow.
//
// Config (node.Data):
//
//	"mcp_sets": []string — names of registered MCP sets
//	"mcp_urls": []string — legacy raw endpoint URLs; still honoured for saved
//	                       graphs but no longer editable in the UI
//
// Input ports:  (none)
// Output ports: "mcp_urls" — {mcp_sets: []string, urls: []string}. The port
// keeps its historical name so edges in saved graphs stay connected.
type mcpConfigNode struct {
	mcpSets []string
	mcpURLs []string
}

func init() {
	workflow.RegisterNodeType("mcp_config", newMCPConfigNode)
}

func newMCPConfigNode(node service.WorkflowNode) (workflow.Noder, error) {
	return &mcpConfigNode{
		mcpSets: uniqueStrings(inputStrings(node.Data["mcp_sets"])),
		mcpURLs: uniqueStrings(inputStrings(node.Data["mcp_urls"])),
	}, nil
}

func (n *mcpConfigNode) Type() string { return "mcp_config" }

func (n *mcpConfigNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "mcp_config",
		Label:       "MCP Config",
		Category:    "resources",
		Description: "Select registered MCP sets for an agent_call node",
		Inputs:      []workflow.PortMeta{},
		Outputs: []workflow.PortMeta{
			{Name: "mcp_urls", Type: workflow.PortTypeConfig, Label: "MCP", Position: "top"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "mcp_sets", Type: "array", Required: true, Description: "Names of registered MCP sets (from the MCP page) to provide to agent_call. Raw MCP URLs are not accepted."},
		},
		Color: "green",
	}
}

func (n *mcpConfigNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

func (n *mcpConfigNode) Run(_ context.Context, _ *workflow.Registry, _ map[string]any) (workflow.NodeResult, error) {
	sets := n.mcpSets
	if sets == nil {
		sets = []string{}
	}
	urls := n.mcpURLs
	if urls == nil {
		urls = []string{}
	}

	return workflow.NewResult(map[string]any{
		"mcp_urls": map[string]any{
			"mcp_sets": sets,
			"urls":     urls,
		},
	}), nil
}
