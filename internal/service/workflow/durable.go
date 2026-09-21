package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rakunlabs/at/internal/service"
)

type WaitRequest struct {
	Mode           string
	Seconds        int
	ExpiresSeconds int
	Prompt         string
}

type NodeResultWait interface {
	NodeResult
	WaitRequest() WaitRequest
}

type waitResult struct {
	data    map[string]any
	request WaitRequest
}

func (r *waitResult) Data() map[string]any     { return r.data }
func (r *waitResult) WaitRequest() WaitRequest { return r.request }
func NewWaitResult(data map[string]any, request WaitRequest) NodeResultWait {
	return &waitResult{data: data, request: request}
}

func HasDurableWait(graph service.WorkflowGraph, entries []string) bool {
	reachable := reachableNodes(entries, graph.Nodes, graph.Edges)
	for _, node := range graph.Nodes {
		if reachable[node.ID] && node.Type == "wait" {
			return true
		}
	}
	return false
}

// Durable execution initially checkpoints the serial DAG, including ordinary
// conditional branches. Fan-out stacks need a separate multi-invocation format.
func ValidateDurableGraph(graph service.WorkflowGraph, entries []string) error {
	ids := make(map[string]bool)
	for _, node := range graph.Nodes {
		if strings.TrimSpace(node.ID) == "" || len(node.ID) > 256 || ids[node.ID] {
			return fmt.Errorf("durable workflows require unique nonempty node IDs up to 256 bytes")
		}
		ids[node.ID] = true
	}
	reachable := reachableNodes(entries, graph.Nodes, graph.Edges)
	if len(reachable) == 0 {
		return fmt.Errorf("durable workflow requires an Input entry point")
	}
	if len(reachable) > 240 {
		return fmt.Errorf("durable workflow exceeds 240 reachable nodes")
	}
	for _, node := range graph.Nodes {
		if reachable[node.ID] && node.Type == "loop" {
			return fmt.Errorf("durable Wait does not yet support Loop fan-out; use a non-Loop workflow")
		}
	}
	_, err := topoSort(reachable, graph.Edges)
	return err
}

// PortableJSON refuses streams, functions and structs instead of silently
// serializing their implementation details as {}. Durable data must be JSON.
func PortableJSON(value any) error {
	var check func(reflect.Value, int) error
	check = func(v reflect.Value, depth int) error {
		if depth > 64 {
			return fmt.Errorf("durable data exceeds 64 levels or contains a cycle")
		}
		if !v.IsValid() {
			return nil
		}
		if v.Kind() == reflect.Interface {
			return check(v.Elem(), depth+1)
		}
		switch v.Kind() {
		case reflect.Map:
			if v.Type().Key().Kind() != reflect.String {
				return fmt.Errorf("durable objects require string keys")
			}
			it := v.MapRange()
			for it.Next() {
				if !utf8.ValidString(it.Key().String()) {
					return fmt.Errorf("durable object keys must be valid UTF-8")
				}
				if err := check(it.Value(), depth+1); err != nil {
					return err
				}
			}
		case reflect.Array, reflect.Slice:
			if v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8 {
				return fmt.Errorf("durable data requires a base64 string or artifact reference instead of raw bytes")
			}
			for i := 0; i < v.Len(); i++ {
				if err := check(v.Index(i), depth+1); err != nil {
					return err
				}
			}
		case reflect.String:
			if !utf8.ValidString(v.String()) {
				return fmt.Errorf("durable strings must be valid UTF-8")
			}
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		default:
			return fmt.Errorf("durable data cannot contain %s values; use JSON or artifact references", v.Kind())
		}
		return nil
	}
	if err := check(reflect.ValueOf(value), 0); err != nil {
		return err
	}
	_, err := json.Marshal(value)
	return err
}

// RunDurable never replays an in-flight step. The save callback must durably
// acknowledge the pre-step marker before any external side effect is allowed.
func (e *Engine) RunDurable(ctx context.Context, graph service.WorkflowGraph, inputs map[string]any, entries []string, checkpoint service.WorkflowCheckpoint, resume bool, save func(service.WorkflowCheckpoint, string) error) (*RunResult, error) {
	if save == nil {
		return nil, fmt.Errorf("durable checkpoint store is required")
	}
	if err := ValidateDurableGraph(graph, entries); err != nil {
		return nil, err
	}
	if err := PortableJSON(inputs); err != nil {
		return nil, err
	}
	if checkpoint.Version == 0 {
		checkpoint.Version = 1
	}
	if checkpoint.Version != 1 {
		return nil, fmt.Errorf("unsupported workflow checkpoint version %d", checkpoint.Version)
	}
	if checkpoint.InFlight != "" {
		return nil, fmt.Errorf("step %q was interrupted; its side effects are uncertain and it will not be replayed", checkpoint.InFlight)
	}
	if checkpoint.Nodes == nil {
		checkpoint.Nodes = make(map[string]service.WorkflowNodeCheckpoint)
	}
	if checkpoint.Outputs == nil {
		checkpoint.Outputs = make(map[string]any)
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return nil, err
	}
	if checkpoint.Waiting != nil {
		if !resume {
			return &RunResult{Outputs: checkpoint.Outputs}, nil
		}
		wait := checkpoint.Waiting
		checkpoint.Nodes[wait.NodeID] = service.WorkflowNodeCheckpoint{Kind: "result", Data: wait.Data}
		checkpoint.Waiting = nil
		if err := save(checkpoint, "running"); err != nil {
			return nil, err
		}
	}
	reg := NewRegistryWithDependencies(e.ensureDependencies(), inputs)
	reg.Dependencies = ScopeDependencies(ctx, *reg.Dependencies)
	reg.SetOutputs(checkpoint.Outputs)
	engine := *e
	engine.testPins = nil
	engine.resumeNodes = checkpoint.Nodes
	reg.engine = &engine
	reachable := reachableNodes(entries, graph.Nodes, graph.Edges)
	for id := range reachable {
		if _, done := checkpoint.Nodes[id]; done {
			continue
		}
		for _, node := range graph.Nodes {
			if node.ID == id {
				if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "node", Name: node.Type}); err != nil {
					return nil, err
				}
			}
		}
	}
	states, err := engine.parseGraph(ctx, graph, reg, reachable)
	if err != nil {
		return nil, fmt.Errorf("durable graph validation: %w", err)
	}
	order, err := topoSort(reachable, graph.Edges)
	if err != nil {
		return nil, err
	}
	results := make(map[string]NodeResult)
	for id, saved := range checkpoint.Nodes {
		switch saved.Kind {
		case "result":
			results[id] = NewResult(saved.Data)
		case "selection":
			results[id] = NewSelectionResult(saved.Data, saved.Selection)
		case "skipped":
		default:
			return nil, fmt.Errorf("invalid checkpoint result kind for %q", id)
		}
	}
	for _, id := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, done := checkpoint.Nodes[id]; done {
			continue
		}
		st := states[id]
		nodeInputs, active := engine.gatherInputs(id, states, results)
		if !active {
			checkpoint.Nodes[id] = service.WorkflowNodeCheckpoint{Kind: "skipped"}
			if err := save(checkpoint, "running"); err != nil {
				return nil, err
			}
			continue
		}
		checkpoint.InFlight = id
		if err := save(checkpoint, "running"); err != nil {
			return nil, fmt.Errorf("persist step intent: %w", err)
		}
		result, stopped, err := engine.executeNode(ctx, st, reg, nodeInputs, func(map[string]any, error) {})
		if err != nil {
			return nil, err
		}
		if waiting, ok := result.(NodeResultWait); ok {
			if err := PortableJSON(waiting.Data()); err != nil {
				return nil, err
			}
			spec := waiting.WaitRequest()
			wait := &service.WorkflowWaitState{NodeID: id, Mode: spec.Mode, Prompt: spec.Prompt, Data: waiting.Data()}
			now := time.Now().UTC()
			if spec.Mode == "duration" {
				at := now.Add(time.Duration(spec.Seconds) * time.Second)
				wait.WakeAt = &at
			} else {
				at := now.Add(time.Duration(spec.ExpiresSeconds) * time.Second)
				wait.ExpiresAt = &at
			}
			checkpoint.Waiting, checkpoint.InFlight = wait, ""
			checkpoint.Outputs = reg.Outputs()
			if err := save(checkpoint, "waiting"); err != nil {
				return nil, fmt.Errorf("persist wait checkpoint: %w", err)
			}
			return &RunResult{Outputs: checkpoint.Outputs}, nil
		}
		entry := service.WorkflowNodeCheckpoint{Kind: "skipped"}
		if !stopped && result != nil {
			if _, fanout := result.(NodeResultFanOut); fanout {
				return nil, fmt.Errorf("durable execution cannot checkpoint fan-out results")
			}
			if err := PortableJSON(result.Data()); err != nil {
				return nil, fmt.Errorf("checkpoint %s: %w", id, err)
			}
			entry.Kind, entry.Data = "result", result.Data()
			if selection, ok := result.(NodeResultSelection); ok {
				entry.Kind, entry.Selection = "selection", selection.Selection()
			}
			results[id] = result
		}
		checkpoint.Nodes[id] = entry
		checkpoint.Outputs, checkpoint.InFlight = reg.Outputs(), ""
		if err := save(checkpoint, "running"); err != nil {
			return nil, fmt.Errorf("persist completed step: %w", err)
		}
	}
	if err := save(checkpoint, "completed"); err != nil {
		return nil, err
	}
	return &RunResult{Outputs: checkpoint.Outputs}, nil
}
