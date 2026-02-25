package nodes

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// httpTriggerNode is functionally identical to inputNode — it passes the
// workflow's run-time inputs (HTTP request body) downstream on the "data"
// port. It exists as a separate type so the visual editor can render it
// with an HTTP-specific badge and the graph clearly shows the trigger origin.
type httpTriggerNode struct{}

func init() {
	workflow.RegisterNodeType("http_trigger", newHTTPTriggerNode)
}

func newHTTPTriggerNode(_ service.WorkflowNode) (workflow.Noder, error) {
	return &httpTriggerNode{}, nil
}

func (n *httpTriggerNode) Type() string { return "http_trigger" }

func (n *httpTriggerNode) Validate(_ context.Context, _ *workflow.Registry) error {
	return nil
}

// Run outputs the original workflow trigger inputs (HTTP request body) on the "data" port.
func (n *httpTriggerNode) Run(_ context.Context, reg *workflow.Registry, _ map[string]any) (workflow.NodeResult, error) {
	return workflow.NewResult(map[string]any{
		"data": reg.RunInputs,
	}), nil
}
