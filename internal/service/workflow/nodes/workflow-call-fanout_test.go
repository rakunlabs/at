package nodes_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

const childDependencyNodeType = "_workflow_call_child_dependency"

type childDependencyNode struct {
	calls *atomic.Int32
}

func init() {
	service.RegisterExecutionCapability("node", childDependencyNodeType, false)
	workflow.RegisterNodeType(childDependencyNodeType, func(node service.WorkflowNode) (workflow.Noder, error) {
		calls, _ := node.Data["calls"].(*atomic.Int32)
		return &childDependencyNode{calls: calls}, nil
	})
}

func (*childDependencyNode) Type() string { return childDependencyNodeType }

func (*childDependencyNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if reg.ConnectionLookup == nil || reg.WorkflowByNameLookup == nil || reg.WorkflowExecutor == nil || reg.LoopGov == nil {
		return fmt.Errorf("child workflow dependencies are incomplete")
	}
	return nil
}

func (n *childDependencyNode) Run(_ context.Context, reg *workflow.Registry, _ map[string]any) (workflow.NodeResult, error) {
	if n.calls != nil {
		n.calls.Add(1)
	}
	return workflow.NewResult(map[string]any{"output": reg.RunInputs}), nil
}

type childWorkflowLoopGovernor struct{}

func (*childWorkflowLoopGovernor) Limit(_ context.Context, _, _ string, messages []service.Message) ([]service.Message, error) {
	return messages, nil
}

func (*childWorkflowLoopGovernor) LimitWithTools(_ context.Context, _, _ string, messages []service.Message, _ []service.Tool) ([]service.Message, error) {
	return messages, nil
}

func (*childWorkflowLoopGovernor) ClampIterations(agentMax, _ int) int { return agentMax }
func (*childWorkflowLoopGovernor) ChatOptions() *service.ChatOptions   { return nil }
func (*childWorkflowLoopGovernor) TruncateToolResult(_, _, body string) (string, bool) {
	return body, false
}

func TestWorkflowCallInsideFanOutExecutesChildWithInheritedDependencies(t *testing.T) {
	var childCalls atomic.Int32
	child := &service.Workflow{
		ID: "child",
		Graph: service.WorkflowGraph{
			Nodes: []service.WorkflowNode{
				{ID: "child-input", Type: "input", Data: map[string]any{}},
				{ID: "dependency-check", Type: childDependencyNodeType, Data: map[string]any{"calls": &childCalls}},
				{ID: "child-output", Type: "output", Data: map[string]any{}},
			},
			Edges: []service.WorkflowEdge{
				{Source: "child-input", SourceHandle: "data", Target: "dependency-check", TargetHandle: "input"},
				{Source: "dependency-check", SourceHandle: "output", Target: "child-output", TargetHandle: "input"},
			},
		},
	}

	deps := workflow.Dependencies{
		WorkflowLookup: func(_ context.Context, id string) (*service.Workflow, error) {
			if id != child.ID {
				return nil, nil
			}
			return child, nil
		},
		ConnectionLookup: func(context.Context, string) (*service.Connection, error) { return nil, nil },
		WorkflowByNameLookup: func(context.Context, string) (*service.Workflow, error) {
			return nil, nil
		},
		WorkflowExecutor: func(context.Context, *service.Workflow, map[string]any) (string, error) {
			return "", nil
		},
		LoopGov: &childWorkflowLoopGovernor{},
	}
	engine := workflow.NewEngineWithDependencies(deps)
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			{ID: "input", Type: "input", Data: map[string]any{}},
			{ID: "loop", Type: "loop", Data: map[string]any{"expression": "data.items"}},
			{ID: "call-child", Type: "workflow_call", Data: map[string]any{"workflow_id": child.ID}},
			{ID: "output", Type: "output", Data: map[string]any{}},
		},
		Edges: []service.WorkflowEdge{
			{Source: "input", SourceHandle: "data", Target: "loop", TargetHandle: "data"},
			{Source: "loop", SourceHandle: "item", Target: "call-child", TargetHandle: "inputs"},
			{Source: "call-child", SourceHandle: "output", Target: "output", TargetHandle: "input"},
		},
	}

	result, err := engine.Run(executiontest.Context(t), graph, map[string]any{
		"items": []any{map[string]any{"value": "a"}, map[string]any{"value": "b"}},
	}, []string{"input"}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if childCalls.Load() != 2 {
		t.Fatalf("child calls = %d, want one per fan-out item", childCalls.Load())
	}

	childOutput, ok := result.Outputs["input"].(map[string]any)
	if !ok {
		t.Fatalf("outputs = %#v, want workflow_call output on declared output port", result.Outputs)
	}
	childInput, ok := childOutput["input"].(map[string]any)
	if !ok || childInput["value"] != "b" {
		t.Fatalf("outputs = %#v, want deterministic last child input b", result.Outputs)
	}
}
