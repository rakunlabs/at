package nodes

import (
	"context"
	"encoding/json"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// cronTriggerNode merges a static payload (from node.Data["payload"]) with
// trigger metadata from the registry's RunInputs. The combined data is emitted
// on the "data" output port.
//
// RunInputs for cron-triggered executions contain:
//
//	trigger_type:  "cron"
//	triggered_at:  RFC3339 timestamp
//	schedule:      the cron expression that fired
//	trigger_id:    the trigger's database ID
type cronTriggerNode struct {
	payload map[string]any
}

func init() {
	workflow.RegisterNodeType("cron_trigger", newCronTriggerNode)
}

func newCronTriggerNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &cronTriggerNode{}

	// Extract static payload from node data if present.
	if raw, ok := node.Data["payload"]; ok {
		switch v := raw.(type) {
		case map[string]any:
			n.payload = v
		case string:
			// Allow JSON string in the editor.
			var parsed map[string]any
			if err := json.Unmarshal([]byte(v), &parsed); err == nil {
				n.payload = parsed
			}
		}
	}

	return n, nil
}

func (n *cronTriggerNode) Type() string { return "cron_trigger" }

func (n *cronTriggerNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

// Run merges the static payload with trigger metadata and outputs on "data".
func (n *cronTriggerNode) Run(_ context.Context, reg *workflow.Registry, _ map[string]any) (workflow.NodeResult, error) {
	data := make(map[string]any)

	// Start with the static payload.
	for k, v := range n.payload {
		data[k] = v
	}

	// Overlay trigger metadata from RunInputs.
	for k, v := range reg.RunInputs {
		data[k] = v
	}

	return workflow.NewResult(map[string]any{
		"data": data,
	}), nil
}
