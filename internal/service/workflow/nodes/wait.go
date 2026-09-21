package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type waitNode struct {
	Mode           string `json:"mode"`
	Seconds        int    `json:"seconds"`
	ExpiresSeconds int    `json:"expires_seconds"`
	Prompt         string `json:"prompt"`
}

func init() {
	workflow.RegisterNodeType("wait", newWaitNode)
	service.RegisterExecutionCapability("node", "wait", false)
}
func newWaitNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &waitNode{Mode: "duration", Seconds: 60, ExpiresSeconds: 604800}
	if err := decodeDataConfig(node.Data, n); err != nil {
		return nil, err
	}
	return n, nil
}
func (*waitNode) Type() string { return "wait" }
func (*waitNode) Meta() workflow.NodeMeta {
	inputs, outputs := standardDataPorts()
	return workflow.NodeMeta{Type: "wait", Label: "Wait / Approval", Category: "flow_control", Description: "Persist the workflow until a duration elapses or its owner/workspace administrator approves. Requires durable execution, outside Loop fan-out.", Inputs: inputs, Outputs: outputs, Fields: []workflow.FieldMeta{
		{Name: "mode", Type: "string", Default: "duration", Enum: []string{"duration", "approval"}},
		{Name: "seconds", Type: "number", Default: 60, Description: "Duration: 1–2592000 seconds"},
		{Name: "expires_seconds", Type: "number", Default: 604800, Description: "Approval expiry: 1–2592000 seconds"},
		{Name: "prompt", Type: "string", Description: "Approval instructions, up to 2000 bytes"},
	}}
}
func (n *waitNode) Validate(context.Context, *workflow.Registry) error {
	if n.Mode != "duration" && n.Mode != "approval" {
		return fmt.Errorf("wait: mode must be duration or approval")
	}
	value := n.Seconds
	if n.Mode == "approval" {
		value = n.ExpiresSeconds
	}
	if value < 1 || value > 2592000 {
		return fmt.Errorf("wait: duration/expiry must be between 1 second and 30 days")
	}
	if len(n.Prompt) > 2000 {
		return fmt.Errorf("wait: prompt exceeds 2000 bytes")
	}
	return nil
}
func (n *waitNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, err := dataInput(inputs)
	if err != nil {
		return nil, err
	}
	return workflow.NewWaitResult(map[string]any{"data": value}, workflow.WaitRequest{Mode: n.Mode, Seconds: n.Seconds, ExpiresSeconds: n.ExpiresSeconds, Prompt: n.Prompt}), nil
}
