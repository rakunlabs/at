package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/rakunlabs/at/internal/service"
)

// TestRunOptions is an explicit editor-only run contract. Pins are request data,
// never node configuration, and are never inherited by production or child runs.
type TestRunOptions struct {
	TargetNodeID string                `json:"target_node_id,omitempty"`
	Pins         map[string]PinnedNode `json:"pins,omitempty"`
}

type PinnedNode struct {
	Signature  string         `json:"signature"`
	Data       map[string]any `json:"data"`
	ResultKind string         `json:"result_kind"`
	Selection  []string       `json:"selection,omitempty"`
}

func pinnableNode(nodeType string) bool {
	switch nodeType {
	case "input", "llm_call", "agent_call", "template", "http_request", "script", "exec", "email", "log", "workflow_call", "image_generate", "vision_analyze", "audio_generate", "audio_transcribe", "embedding", "conditional", "edit_fields", "filter", "switch", "merge", "aggregate":
		return true
	}
	return false
}

func ancestorNodes(graph service.WorkflowGraph, target string) map[string]bool {
	parents := make(map[string][]string)
	for _, edge := range graph.Edges {
		parents[edge.Target] = append(parents[edge.Target], edge.Source)
	}
	result := map[string]bool{target: true}
	queue := []string{target}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, parent := range parents[id] {
			if !result[parent] {
				result[parent] = true
				queue = append(queue, parent)
			}
		}
	}
	return result
}

// NodePinSignature is a freshness check, not an authorization credential. Only
// executable node data and upstream wiring matter: moving nodes or editing a
// downstream step must not invalidate a useful test fixture.
func NodePinSignature(graph service.WorkflowGraph, inputs map[string]any, entryIDs []string, target string) string {
	ancestors := ancestorNodes(graph, target)
	nodes := make([]service.WorkflowNode, 0, len(ancestors))
	eligible := false
	for _, node := range graph.Nodes {
		if !ancestors[node.ID] {
			continue
		}
		// The inspector captures one invocation, not the complete fan-out stream.
		if node.Type == "loop" {
			return ""
		}
		if node.ID == target {
			eligible = pinnableNode(node.Type)
		}
		data := make(map[string]any, len(node.Data))
		for key, value := range node.Data {
			if key != "label" {
				data[key] = value
			}
		}
		nodes = append(nodes, service.WorkflowNode{ID: node.ID, Type: node.Type, Data: data})
	}
	if !eligible {
		return ""
	}
	edges := make([]service.WorkflowEdge, 0)
	for _, edge := range graph.Edges {
		if ancestors[edge.Source] && ancestors[edge.Target] {
			edges = append(edges, service.WorkflowEdge{Source: edge.Source, Target: edge.Target, SourceHandle: edge.SourceHandle, TargetHandle: edge.TargetHandle})
		}
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		a, _ := json.Marshal(edges[i])
		b, _ := json.Marshal(edges[j])
		return string(a) < string(b)
	})
	entries := append([]string(nil), entryIDs...)
	sort.Strings(entries)
	payload, err := json.Marshal(struct {
		Nodes   []service.WorkflowNode
		Edges   []service.WorkflowEdge
		Inputs  map[string]any
		Entries []string
	}{nodes, edges, inputs, entries})
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}

func prepareTestRun(graph service.WorkflowGraph, inputs map[string]any, entries []string, options TestRunOptions) (service.WorkflowGraph, map[string]PinnedNode, error) {
	selected := reachableNodes(entries, graph.Nodes, graph.Edges)
	if options.TargetNodeID != "" {
		found := false
		for _, node := range graph.Nodes {
			if node.ID == options.TargetNodeID && node.Type != "group" && node.Type != "sticky_note" {
				found = true
			}
		}
		if !found || !selected[options.TargetNodeID] {
			return graph, nil, fmt.Errorf("target node %q must be connected to the selected Input entry point", options.TargetNodeID)
		}
		ancestors := ancestorNodes(graph, options.TargetNodeID)
		for id := range selected {
			if !ancestors[id] {
				delete(selected, id)
			}
		}
	}
	if len(options.Pins) > 32 {
		return graph, nil, fmt.Errorf("a test run accepts at most 32 pinned nodes")
	}
	pins := make(map[string]PinnedNode)
	totalBytes := 0
	for id, pin := range options.Pins {
		encoded, err := json.Marshal(pin)
		if err != nil || pin.Data == nil || len(encoded) > nodePreviewMaxBytes+4096 || len(pin.Selection) > 64 {
			return graph, nil, fmt.Errorf("pin for node %q exceeds the preview limits or contains invalid data", id)
		}
		totalBytes += len(encoded)
		if totalBytes > 512*1024 {
			return graph, nil, fmt.Errorf("pinned data exceeds 512 KiB")
		}
		dataJSON, err := json.Marshal(pin.Data)
		if err != nil || len(dataJSON) > nodePreviewMaxBytes {
			return graph, nil, fmt.Errorf("pin for node %q exceeds 64 KiB", id)
		}
		// Execute step always runs that step, even when its output was pinned.
		if id == options.TargetNodeID {
			continue
		}
		if !selected[id] {
			continue
		} // unrelated branch pins do not affect this test
		signature := NodePinSignature(graph, inputs, entries, id)
		if signature == "" || pin.Signature != signature {
			return graph, nil, fmt.Errorf("pin for node %q is stale or unsupported; unpin and run it again", id)
		}
		if pin.ResultKind != "result" && pin.ResultKind != "selection" {
			return graph, nil, fmt.Errorf("pin for node %q has an unsupported result kind", id)
		}
		// Own a detached copy, using ordinary JSON runtime number types.
		var copied map[string]any
		if err := json.Unmarshal(dataJSON, &copied); err != nil {
			return graph, nil, fmt.Errorf("pin for node %q: %w", id, err)
		}
		pin.Data = copied
		pin.Selection = append([]string(nil), pin.Selection...)
		pins[id] = pin
	}
	filtered := service.WorkflowGraph{}
	for _, node := range graph.Nodes {
		if selected[node.ID] {
			filtered.Nodes = append(filtered.Nodes, node)
		}
	}
	for _, edge := range graph.Edges {
		if selected[edge.Source] && selected[edge.Target] {
			filtered.Edges = append(filtered.Edges, edge)
		}
	}
	if HasDurableWait(filtered, entries) {
		return graph, nil, fmt.Errorf("partial/pinned tests cannot cross Wait; test an earlier step or run the full durable workflow")
	}
	return filtered, pins, nil
}

// ValidateTestRun lets the HTTP adapter reject stale pins before committing SSE
// headers or making any provider calls. RunTest revalidates for non-HTTP callers.
func ValidateTestRun(graph service.WorkflowGraph, inputs map[string]any, entries []string, options TestRunOptions) error {
	_, _, err := prepareTestRun(graph, inputs, entries, options)
	return err
}

func (e *Engine) RunTest(ctx context.Context, graph service.WorkflowGraph, inputs map[string]any, entries []string, options TestRunOptions) (*RunResult, error) {
	planned, pins, err := prepareTestRun(graph, inputs, entries, options)
	if err != nil {
		return nil, fmt.Errorf("test run: %w", err)
	}
	engine := *e
	engine.testPins = pins
	engine.pinSignatures = make(map[string]string)
	if engine.eventCh != nil {
		for _, node := range planned.Nodes {
			engine.pinSignatures[node.ID] = NodePinSignature(graph, inputs, entries, node.ID)
		}
	}
	// Use all selected nodes as reachability seeds after slicing. This includes
	// resource ancestors without reintroducing descendants of the target.
	seeds := make([]string, 0, len(planned.Nodes))
	for _, node := range planned.Nodes {
		seeds = append(seeds, node.ID)
	}
	return engine.run(ctx, planned, inputs, seeds, nil)
}
