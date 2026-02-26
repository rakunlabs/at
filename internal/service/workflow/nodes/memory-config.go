package nodes

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// memoryConfigNode is a resource configuration node that passes through
// memory/context data to an agent_call node. It is designed to be connected
// to the bottom "memory" handle of an agent_call node.
//
// The memory node accepts arbitrary data from upstream and forwards it as
// the "memory" output. The agent_call node converts this to additional
// context for the LLM conversation.
//
// Config (node.Data):
//
//	(none currently — reserved for future memory_type config)
//
// Input ports:  "data" — arbitrary data to forward as memory
// Output ports: "memory" — the memory data passed through
type memoryConfigNode struct{}

func init() {
	workflow.RegisterNodeType("memory_config", newMemoryConfigNode)
}

func newMemoryConfigNode(_ service.WorkflowNode) (workflow.Noder, error) {
	return &memoryConfigNode{}, nil
}

func (n *memoryConfigNode) Type() string { return "memory_config" }

func (n *memoryConfigNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

func (n *memoryConfigNode) Run(_ context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Pass through the input data as memory.
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
