package workflow

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
)

type mappingTestNode struct{ called bool }

func (*mappingTestNode) Type() string                              { return characterizationNodeType }
func (*mappingTestNode) Validate(context.Context, *Registry) error { return nil }
func (*mappingTestNode) Meta() NodeMeta {
	return NodeMeta{Inputs: []PortMeta{{Name: "prompt"}, {Name: "context"}}}
}
func (n *mappingTestNode) Run(_ context.Context, _ *Registry, inputs map[string]any) (NodeResult, error) {
	n.called = true
	return NewResult(map[string]any{"response": inputs["prompt"]}), nil
}

func TestInputMappingPointers(t *testing.T) {
	input := map[string]any{"prompt": map[string]any{
		"customer": map[string]any{"message": "hello", "a/b~c": "escaped", "": "empty key"},
		"items":    []map[string]any{{"name": "first"}}, "null": nil, "false": false, "zero": 0,
	}}
	for _, tt := range []struct {
		name, path string
		want       any
		wantErr    bool
	}{
		{"nested", "/prompt/customer/message", "hello", false},
		{"escaped", "/prompt/customer/a~1b~0c", "escaped", false},
		{"empty key", "/prompt/customer/", "empty key", false},
		{"array", "/prompt/items/0/name", "first", false},
		{"null", "/prompt/null", nil, false},
		{"false", "/prompt/false", false, false},
		{"zero", "/prompt/zero", 0, false},
		{"missing", "/prompt/absent", nil, true},
		{"scalar", "/prompt/zero/name", nil, true},
		{"out of bounds", "/prompt/items/1", nil, true},
		{"negative index", "/prompt/items/-1", nil, true},
		{"noncanonical index", "/prompt/items/00", nil, true},
		{"plus index", "/prompt/items/+0", nil, true},
		{"invalid escape", "/prompt/customer/~2", nil, true},
		{"not a pointer", "prompt.customer", nil, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveInputPointer(input, tt.path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %v", err, tt.wantErr)
			}
			if err == nil && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestInputMappingsReadOriginalInputsAndPreserveGraphJSON(t *testing.T) {
	var config map[string]any
	if err := json.Unmarshal([]byte(`{"input_mappings":{"prompt":"/context","context":"/prompt"}}`), &config); err != nil {
		t.Fatal(err)
	}
	inputs := map[string]any{"prompt": "one", "context": "two"}
	got, err := mapNodeInputs(&mappingTestNode{}, config, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if got["prompt"] != "two" || got["context"] != "one" {
		t.Fatalf("mappings cascaded: %#v", got)
	}
	if inputs["prompt"] != "one" || inputs["context"] != "two" {
		t.Fatal("mapping mutated source input")
	}
	for _, invalid := range []any{"bad", map[string]any{"unknown": "/prompt"}, map[string]any{"prompt": 3}} {
		if _, err := mapNodeInputs(&mappingTestNode{}, map[string]any{"input_mappings": invalid}, inputs); err == nil {
			t.Fatalf("accepted invalid mapping %#v", invalid)
		}
	}
}

func TestEngineMappingBeforeSideEffectsAndCorrelatedSnapshots(t *testing.T) {
	for _, valid := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing path", true: "valid path"}[valid], func(t *testing.T) {
			node := &mappingTestNode{}
			path := "/prompt/customer/message"
			if !valid {
				path = "/prompt/missing"
			}
			st := &nodeState{node: service.WorkflowNode{ID: "llm", Data: map[string]any{"input_mappings": map[string]any{"prompt": path}}}, noder: node}
			engine := NewEngineWithDependencies(Dependencies{})
			events := make(chan NodeEvent, 4)
			engine.SetEventChannel(events)
			customer := map[string]any{"message": "hello"}
			inputs := map[string]any{"prompt": map[string]any{"customer": customer}}
			_, _, err := engine.executeNode(executiontest.Context(t), st, &Registry{}, inputs, func(map[string]any, error) {})
			if (err == nil) != valid || node.called != valid {
				t.Fatalf("err=%v, called=%v, valid=%v", err, node.called, valid)
			}
			started := <-events
			var terminal NodeEvent
			for len(events) > 0 {
				event := <-events
				if event.EventType == "completed" || event.EventType == "error" {
					terminal = event
				}
			}
			if started.ExecutionID == "" || started.ExecutionID != terminal.ExecutionID {
				t.Fatal("invocations are not correlated")
			}
			customer["message"] = "changed"
			got, err := resolveInputPointer(started.Inputs, "/prompt/customer/message")
			if err != nil || got != "hello" {
				t.Fatalf("snapshot aliased live input: %v, %v", got, err)
			}
			if valid && (started.ResolvedInputs["prompt"] != "hello" || terminal.Data["response"] != "hello") {
				t.Fatal("resolved input/output snapshot does not match execution")
			}
		})
	}
}

func TestNodeSnapshotBoundsNestedPayloads(t *testing.T) {
	input := map[string]any{"nested": map[string]any{"value": strings.Repeat("x", nodePreviewMaxBytes)}}
	if snapshot, omitted := snapshotNodeData(input); !omitted || snapshot != nil {
		t.Fatal("oversized nested preview was not omitted")
	}
	if len(input["nested"].(map[string]any)["value"].(string)) != nodePreviewMaxBytes {
		t.Fatal("preview changed execution data")
	}
	if _, omitted := snapshotNodeData(map[string]any{"bad": func() {}}); !omitted {
		t.Fatal("non-JSON preview accepted")
	}
	if snapshot, omitted := snapshotNodeData(map[string]any{"value": strings.Repeat("x", 1000)}); omitted || len(snapshot["value"].(string)) != 1000 {
		t.Fatal("ordinary data was silently truncated")
	}
}
