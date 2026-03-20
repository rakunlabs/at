package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// agentConfigNode is a resource node that outputs a list of agent IDs.
// It is designed to be connected to the bottom "agents" handle of an
// agent_call node.
//
// Config (node.Data):
//
//	"agent_id": string — The ID of the selected agent (required)
//
// Output ports:
//
//	"agent" — returns the agent ID as a string (or list if multiple connected)
type agentConfigNode struct {
	agentID string
}

func init() {
	workflow.RegisterNodeType("agent_config", newAgentConfigNode)
}

func newAgentConfigNode(node service.WorkflowNode) (workflow.Noder, error) {
	agentID, _ := node.Data["agent_id"].(string)
	return &agentConfigNode{
		agentID: agentID,
	}, nil
}

func (n *agentConfigNode) Type() string { return "agent_config" }

func (n *agentConfigNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "agent_config",
		Label:       "Agent Config",
		Category:    "resources",
		Description: "Provide an agent ID for delegation in agent_call",
		Inputs:      []workflow.PortMeta{},
		Outputs: []workflow.PortMeta{
			{Name: "agent", Type: workflow.PortTypeConfig, Label: "Agent", Position: "top"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "agent_id", Type: "string", Required: true, Description: "Agent ID to delegate to"},
		},
		Color: "indigo",
	}
}

func (n *agentConfigNode) Validate(ctx context.Context, reg *workflow.Registry) error {
	if n.agentID == "" {
		return fmt.Errorf("agent_config: 'agent_id' is required")
	}
	// Verify agent exists
	if reg.AgentLookup != nil {
		if _, err := reg.AgentLookup(ctx, n.agentID); err != nil {
			return fmt.Errorf("agent_config: agent %q: %w", n.agentID, err)
		}
	}
	return nil
}

func (n *agentConfigNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Return the agent ID.
	// The downstream agent_call node will collect these into a list.
	return workflow.NewResult(map[string]any{
		"agent": n.agentID,
	}), nil
}
