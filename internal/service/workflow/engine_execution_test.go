package workflow

import (
	"context"
	"sync"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

const characterizationNodeType = "_engine_characterization"

type characterizationRecorder struct {
	mu     sync.Mutex
	inputs map[string][]map[string]any
}

func (r *characterizationRecorder) record(nodeID string, inputs map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inputs == nil {
		r.inputs = make(map[string][]map[string]any)
	}
	cloned := make(map[string]any, len(inputs))
	for key, value := range inputs {
		cloned[key] = value
	}
	r.inputs[nodeID] = append(r.inputs[nodeID], cloned)
}

func (r *characterizationRecorder) calls(nodeID string) []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.inputs[nodeID]...)
}

type characterizationNode struct {
	node     service.WorkflowNode
	behavior string
	recorder *characterizationRecorder
}

func init() {
	service.RegisterExecutionCapability("node", characterizationNodeType, false)
	RegisterNodeType(characterizationNodeType, func(node service.WorkflowNode) (Noder, error) {
		recorder, _ := node.Data["recorder"].(*characterizationRecorder)
		behavior, _ := node.Data["behavior"].(string)
		return &characterizationNode{node: node, behavior: behavior, recorder: recorder}, nil
	})
}

func (n *characterizationNode) Type() string {
	if n.behavior == "output" {
		return "output"
	}
	return characterizationNodeType
}

func (n *characterizationNode) Validate(context.Context, *Registry) error { return nil }

func (n *characterizationNode) Run(_ context.Context, reg *Registry, inputs map[string]any) (NodeResult, error) {
	if n.recorder != nil {
		n.recorder.record(n.node.ID, inputs)
	}

	switch n.behavior {
	case "source":
		return NewResult(map[string]any{"output": n.node.Data["value"]}), nil
	case "select":
		selection, _ := n.node.Data["selection"].([]string)
		return NewSelectionResult(map[string]any{"value": n.node.Data["value"]}, selection), nil
	case "stop":
		return nil, ErrStopBranch
	case "fan_out":
		items, _ := n.node.Data["items"].([]map[string]any)
		return NewFanOutResult(items), nil
	case "fan_out_from_input":
		input, _ := inputs["input"].(map[string]any)
		outer, _ := input["outer"].(string)
		return NewFanOutResult([]map[string]any{
			{"value": outer + "1"},
			{"value": outer + "2"},
		}), nil
	case "output":
		value := inputs["input"]
		reg.SetOutputs(map[string]any{"value": value})
		return NewResult(map[string]any{"value": value}), nil
	default:
		value := any(inputs)
		if input, ok := inputs["input"]; ok && len(inputs) == 1 {
			value = input
		}
		return NewResult(map[string]any{"output": value}), nil
	}
}

func characterizationWorkflowNode(id, behavior string, recorder *characterizationRecorder, data map[string]any) service.WorkflowNode {
	if data == nil {
		data = make(map[string]any)
	}
	data["behavior"] = behavior
	data["recorder"] = recorder
	return service.WorkflowNode{ID: id, Type: characterizationNodeType, Data: data}
}

func runCharacterizationGraph(t *testing.T, graph service.WorkflowGraph, entry string) (*RunResult, []NodeEvent) {
	t.Helper()
	engine := NewEngineWithDependencies(Dependencies{})
	eventCh := make(chan NodeEvent, 128)
	engine.SetEventChannel(eventCh)
	result, err := engine.Run(executiontest.Context(t), graph, nil, []string{entry}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	events := make([]NodeEvent, 0, len(eventCh))
	for len(eventCh) > 0 {
		events = append(events, <-eventCh)
	}
	return result, events
}

func countNodeEvents(events []NodeEvent, nodeID, eventType string) int {
	count := 0
	for _, event := range events {
		if event.NodeID == nodeID && event.EventType == eventType {
			count++
		}
	}
	return count
}

func TestEngineConditionalInactiveBranchAndDescendantsDoNotRun(t *testing.T) {
	recorder := &characterizationRecorder{}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": true}),
			characterizationWorkflowNode("condition", "select", recorder, map[string]any{"selection": []string{"true"}, "value": "chosen"}),
			characterizationWorkflowNode("active", "pass", recorder, nil),
			characterizationWorkflowNode("inactive", "pass", recorder, nil),
			characterizationWorkflowNode("inactive-descendant", "pass", recorder, nil),
			characterizationWorkflowNode("join", "pass", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "condition", TargetHandle: "input"},
			{Source: "condition", SourceHandle: "true", Target: "active", TargetHandle: "input"},
			{Source: "condition", SourceHandle: "false", Target: "inactive", TargetHandle: "input"},
			{Source: "inactive", SourceHandle: "output", Target: "inactive-descendant", TargetHandle: "input"},
			{Source: "active", SourceHandle: "output", Target: "join", TargetHandle: "active"},
			{Source: "inactive-descendant", SourceHandle: "output", Target: "join", TargetHandle: "inactive"},
		},
	}

	_, events := runCharacterizationGraph(t, graph, "root")
	if len(recorder.calls("active")) != 1 || len(recorder.calls("join")) != 1 {
		t.Fatalf("active calls = %d, join calls = %d; want one each", len(recorder.calls("active")), len(recorder.calls("join")))
	}
	if len(recorder.calls("inactive")) != 0 || len(recorder.calls("inactive-descendant")) != 0 {
		t.Fatalf("inactive calls = %d, descendant calls = %d; want zero", len(recorder.calls("inactive")), len(recorder.calls("inactive-descendant")))
	}
	if countNodeEvents(events, "inactive", "skipped") != 1 || countNodeEvents(events, "inactive-descendant", "skipped") != 1 {
		t.Fatalf("inactive branch did not emit one skipped event per node: %#v", events)
	}
}

func TestEngineErrStopBranchDeactivatesDescendants(t *testing.T) {
	recorder := &characterizationRecorder{}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": "go"}),
			characterizationWorkflowNode("stop", "stop", recorder, nil),
			characterizationWorkflowNode("descendant", "pass", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "stop", TargetHandle: "input"},
			{Source: "stop", SourceHandle: "output", Target: "descendant", TargetHandle: "input"},
		},
	}

	_, events := runCharacterizationGraph(t, graph, "root")
	if len(recorder.calls("stop")) != 1 || len(recorder.calls("descendant")) != 0 {
		t.Fatalf("stop calls = %d, descendant calls = %d", len(recorder.calls("stop")), len(recorder.calls("descendant")))
	}
	if countNodeEvents(events, "stop", "started") != 1 || countNodeEvents(events, "stop", "skipped") != 1 {
		t.Fatalf("stop node events = %#v", events)
	}
	if countNodeEvents(events, "descendant", "skipped") != 1 {
		t.Fatalf("descendant events = %#v", events)
	}
}

func TestEngineEmptyFanOutDoesNotActivateDownstream(t *testing.T) {
	recorder := &characterizationRecorder{}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": "go"}),
			characterizationWorkflowNode("fan", "fan_out", recorder, map[string]any{"items": []map[string]any{}}),
			characterizationWorkflowNode("downstream", "pass", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "fan", TargetHandle: "input"},
			{Source: "fan", SourceHandle: "item", Target: "downstream", TargetHandle: "input"},
		},
	}

	_, events := runCharacterizationGraph(t, graph, "root")
	if len(recorder.calls("fan")) != 1 || len(recorder.calls("downstream")) != 0 {
		t.Fatalf("fan calls = %d, downstream calls = %d", len(recorder.calls("fan")), len(recorder.calls("downstream")))
	}
	if countNodeEvents(events, "fan", "completed") != 1 || countNodeEvents(events, "downstream", "skipped") != 1 {
		t.Fatalf("empty fan-out events = %#v", events)
	}
}

func TestEngineFanOutExecutesDownstreamOncePerItemWithOutputsAndEvents(t *testing.T) {
	recorder := &characterizationRecorder{}
	items := []map[string]any{{"value": "a"}, {"value": "b"}, {"value": "c"}}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": "go"}),
			characterizationWorkflowNode("fan", "fan_out", recorder, map[string]any{"items": items}),
			characterizationWorkflowNode("worker", "pass", recorder, nil),
			characterizationWorkflowNode("result", "output", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "fan", TargetHandle: "input"},
			{Source: "fan", SourceHandle: "item", Target: "worker", TargetHandle: "input"},
			{Source: "worker", SourceHandle: "output", Target: "result", TargetHandle: "input"},
		},
	}

	result, events := runCharacterizationGraph(t, graph, "root")
	if len(recorder.calls("worker")) != len(items) || len(recorder.calls("result")) != len(items) {
		t.Fatalf("worker calls = %d, output calls = %d; want %d", len(recorder.calls("worker")), len(recorder.calls("result")), len(items))
	}
	if got := result.Outputs["value"]; got == nil || got.(map[string]any)["value"] != "c" {
		t.Fatalf("outputs = %#v, want deterministic last-item value c", result.Outputs)
	}
	for _, nodeID := range []string{"worker", "result"} {
		if countNodeEvents(events, nodeID, "started") != len(items) || countNodeEvents(events, nodeID, "completed") != len(items) {
			t.Fatalf("events for %s = %#v", nodeID, events)
		}
	}
}

func TestEngineJoinRunsOnceWithAllActivePredecessors(t *testing.T) {
	recorder := &characterizationRecorder{}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": "shared"}),
			characterizationWorkflowNode("left", "pass", recorder, nil),
			characterizationWorkflowNode("right", "pass", recorder, nil),
			characterizationWorkflowNode("join", "pass", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "left", TargetHandle: "input"},
			{Source: "root", SourceHandle: "output", Target: "right", TargetHandle: "input"},
			{Source: "left", SourceHandle: "output", Target: "join", TargetHandle: "left"},
			{Source: "right", SourceHandle: "output", Target: "join", TargetHandle: "right"},
		},
	}

	_, _ = runCharacterizationGraph(t, graph, "root")
	joinCalls := recorder.calls("join")
	if len(joinCalls) != 1 {
		t.Fatalf("join calls = %d, want 1", len(joinCalls))
	}
	if joinCalls[0]["left"] != "shared" || joinCalls[0]["right"] != "shared" {
		t.Fatalf("join inputs = %#v, want both predecessors", joinCalls[0])
	}
}

func TestEngineNestedFanOutsExecuteCartesianBranchesAndMergeDeterministically(t *testing.T) {
	recorder := &characterizationRecorder{}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": "go"}),
			characterizationWorkflowNode("outer-fan", "fan_out", recorder, map[string]any{
				"items": []map[string]any{{"outer": "a"}, {"outer": "b"}},
			}),
			characterizationWorkflowNode("inner-fan", "fan_out_from_input", recorder, nil),
			characterizationWorkflowNode("worker", "pass", recorder, nil),
			characterizationWorkflowNode("result", "output", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "outer-fan", TargetHandle: "input"},
			{Source: "outer-fan", SourceHandle: "item", Target: "inner-fan", TargetHandle: "input"},
			{Source: "inner-fan", SourceHandle: "item", Target: "worker", TargetHandle: "input"},
			{Source: "worker", SourceHandle: "output", Target: "result", TargetHandle: "input"},
		},
	}

	result, events := runCharacterizationGraph(t, graph, "root")
	if len(recorder.calls("inner-fan")) != 2 || len(recorder.calls("worker")) != 4 || len(recorder.calls("result")) != 4 {
		t.Fatalf("inner fan calls = %d, worker calls = %d, output calls = %d; want 2, 4, 4", len(recorder.calls("inner-fan")), len(recorder.calls("worker")), len(recorder.calls("result")))
	}
	if got := result.Outputs["value"]; got == nil || got.(map[string]any)["value"] != "b2" {
		t.Fatalf("outputs = %#v, want outer-last/inner-last value b2", result.Outputs)
	}
	if countNodeEvents(events, "worker", "started") != 4 || countNodeEvents(events, "worker", "completed") != 4 {
		t.Fatalf("worker events = %#v", events)
	}
}

func TestEngineIndependentFanOutsConvergeWithORJoinSemantics(t *testing.T) {
	recorder := &characterizationRecorder{}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": "go"}),
			characterizationWorkflowNode("fan-a", "fan_out", recorder, map[string]any{
				"items": []map[string]any{{"value": "a1"}, {"value": "a2"}},
			}),
			characterizationWorkflowNode("fan-b", "fan_out", recorder, map[string]any{
				"items": []map[string]any{{"value": "b1"}, {"value": "b2"}},
			}),
			characterizationWorkflowNode("join", "pass", recorder, nil),
			characterizationWorkflowNode("result", "output", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "fan-a", TargetHandle: "input"},
			{Source: "root", SourceHandle: "output", Target: "fan-b", TargetHandle: "input"},
			{Source: "fan-a", SourceHandle: "item", Target: "join", TargetHandle: "left"},
			{Source: "fan-b", SourceHandle: "item", Target: "join", TargetHandle: "right"},
			{Source: "join", SourceHandle: "output", Target: "result", TargetHandle: "input"},
		},
	}

	result, _ := runCharacterizationGraph(t, graph, "root")
	joinCalls := recorder.calls("join")
	if len(joinCalls) != 4 {
		t.Fatalf("join calls = %d, want one per item across both fan-outs", len(joinCalls))
	}
	for _, inputs := range joinCalls {
		_, left := inputs["left"]
		_, right := inputs["right"]
		if left == right {
			t.Fatalf("join inputs = %#v, want exactly one active fan-out predecessor", inputs)
		}
	}
	got, ok := result.Outputs["value"].(map[string]any)
	if !ok || got["right"].(map[string]any)["value"] != "b2" {
		t.Fatalf("outputs = %#v, want fan-b item b2 from deterministic source/item merge", result.Outputs)
	}
}

func TestEngineFanOutBranchIncludesIndependentPrerequisite(t *testing.T) {
	recorder := &characterizationRecorder{}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			characterizationWorkflowNode("root", "source", recorder, map[string]any{"value": "shared"}),
			characterizationWorkflowNode("a-fan", "fan_out", recorder, map[string]any{
				"items": []map[string]any{{"value": "a"}, {"value": "b"}},
			}),
			characterizationWorkflowNode("z-prerequisite", "pass", recorder, nil),
			characterizationWorkflowNode("join", "pass", recorder, nil),
			characterizationWorkflowNode("result", "output", recorder, nil),
		},
		Edges: []service.WorkflowEdge{
			{Source: "root", SourceHandle: "output", Target: "a-fan", TargetHandle: "input"},
			{Source: "root", SourceHandle: "output", Target: "z-prerequisite", TargetHandle: "input"},
			{Source: "a-fan", SourceHandle: "item", Target: "join", TargetHandle: "item"},
			{Source: "z-prerequisite", SourceHandle: "output", Target: "join", TargetHandle: "prerequisite"},
			{Source: "join", SourceHandle: "output", Target: "result", TargetHandle: "input"},
		},
	}

	result, _ := runCharacterizationGraph(t, graph, "root")
	joinCalls := recorder.calls("join")
	if len(joinCalls) != 2 {
		t.Fatalf("join calls = %d, want one per fan-out item", len(joinCalls))
	}
	for _, inputs := range joinCalls {
		if inputs["prerequisite"] != "shared" {
			t.Fatalf("join inputs = %#v, missing independent prerequisite", inputs)
		}
	}
	got, ok := result.Outputs["value"].(map[string]any)
	if !ok || got["item"].(map[string]any)["value"] != "b" || got["prerequisite"] != "shared" {
		t.Fatalf("outputs = %#v, want last item b plus prerequisite", result.Outputs)
	}
}

type characterizationLoopGovernor struct{}

func (*characterizationLoopGovernor) Limit(context.Context, string, string, []service.Message) ([]service.Message, error) {
	return nil, nil
}

func (*characterizationLoopGovernor) LimitWithTools(context.Context, string, string, []service.Message, []service.Tool) ([]service.Message, error) {
	return nil, nil
}

func (*characterizationLoopGovernor) ClampIterations(int, int) int      { return 1 }
func (*characterizationLoopGovernor) ChatOptions() *service.ChatOptions { return nil }
func (*characterizationLoopGovernor) TruncateToolResult(string, string, string) (string, bool) {
	return "", false
}

func TestEngineNewChildCopiesCompleteDependencies(t *testing.T) {
	gov := &characterizationLoopGovernor{}
	deps := Dependencies{
		ProviderLookup: func(string) (service.LLMProvider, string, error) { return nil, "", nil },
		ConnectionLookup: func(context.Context, string) (*service.Connection, error) {
			return &service.Connection{ID: "connection"}, nil
		},
		WorkflowByNameLookup: func(context.Context, string) (*service.Workflow, error) {
			return &service.Workflow{ID: "workflow"}, nil
		},
		WorkflowExecutor: func(context.Context, *service.Workflow, map[string]any) (string, error) {
			return "executed", nil
		},
		BuiltinToolDefs: []BuiltinToolDef{{Name: "tool"}},
		LoopGov:         gov,
	}
	parent := NewEngineWithDependencies(deps)
	reg := NewRegistryWithDependencies(parent.dependencies, nil)
	reg.engine = parent
	child := reg.NewChildEngine()

	if reg.Dependencies != parent.dependencies {
		t.Fatal("registry and parent engine do not share dependencies")
	}
	if child.dependencies == parent.dependencies {
		t.Fatal("child engine should receive a complete dependency copy")
	}
	if child.dependencies.ProviderLookup == nil || child.dependencies.ConnectionLookup == nil || child.dependencies.WorkflowByNameLookup == nil || child.dependencies.WorkflowExecutor == nil {
		t.Fatalf("child dependencies are incomplete: %#v", child.dependencies)
	}
	if child.dependencies.LoopGov != gov || len(child.dependencies.BuiltinToolDefs) != 1 || child.dependencies.BuiltinToolDefs[0].Name != "tool" {
		t.Fatalf("child dependencies were not copied: %#v", child.dependencies)
	}
}
