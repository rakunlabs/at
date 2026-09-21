package nodes_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func runDataNode(t *testing.T, kind string, config, inputs map[string]any) workflow.NodeResult {
	t.Helper()
	n := makeNode(t, kind, config)
	if err := n.Validate(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	result, err := n.Run(t.Context(), nil, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertDataJSON(t *testing.T, value any, expected string) {
	t.Helper()
	actual, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(actual, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("got %s, want %s", actual, expected)
	}
}

func TestEditFieldsTypedValuesOriginalPathsAndIsolation(t *testing.T) {
	input := map[string]any{"a": "one", "b": "two", "keep": false, "nested": map[string]any{"a/b~c": "escaped"}}
	before, _ := json.Marshal(input)
	fields := []any{
		map[string]any{"name": "a", "source": "path", "path": "/b"},
		map[string]any{"name": "b", "source": "path", "path": "/a"},
		map[string]any{"name": "escaped", "source": "path", "path": "/nested/a~1b~0c"},
		map[string]any{"name": "zero", "source": "value", "value_type": "number", "value": "0"},
		map[string]any{"name": "flag", "source": "value", "value_type": "boolean", "value": "false"},
		map[string]any{"name": "nothing", "source": "value", "value_type": "null", "value": ""},
		map[string]any{"name": "object", "source": "value", "value_type": "json", "value": `{"key":1}`},
	}
	result := runDataNode(t, "edit_fields", map[string]any{"keep_input": false, "fields": fields}, map[string]any{"data": []any{input, input}})
	rows := result.Data()["data"].([]any)
	assertDataJSON(t, rows[0], `{"a":"two","b":"one","escaped":"escaped","zero":0,"flag":false,"nothing":null,"object":{"key":1}}`)
	rows[0].(map[string]any)["object"].(map[string]any)["key"] = 99
	assertDataJSON(t, rows[1].(map[string]any)["object"], `{"key":1}`)
	after, _ := json.Marshal(input)
	if string(before) != string(after) {
		t.Fatal("input was mutated")
	}
	result = runDataNode(t, "edit_fields", map[string]any{}, map[string]any{"data": input})
	assertDataJSON(t, result.Data()["data"], string(before))
	result = runDataNode(t, "edit_fields", map[string]any{}, map[string]any{"data": []any{}})
	assertDataJSON(t, result.Data()["data"], `[]`)
}

func TestFilterMissingNullTypesAndBooleanCombinations(t *testing.T) {
	items := []any{map[string]any{"v": nil}, map[string]any{}, map[string]any{"v": false}, map[string]any{"v": 0}, map[string]any{"v": "0"}, map[string]any{"v": ""}, map[string]any{"v": []any{}}, map[string]any{"v": 2}}
	for _, tt := range []struct {
		name, operator, kind, value string
		count                       int
	}{
		{"missing", "not_exists", "", "", 1}, {"exists includes null", "exists", "", "", 7},
		{"null", "eq", "null", "", 1}, {"false", "eq", "boolean", "false", 1},
		{"number", "eq", "number", "0", 1}, {"text", "eq", "string", "0", 1},
		{"empty", "is_empty", "", "", 3}, {"numeric greater", "gt", "number", "1", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			condition := map[string]any{"path": "/v", "operator": tt.operator, "value_type": tt.kind, "value": tt.value}
			result := runDataNode(t, "filter", map[string]any{"conditions": []any{condition}}, map[string]any{"data": items})
			if got := len(result.Data()["data"].([]any)); got != tt.count {
				t.Fatalf("got %d items, want %d", got, tt.count)
			}
		})
	}
	conditions := []any{map[string]any{"path": "/v", "operator": "eq", "value_type": "number", "value": "0"}, map[string]any{"path": "/v", "operator": "eq", "value_type": "number", "value": "2"}}
	anyResult := runDataNode(t, "filter", map[string]any{"match": "any", "conditions": conditions}, map[string]any{"data": items})
	if len(anyResult.Data()["data"].([]any)) != 2 {
		t.Fatal("OR conditions did not match both numbers")
	}
	allResult := runDataNode(t, "filter", map[string]any{"match": "all", "conditions": conditions}, map[string]any{"data": items})
	assertDataJSON(t, allResult.Data()["data"], `[]`)
}

func TestSwitchFirstAllFallbackAndStableIDs(t *testing.T) {
	rules := []any{map[string]any{"id": "case_a", "path": "/amount", "operator": "gt", "value_type": "number", "value": "5"}, map[string]any{"id": "case_b", "path": "/amount", "operator": "gte", "value_type": "number", "value": "10"}}
	input := map[string]any{"amount": 10, "message": "original"}
	for _, tt := range []struct {
		mode     string
		selected []string
	}{{"first", []string{"case_a"}}, {"all", []string{"case_a", "case_b"}}} {
		result := runDataNode(t, "switch", map[string]any{"match_mode": tt.mode, "rules": rules}, map[string]any{"data": input})
		if !reflect.DeepEqual(result.(workflow.NodeResultSelection).Selection(), tt.selected) {
			t.Fatal("wrong selected ports")
		}
		for _, port := range tt.selected {
			assertDataJSON(t, result.Data()[port], `{"amount":10,"message":"original"}`)
		}
	}
	result := runDataNode(t, "switch", map[string]any{"rules": rules}, map[string]any{"data": false})
	if result.Data()["fallback"] != false {
		t.Fatal("fallback lost a falsy payload")
	}
	result = runDataNode(t, "switch", map[string]any{"rules": []any{rules[1], rules[0]}}, map[string]any{"data": input})
	if got := result.(workflow.NodeResultSelection).Selection(); !reflect.DeepEqual(got, []string{"case_b"}) {
		t.Fatalf("reordering changed IDs: %v", got)
	}
}

func TestMergeModesPreserveOrderTypesAndUnmatchedRows(t *testing.T) {
	appendResult := runDataNode(t, "merge", nil, map[string]any{"left": []any{1, 2}, "right": nil})
	assertDataJSON(t, appendResult.Data()["data"], `[1,2,null]`)
	zip := runDataNode(t, "merge", map[string]any{"mode": "zip"}, map[string]any{"left": []any{1}, "right": []any{"a", "b"}})
	assertDataJSON(t, zip.Data()["data"], `[{"left":1,"right":"a"},{"left":null,"right":"b"}]`)
	left := []any{map[string]any{"id": 1, "v": "l1"}, map[string]any{"id": 1, "v": "l2"}, map[string]any{"id": nil}, map[string]any{"missing": true}}
	right := []any{map[string]any{"id": 1, "v": "r1"}, map[string]any{"id": 1, "v": "r2"}, map[string]any{"id": "1"}, map[string]any{"id": nil}}
	for _, tt := range []struct {
		join  string
		count int
	}{{"inner", 4}, {"left", 6}, {"outer", 8}} {
		result := runDataNode(t, "merge", map[string]any{"mode": "join", "join_type": tt.join}, map[string]any{"left": left, "right": right})
		rows := result.Data()["data"].([]any)
		if len(rows) != tt.count {
			t.Fatalf("%s: got %d rows", tt.join, len(rows))
		}
		assertDataJSON(t, rows[0], `{"left":{"id":1,"v":"l1"},"right":{"id":1,"v":"r1"}}`)
		assertDataJSON(t, rows[3], `{"left":{"id":1,"v":"l2"},"right":{"id":1,"v":"r2"}}`)
	}
	empty := runDataNode(t, "merge", nil, map[string]any{"right": []any{}})
	assertDataJSON(t, empty.Data()["data"], `[]`)
}

func TestAggregateOperationsAndEmptyArrays(t *testing.T) {
	for _, tt := range []struct{ op, expected, empty string }{
		{"collect", `{"value":[10,20],"count":2}`, `{"value":[],"count":0}`},
		{"count", `{"value":2,"count":2}`, `{"value":0,"count":0}`},
		{"sum", `{"value":30,"count":2}`, `{"value":0,"count":0}`},
		{"average", `{"value":15,"count":2}`, `{"value":null,"count":0}`},
		{"min", `{"value":10,"count":2}`, `{"value":null,"count":0}`},
		{"max", `{"value":20,"count":2}`, `{"value":null,"count":0}`},
	} {
		t.Run(tt.op, func(t *testing.T) {
			config := map[string]any{"operation": tt.op, "items_path": "/items", "field_path": "/amount"}
			result := runDataNode(t, "aggregate", config, map[string]any{"data": map[string]any{"items": []any{map[string]any{"amount": 10}, map[string]any{"amount": 20}}}})
			assertDataJSON(t, result.Data()["data"], tt.expected)
			result = runDataNode(t, "aggregate", config, map[string]any{"data": map[string]any{"items": []any{}}})
			assertDataJSON(t, result.Data()["data"], tt.empty)
		})
	}
	node := makeNode(t, "aggregate", map[string]any{"operation": "sum"})
	if _, err := node.Run(t.Context(), nil, map[string]any{"data": []any{1, "2"}}); err == nil {
		t.Fatal("numeric string was silently summed")
	}
	if _, err := node.Run(t.Context(), nil, map[string]any{"data": []any{math.MaxFloat64, math.MaxFloat64}}); err == nil {
		t.Fatal("numeric overflow emitted an invalid JSON result")
	}
}

func TestFilterContainsUsesTypedArrayMembershipAndCaseSensitiveText(t *testing.T) {
	items := []any{map[string]any{"v": "alpha beta"}, map[string]any{"v": "ALPHA"}, map[string]any{"v": []any{1, "alpha"}}, map[string]any{"v": []any{"1"}}, map[string]any{"v": []any{map[string]any{"id": int64(1)}}}}
	for _, tt := range []struct {
		kind, value string
		count       int
	}{{"string", "alpha", 2}, {"number", "1", 1}, {"json", `{"id":1.0}`, 1}} {
		result := runDataNode(t, "filter", map[string]any{"conditions": []any{map[string]any{"path": "/v", "operator": "contains", "value_type": tt.kind, "value": tt.value}}}, map[string]any{"data": items})
		if len(result.Data()["data"].([]any)) != tt.count {
			t.Fatalf("wrong contains matches for %s %s: %v", tt.kind, tt.value, result.Data())
		}
	}
}

func TestDataNodesRejectMalformedConfigurationAndBoundWork(t *testing.T) {
	for _, tt := range []struct {
		kind   string
		config map[string]any
	}{
		{"edit_fields", map[string]any{"fields": []any{map[string]any{"name": ""}}}},
		{"edit_fields", map[string]any{"fields": []any{map[string]any{"name": "x", "value_type": "number", "value": "NaN"}}}},
		{"filter", map[string]any{"conditions": []any{map[string]any{"path": "/absent/~2", "operator": "exists"}}}},
		{"filter", map[string]any{"conditions": []any{map[string]any{"operator": "eval"}}}},
		{"switch", map[string]any{"rules": []any{map[string]any{"id": "fallback", "operator": "exists"}}}},
		{"merge", map[string]any{"mode": "unknown"}},
		{"aggregate", map[string]any{"operation": "unknown"}},
	} {
		n := makeNode(t, tt.kind, tt.config)
		if err := n.Validate(t.Context(), nil); err == nil {
			t.Fatalf("%s accepted invalid config: %v", tt.kind, tt.config)
		}
	}
	n := makeNode(t, "aggregate", map[string]any{"operation": "count"})
	if _, err := n.Run(t.Context(), nil, map[string]any{"data": make([]any, 10001)}); err == nil {
		t.Fatal("oversized array accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := n.Run(ctx, nil, map[string]any{"data": []any{1}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	join := makeNode(t, "merge", map[string]any{"mode": "join"})
	items := make([]any, 101)
	for i := range items {
		items[i] = map[string]any{"id": 1}
	}
	if _, err := join.Run(t.Context(), nil, map[string]any{"left": items, "right": items}); err == nil {
		t.Fatal("join expansion exceeded output bound")
	}
	filter := makeNode(t, "filter", map[string]any{"conditions": []any{map[string]any{"path": "/values", "operator": "contains", "value_type": "number", "value": "1"}}})
	if _, err := filter.Run(t.Context(), nil, map[string]any{"data": map[string]any{"values": make([]any, 10001)}}); err == nil {
		t.Fatal("oversized contains input silently reported no match")
	}
}

func TestDataNodeRegistrationsAreDiscoverableAndNonHost(t *testing.T) {
	metas := map[string]workflow.NodeMeta{}
	for _, meta := range workflow.GetAllNodeMetas() {
		metas[meta.Type] = meta
	}
	for _, kind := range []string{"edit_fields", "filter", "switch", "merge", "aggregate"} {
		host, known := service.ExecutionNodeClass(kind)
		if host || !known || metas[kind].Type == "" {
			t.Fatalf("%s missing metadata or safe runtime classification", kind)
		}
	}
}

func TestDataPipelineEmptyFilterStillAggregatesAndPins(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "input", Type: "input"},
		{ID: "filter", Type: "filter", Data: map[string]any{"items_path": "/items", "conditions": []any{map[string]any{"path": "/enabled", "operator": "eq", "value_type": "boolean", "value": "true"}}}},
		{ID: "aggregate", Type: "aggregate", Data: map[string]any{"operation": "count"}},
		{ID: "output", Type: "output"},
	}, Edges: []service.WorkflowEdge{
		{Source: "input", SourceHandle: "data", Target: "filter", TargetHandle: "data"},
		{Source: "filter", SourceHandle: "data", Target: "aggregate", TargetHandle: "data"},
		{Source: "aggregate", SourceHandle: "data", Target: "output", TargetHandle: "input"},
	}}
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	events := make(chan workflow.NodeEvent, 32)
	e.SetEventChannel(events)
	result, err := e.Run(executiontest.Context(t), graph, map[string]any{"items": []any{map[string]any{"enabled": false}}}, []string{"input"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertDataJSON(t, result.Outputs, `{"input":{"value":0,"count":0}}`)
	event := completedTestEvent(t, events, "filter")
	if event.PinSignature == "" {
		t.Fatal("empty filter output not pinnable")
	}
	assertDataJSON(t, event.Data, `{"data":[]}`)
}

func TestSwitchBranchesMergeOnceIncludingInactiveBranch(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "i", Type: "input"},
		{ID: "s", Type: "switch"},
		{ID: "left", Type: "edit_fields", Data: map[string]any{"keep_input": false, "fields": []any{map[string]any{"name": "side", "source": "value", "value": "left"}}}},
		{ID: "right", Type: "edit_fields", Data: map[string]any{"keep_input": false, "fields": []any{map[string]any{"name": "side", "source": "value", "value": "right"}}}},
		{ID: "merge", Type: "merge"},
		{ID: "output", Type: "output"},
	}, Edges: []service.WorkflowEdge{
		{Source: "i", SourceHandle: "data", Target: "s", TargetHandle: "data"},
		{Source: "s", SourceHandle: "case_1", Target: "left", TargetHandle: "data"},
		{Source: "s", SourceHandle: "fallback", Target: "right", TargetHandle: "data"},
		{Source: "left", SourceHandle: "data", Target: "merge", TargetHandle: "left"},
		{Source: "right", SourceHandle: "data", Target: "merge", TargetHandle: "right"},
		{Source: "merge", SourceHandle: "data", Target: "output", TargetHandle: "input"},
	}}
	for _, tt := range []struct{ status, side string }{{"ready", "left"}, {"other", "right"}} {
		e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
		events := make(chan workflow.NodeEvent, 64)
		e.SetEventChannel(events)
		result, err := e.Run(executiontest.Context(t), graph, map[string]any{"status": tt.status}, []string{"i"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		assertDataJSON(t, result.Outputs, `{"input":[{"side":"`+tt.side+`"}]}`)
		count := 0
		for len(events) > 0 {
			event := <-events
			if event.NodeID == "merge" && event.EventType == "started" {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("merge ran %d times", count)
		}
	}
	graph.Edges[1].SourceHandle = "case_removed"
	if _, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(executiontest.Context(t), graph, nil, []string{"i"}, nil); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("stale Switch handle accepted: %v", err)
	}
}

func TestMergeRejectsAmbiguousProducersAndIndependentLoops(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "a", Type: "loop", Data: map[string]any{"expression": "data.items"}}, {ID: "b", Type: "loop", Data: map[string]any{"expression": "data.items"}}, {ID: "merge", Type: "merge"}}, Edges: []service.WorkflowEdge{
		{Source: "i", SourceHandle: "data", Target: "a", TargetHandle: "data"},
		{Source: "i", SourceHandle: "data", Target: "b", TargetHandle: "data"},
		{Source: "a", SourceHandle: "item", Target: "merge", TargetHandle: "left"},
		{Source: "b", SourceHandle: "item", Target: "merge", TargetHandle: "right"},
	}}
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	if _, err := e.Run(executiontest.Context(t), graph, nil, []string{"i"}, nil); err == nil || !strings.Contains(err.Error(), "independent Loop") {
		t.Fatalf("independent streams silently merged: %v", err)
	}
	graph.Edges[3].TargetHandle = "left"
	if _, err := e.Run(executiontest.Context(t), graph, nil, []string{"i"}, nil); err == nil || !strings.Contains(err.Error(), "duplicate input") {
		t.Fatalf("duplicate producers silently overwritten: %v", err)
	}
}

func TestMergeInsideNestedLoopRetainsInvocationScope(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "outer", Type: "loop", Data: map[string]any{"expression": "data.items"}}, {ID: "inner", Type: "loop", Data: map[string]any{"expression": "data.inner"}}, {ID: "merge", Type: "merge"}}, Edges: []service.WorkflowEdge{
		{Source: "i", SourceHandle: "data", Target: "outer", TargetHandle: "data"},
		{Source: "outer", SourceHandle: "item", Target: "inner", TargetHandle: "data"},
		{Source: "outer", SourceHandle: "item", Target: "merge", TargetHandle: "left"},
		{Source: "inner", SourceHandle: "item", Target: "merge", TargetHandle: "right"},
	}}
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	events := make(chan workflow.NodeEvent, 64)
	e.SetEventChannel(events)
	_, err := e.Run(executiontest.Context(t), graph, map[string]any{"items": []any{map[string]any{"inner": []any{map[string]any{"v": 1}, map[string]any{"v": 2}}}}}, []string{"i"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for len(events) > 0 {
		event := <-events
		if event.NodeID == "merge" && event.EventType == "completed" {
			count++
			if len(event.Data["data"].([]any)) != 2 || event.PinSignature != "" {
				t.Fatal("merge confused per-invocation data with collected loop output")
			}
		}
	}
	if count != 2 {
		t.Fatalf("merge ran %d times, want one per inner item", count)
	}
}
