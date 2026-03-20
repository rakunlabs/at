package nodes

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// inputNode passes the workflow's run-time inputs downstream.
// It has no input ports and one output port ("data").
type inputNode struct{}

func init() {
	workflow.RegisterNodeType("input", newInputNode)
}

func newInputNode(_ service.WorkflowNode) (workflow.Noder, error) {
	return &inputNode{}, nil
}

func (n *inputNode) Type() string { return "input" }

func (n *inputNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "input",
		Label:       "Input",
		Category:    "entry",
		Description: "Passes workflow trigger inputs downstream",
		Inputs:      []workflow.PortMeta{},
		Outputs: []workflow.PortMeta{
			{Name: "data", Type: workflow.PortTypeData, Label: "Data", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
		},
		Color: "emerald",
	}
}

func (n *inputNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

// Run outputs the original workflow trigger inputs on the "data" port.
func (n *inputNode) Run(_ context.Context, reg *workflow.Registry, _ map[string]any) (workflow.NodeResult, error) {
	return workflow.NewResult(map[string]any{
		"data": reg.RunInputs,
	}), nil
}
