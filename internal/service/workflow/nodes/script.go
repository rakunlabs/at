package nodes

import (
	"context"
	"fmt"

	"github.com/dop251/goja"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// scriptNode executes arbitrary JavaScript code via Goja and routes the
// output to one of three ports based on the script's return value:
//
//   - If the script returns a truthy value → port index 1 ("true")
//   - If the script returns a falsy value  → port index 0 ("false")
//   - Port index 2 ("always") is always activated
//
// Config (node.Data):
//
//	"code":        string  — JavaScript code to execute (required)
//	"input_count": float64 — number of input ports (default 1, max 10)
//
// When input_count is 1, a single input port "data" is exposed.
// When input_count > 1, named input ports "data1", "data2", ..., "dataN"
// are exposed. Inside the JS runtime, each input is available as a
// variable with its port name (data / data1, data2, ...).
//
// Global helper functions are available via SetupGojaVM:
//
//	toString(v), jsonParse(v), btoa(v), atob(s)
//
// Any io.ReadCloser values in inputs (e.g. HTTP body) are automatically
// wrapped in BodyWrapper with .toString(), .jsonParse(), .toBase64(), .bytes() methods.
//
// Output ports: index 0 = "false", index 1 = "true", index 2 = "always"
//
// Returns NodeResultSelection.
type scriptNode struct {
	code       string
	inputCount int
}

func init() {
	workflow.RegisterNodeType("script", newScriptNode)
}

func newScriptNode(node service.WorkflowNode) (workflow.Noder, error) {
	code, _ := node.Data["code"].(string)

	inputCount := 1
	if c, ok := node.Data["input_count"].(float64); ok && c >= 1 {
		inputCount = int(c)
		if inputCount > 10 {
			inputCount = 10
		}
	}

	return &scriptNode{code: code, inputCount: inputCount}, nil
}

func (n *scriptNode) Type() string { return "script" }

func (n *scriptNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if n.code == "" {
		return fmt.Errorf("script: 'code' is required")
	}
	return nil
}

func (n *scriptNode) Run(_ context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	vm := goja.New()

	// Set up global helpers and wrap io.ReadCloser values (e.g. HTTP body).
	if err := workflow.SetupGojaVM(vm, inputs, reg.VarLookup); err != nil {
		return nil, fmt.Errorf("script: %w", err)
	}

	// Wrap user code in an IIFE so `return` works naturally.
	val, err := vm.RunString("(function(){" + n.code + "})()")
	if err != nil {
		return nil, fmt.Errorf("script: execution error: %w", err)
	}

	// Export the result.
	exported := val.Export()

	outData := make(map[string]any, len(inputs)+1)
	for k, v := range inputs {
		outData[k] = v
	}
	outData["result"] = exported

	// Determine truthiness for port selection.
	truthy := val.ToBoolean()

	// Port 2 ("always") is always active.
	// Port 1 ("true") if truthy, port 0 ("false") if falsy.
	selection := []int{2} // always
	if truthy {
		selection = append(selection, 1)
	} else {
		selection = append(selection, 0)
	}

	return workflow.NewSelectionResult(outData, selection), nil
}
