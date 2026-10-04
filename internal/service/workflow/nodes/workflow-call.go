package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// workflowCallNode calls another workflow synchronously.
//
// Config (node.Data):
//
//	"workflow_id": string — ID of the workflow to call (required)
//	"inputs":      map[string]any — static inputs (optional)
//
// Input ports:
//
//	"inputs" — dynamic inputs (merged on top of static inputs)
//
// Output ports:
//
//	"output" — the outputs of the called workflow (whole map)
//	"files"  — every file reference found in those outputs ([] when none)
//	<field>  — one port per name in "output_fields" (the child Output node's
//	           named fields), carrying that output key alone, so text and a
//	           file can be wired to different steps
//
// Child steps share the parent's run workspace, so file references a child
// returns stay valid in the parent.
type workflowCallNode struct {
	workflowID   string
	inputs       map[string]any
	outputFields []string
}

func init() {
	workflow.RegisterNodeType("workflow_call", newWorkflowCallNode)
}

func newWorkflowCallNode(node service.WorkflowNode) (workflow.Noder, error) {
	workflowID, _ := node.Data["workflow_id"].(string)
	inputs := make(map[string]any)
	if m, ok := node.Data["inputs"].(map[string]any); ok {
		for k, v := range m {
			inputs[k] = v
		}
	}

	return &workflowCallNode{
		workflowID:   workflowID,
		inputs:       inputs,
		outputFields: outputFieldNames(node.Data["output_fields"]),
	}, nil
}

func (n *workflowCallNode) Type() string { return "workflow_call" }

func (n *workflowCallNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{
		Type:        "workflow_call",
		Label:       "Workflow Call",
		Category:    "processing",
		Description: "Call another workflow synchronously",
		Inputs: []workflow.PortMeta{
			{Name: "inputs", Type: workflow.PortTypeData, Label: "Inputs", Position: "left"},
		},
		Outputs: n.outputPorts(),
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "workflow_id", Type: "string", Required: true, Description: "Child workflow ID"},
			{Name: "workflow_name", Type: "string", Description: "Display name of the child workflow"},
			{Name: "inputs", Type: "object", Description: "Static inputs for child workflow"},
			{Name: "output_fields", Type: "array", Description: "Child output fields exposed as separate output ports (copied from the child's Output node)"},
		},
		Color: "fuchsia",
	}
}

func (n *workflowCallNode) outputPorts() []workflow.PortMeta {
	ports := []workflow.PortMeta{
		{Name: "output", Type: workflow.PortTypeData, Label: "Output", Position: "right"},
		{Name: "files", Type: workflow.PortTypeData, Label: "Files", Position: "right"},
	}
	for _, field := range n.outputFields {
		ports = append(ports, workflow.PortMeta{Name: field, Type: workflow.PortTypeData, Label: field, Position: "right"})
	}
	return ports
}

func (n *workflowCallNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.workflowID == "" {
		return fmt.Errorf("workflow_call: 'workflow_id' is required")
	}
	if reg.WorkflowLookup == nil {
		return fmt.Errorf("workflow_call: workflow lookup not available")
	}
	return nil
}

func (n *workflowCallNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// 1. Fetch the target workflow.
	targetWF, err := reg.WorkflowLookup(ctx, n.workflowID)
	if err != nil {
		return nil, fmt.Errorf("workflow_call: lookup %q: %w", n.workflowID, err)
	}
	if targetWF == nil {
		return nil, fmt.Errorf("workflow_call: workflow %q not found", n.workflowID)
	}

	// 2. Prepare inputs.
	// Merge static config inputs with dynamic port inputs.
	runInputs := make(map[string]any)
	for k, v := range n.inputs {
		runInputs[k] = v
	}
	// "inputs" port data overrides static config.
	if dynamicInputs, ok := inputs["inputs"].(map[string]any); ok {
		for k, v := range dynamicInputs {
			runInputs[k] = v
		}
	} else {
		// If "inputs" port brings non-map data, maybe treat it as a single value?
		// For now, let's assume specific fields are mapped or "inputs" is a map.
		// Or if the upstream just dumped everything into "inputs", merge it.
		for k, v := range inputs {
			if k != "inputs" {
				runInputs[k] = v
			}
		}
	}

	// 3. Create a child engine with the parent's complete dependency set.
	childEngine := reg.NewChildEngine()

	// 4. Run the child workflow.
	// Use the active version's graph if available, otherwise fall back to the draft graph.
	graphToRun := targetWF.Graph
	if targetWF.ActiveVersion != nil && reg.VersionLookup != nil {
		activeGraph, err := reg.VersionLookup(ctx, n.workflowID)
		if err != nil {
			// Log but fall back to draft graph on error.
			_ = err
		} else if activeGraph != nil {
			graphToRun = *activeGraph
		}
	}

	// Determine entry nodes (inputs).
	var entryNodeIDs []string
	for _, node := range graphToRun.Nodes {
		if node.Type == "input" {
			entryNodeIDs = append(entryNodeIDs, node.ID)
		}
	}

	result, err := childEngine.Run(ctx, graphToRun, runInputs, entryNodeIDs, nil)
	if err != nil {
		return nil, fmt.Errorf("workflow_call: execution failed: %w", err)
	}

	out := map[string]any{"output": result.Outputs}
	refs := workflow.CollectFileRefs(result.Outputs, maxRunOutputFiles)
	files := make([]any, len(refs))
	for i := range refs {
		files[i] = refs[i]
	}
	out["files"] = files
	for _, field := range n.outputFields {
		// A field the child did not produce stays absent rather than null.
		if value, ok := result.Outputs[field]; ok {
			out[field] = value
		}
	}
	return workflow.NewResult(out), nil
}
