package nodes

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// outputNode collects final results into the registry.
// It has one input port ("input") and no output ports.
type outputNode struct{}

func init() {
	workflow.RegisterNodeType("output", newOutputNode)
}

func newOutputNode(_ service.WorkflowNode) (workflow.Noder, error) {
	return &outputNode{}, nil
}

func (n *outputNode) Type() string { return "output" }

func (n *outputNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

// Run merges all incoming data into the registry's outputs.
func (n *outputNode) Run(_ context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	reg.SetOutputs(inputs)
	return workflow.NewResult(inputs), nil
}
