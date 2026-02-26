package nodes

import (
	"context"
	"fmt"

	"github.com/dop251/goja"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// loopNode iterates over an array from the input data and fans out each
// item as a separate downstream branch. Uses Goja JS to extract the
// iterable from the input data.
//
// Config (node.Data):
//
//	"expression": string — JS expression returning an array (required)
//	                       e.g. "data.items", "data.results.filter(r => r.active)"
//
// Input ports:  "data" — upstream data exposed as `data` in JS
// Output ports: port 0 — each fan-out item is sent here
//
// Returns NodeResultFanOut. If the expression evaluates to an empty array
// or a non-array value, the branch stops via ErrStopBranch.
type loopNode struct {
	expression string
}

func init() {
	workflow.RegisterNodeType("loop", newLoopNode)
}

func newLoopNode(node service.WorkflowNode) (workflow.Noder, error) {
	expr, _ := node.Data["expression"].(string)

	return &loopNode{expression: expr}, nil
}

func (n *loopNode) Type() string { return "loop" }

func (n *loopNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if n.expression == "" {
		return fmt.Errorf("loop: 'expression' is required")
	}
	return nil
}

func (n *loopNode) Run(_ context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	vm := goja.New()

	// Set up global helpers and wrap io.ReadCloser values (e.g. HTTP body).
	if err := workflow.SetupGojaVM(vm, inputs, reg.SecretLookup); err != nil {
		return nil, fmt.Errorf("loop: %w", err)
	}

	val, err := vm.RunString(n.expression)
	if err != nil {
		return nil, fmt.Errorf("loop: expression error: %w", err)
	}

	// Export the JS value back to Go.
	exported := val.Export()
	if exported == nil {
		return nil, workflow.ErrStopBranch
	}

	// Convert to []map[string]any for fan-out items.
	var items []map[string]any

	switch v := exported.(type) {
	case []any:
		for i, item := range v {
			m := toMap(item, i)
			items = append(items, m)
		}
	case []map[string]any:
		items = v
	default:
		// If it's a single value, treat as a single-item loop.
		items = []map[string]any{{"item": exported, "index": 0}}
	}

	if len(items) == 0 {
		return nil, workflow.ErrStopBranch
	}

	return workflow.NewFanOutResult(items), nil
}

// toMap wraps a value as a map with "item" and "index" keys.
// If the value is already a map[string]any, it adds the index to it.
func toMap(v any, index int) map[string]any {
	if m, ok := v.(map[string]any); ok {
		m["index"] = index
		return m
	}
	return map[string]any{"item": v, "index": index}
}
