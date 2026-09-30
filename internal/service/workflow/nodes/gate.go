package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// gateNode is an in-run barrier: "data" continues only once "signal" has
// arrived in the same invocation.
//
// The engine runs nodes in topological order, so both inputs have already
// settled when the gate runs. The decision is therefore whether the signal
// branch actually delivered: a signal behind an inactive conditional port, a
// failed step skipped by on_error=continue, or an empty branch leaves the
// signal absent, and the gate stops its own branch rather than letting data
// through ahead of the work it was meant to follow. Unlike Wait it never
// persists or sleeps, so it needs no durable launch and works inside Loop.
//
// Config (node.Data):
//
//	"pass_signal": bool — also emit the signal value as "signal" (default false)
type gateNode struct {
	PassSignal bool `json:"pass_signal"`
}

func init() {
	workflow.RegisterNodeType("gate", newGateNode)
	service.RegisterExecutionCapability("node", "gate", false)
}

func newGateNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &gateNode{}
	if err := decodeDataConfig(node.Data, n); err != nil {
		return nil, err
	}
	return n, nil
}

func (*gateNode) Type() string { return "gate" }

func (*gateNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "gate",
		Label:       "Gate",
		Category:    "flow_control",
		Description: "Pass data on only after the signal branch has completed in this run",
		Inputs: []workflow.PortMeta{
			{Name: "data", Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "Data", Position: "left"},
			{Name: "signal", Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "Signal", Position: "left"},
		},
		Outputs: []workflow.PortMeta{
			{Name: "data", Type: workflow.PortTypeData, Label: "Data", Position: "right"},
		},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "pass_signal", Type: "boolean", Default: false, Description: "Also emit the signal value as 'signal'"},
		},
		Color: "amber",
	}
}

func (*gateNode) Validate(context.Context, *workflow.Registry) error { return nil }

func (n *gateNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	signal, hasSignal := inputs["signal"]
	if !hasSignal {
		return nil, fmt.Errorf("gate: %w", workflow.ErrStopBranch)
	}
	data, hasData := inputs["data"]
	if !hasData {
		return nil, fmt.Errorf("gate: %w", workflow.ErrStopBranch)
	}
	out := map[string]any{"data": data}
	if n.PassSignal {
		out["signal"] = signal
	}
	return workflow.NewResult(out), nil
}
