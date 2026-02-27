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
//	index 0 = "output" — returns the outputs of the called workflow
type workflowCallNode struct {
	workflowID string
	inputs     map[string]any
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
		workflowID: workflowID,
		inputs:     inputs,
	}, nil
}

func (n *workflowCallNode) Type() string { return "workflow_call" }

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

	// 3. Create a child engine.
	// We reuse the lookups from the current registry to share state/config access.
	childEngine := workflow.NewEngine(
		reg.ProviderLookup,
		reg.SkillLookup,
		reg.VarLookup,
		reg.VarLister,
		reg.NodeConfigLookup,
		reg.WorkflowLookup,
	)

	// 4. Run the child workflow.
	// We run it synchronously.
	// Note: We use the active version's graph if available, otherwise the draft graph.
	// Ideally WorkflowLookup would handle versioning, but it returns the Workflow struct.
	// We should probably check ActiveVersion here, similar to how Scheduler/API does it.
	// But `reg` doesn't expose WorkflowVersionStorer.
	// For now, we will run the draft graph `targetWF.Graph`.
	// TODO: Support running specific versions or the active version if we inject VersionLookup.
	// Since we don't have VersionLookup in Registry yet, we'll stick to draft graph for simplicity
	// or assume the user wants the current state of the workflow ID they picked.

	// Determine entry nodes (inputs).
	var entryNodeIDs []string
	for _, node := range targetWF.Graph.Nodes {
		if node.Type == "input" {
			entryNodeIDs = append(entryNodeIDs, node.ID)
		}
	}

	// Channel for early output (sync mode).
	outputCh := make(chan workflow.EarlyOutput, 1)

	// Run in a separate goroutine to handle the channel, but we block waiting for it.
	// Actually, Engine.Run blocks until completion if we don't care about early output,
	// BUT we *do* want the outputs.
	// Engine.Run returns *RunResult which contains the final outputs.
	// So we can just call Engine.Run directly!

	// Wait, Engine.Run executes async fan-out. It blocks until all branches finish.
	// So direct call is fine.

	// Use a timeout? The parent context handles cancellation.
	// Maybe add a configurable timeout in the node config?
	// For now, inherit context.

	result, err := childEngine.Run(ctx, targetWF.Graph, runInputs, entryNodeIDs, outputCh)
	if err != nil {
		return nil, fmt.Errorf("workflow_call: execution failed: %w", err)
	}

	// If an output node fired, we prefer that.
	// Engine.Run returns `result.Outputs` which is collected from Output nodes
	// or terminal nodes (if enabled, but we disabled that).
	// So `result.Outputs` is exactly what we want.

	return workflow.NewResult(result.Outputs), nil
}
