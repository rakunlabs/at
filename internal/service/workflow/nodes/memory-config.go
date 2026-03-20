package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// memoryConfigNode is a resource configuration node that can operate in two modes:
//
// Mode "static" (default): passes through input data to an agent_call node's
// memory port. This preserves backward compatibility.
//
// Mode "recall": queries the agent memory store for relevant past memories and
// outputs them as formatted text suitable for injection into an LLM prompt.
//
// Config (node.Data):
//
//	mode:            "static" (default) | "recall"
//	agent_id:        agent ID for recall (required for recall mode)
//	organization_id: organization ID for recall (required for recall mode)
//	max_tokens:      maximum token budget for recall (default 2000)
//
// Input ports:  "data" — arbitrary data to forward as memory (static mode)
// Output ports: "memory" — the memory data or recalled memories
type memoryConfigNode struct {
	mode           string
	agentID        string
	organizationID string
	maxTokens      int
}

func init() {
	workflow.RegisterNodeType("memory_config", newMemoryConfigNode)
}

func newMemoryConfigNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &memoryConfigNode{
		mode:      "static",
		maxTokens: 2000,
	}

	if node.Data != nil {
		if v, ok := node.Data["mode"].(string); ok && v != "" {
			n.mode = v
		}
		if v, ok := node.Data["agent_id"].(string); ok {
			n.agentID = v
		}
		if v, ok := node.Data["organization_id"].(string); ok {
			n.organizationID = v
		}
		if v, ok := node.Data["max_tokens"].(float64); ok && v > 0 {
			n.maxTokens = int(v)
		}
	}

	return n, nil
}

func (n *memoryConfigNode) Type() string { return "memory_config" }

func (n *memoryConfigNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "memory_config",
		Label:       "Memory Config",
		Category:    "resources",
		Description: "Provide memory context for an agent_call node",
		Inputs: []workflow.PortMeta{
			{Name: "data", Type: workflow.PortTypeData, Label: "Data", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "memory", Type: workflow.PortTypeConfig, Label: "Memory", Position: "top"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
		},
		Color: "teal",
	}
}

func (n *memoryConfigNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if n.mode == "recall" {
		if n.agentID == "" {
			return fmt.Errorf("memory_config: recall mode requires agent_id")
		}
		if n.organizationID == "" {
			return fmt.Errorf("memory_config: recall mode requires organization_id")
		}
	}

	return nil
}

func (n *memoryConfigNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	if n.mode == "recall" {
		return n.runRecall(ctx, reg)
	}

	return n.runStatic(inputs)
}

// runStatic preserves the original pass-through behavior.
func (n *memoryConfigNode) runStatic(inputs map[string]any) (workflow.NodeResult, error) {
	var memory any
	if v, ok := inputs["data"]; ok {
		memory = v
	} else {
		// Fallback: merge all inputs.
		memory = inputs
	}

	return workflow.NewResult(map[string]any{
		"memory": memory,
	}), nil
}

// runRecall queries the agent memory store for relevant past memories.
func (n *memoryConfigNode) runRecall(ctx context.Context, reg *workflow.Registry) (workflow.NodeResult, error) {
	if reg.MemoryRecall == nil {
		// Fallback to empty memory if recall is not configured.
		return workflow.NewResult(map[string]any{
			"memory": "",
		}), nil
	}

	recalled, err := reg.MemoryRecall(ctx, n.agentID, n.organizationID, n.maxTokens)
	if err != nil {
		return nil, fmt.Errorf("memory_config: recall failed: %w", err)
	}

	return workflow.NewResult(map[string]any{
		"memory": recalled,
	}), nil
}
