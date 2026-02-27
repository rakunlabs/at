package nodes

import (
	"context"
	"fmt"

	"github.com/dop251/goja"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// conditionalNode evaluates a JavaScript expression against the input data
// and routes to either the "true" or "false" output port.
//
// Config (node.Data):
//
//	"expression": string — JS expression that evaluates to a boolean (required)
//	                       e.g. "data.score > 0.8", "data.items.length > 0"
//
// Input ports:  "data" — upstream data exposed as `data` in JS
// Output ports: index 0 = "false", index 1 = "true"
//
// Returns NodeResultSelection with selection [0] for false, [1] for true.
type conditionalNode struct {
	expression string
}

func init() {
	workflow.RegisterNodeType("conditional", newConditionalNode)
}

func newConditionalNode(node service.WorkflowNode) (workflow.Noder, error) {
	expr, _ := node.Data["expression"].(string)

	return &conditionalNode{expression: expr}, nil
}

func (n *conditionalNode) Type() string { return "conditional" }

func (n *conditionalNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if n.expression == "" {
		return fmt.Errorf("conditional: 'expression' is required")
	}
	return nil
}

func (n *conditionalNode) Run(_ context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	vm := goja.New()

	// Set up global helpers and wrap io.ReadCloser values (e.g. HTTP body).
	if err := workflow.SetupGojaVM(vm, inputs, reg.VarLookup); err != nil {
		return nil, fmt.Errorf("conditional: %w", err)
	}

	val, err := vm.RunString(n.expression)
	if err != nil {
		return nil, fmt.Errorf("conditional: expression error: %w", err)
	}

	result := val.ToBoolean()

	// Output data carries a "result" field plus all inputs passed through.
	outData := make(map[string]any, len(inputs)+1)
	for k, v := range inputs {
		outData[k] = v
	}
	outData["result"] = result

	// Selection by port name: "true" or "false".
	var selection []string
	if result {
		selection = []string{"true"}
	} else {
		selection = []string{"false"}
	}

	return workflow.NewSelectionResult(outData, selection), nil
}
