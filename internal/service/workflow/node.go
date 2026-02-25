// Package workflow implements a graph-based workflow execution engine.
//
// The design is inspired by worldline-go/chore: nodes implement the Noder
// interface, return NodeResult variants that control routing, and the engine
// uses a two-phase approach (validate → run) with concurrent goroutine-per-branch
// execution.
package workflow

import (
	"context"
	"errors"
	"sync"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Sentinel Errors ───

// ErrStopBranch is returned by a node to gracefully terminate its branch
// without propagating an error. Used by multi-input nodes when not all
// inputs have arrived yet, or by loops with empty iterations.
var ErrStopBranch = errors.New("stop branch")

// ─── Node Result Interfaces ───
//
// The engine inspects the returned NodeResult via type assertion to decide
// how data flows to downstream nodes. This is the "return-type routing"
// pattern from chore.

// NodeResult is the base interface returned by every node's Run method.
// It carries the output data as a map[string]any keyed by output port name.
type NodeResult interface {
	// Data returns the node's output keyed by output port name.
	// Example: {"text": "hello", "usage": {...}}
	Data() map[string]any
}

// NodeResultSelection is returned by nodes that route to specific output
// ports. The engine only activates the ports listed in Selection().
// Used by conditional/if nodes and script nodes.
//
// Output ports are identified by index (0, 1, 2, ...) matching the order
// of output handles on the node.
type NodeResultSelection interface {
	NodeResult
	// Selection returns the indices of output ports to activate.
	Selection() []int
}

// NodeResultFanOut is returned by loop/iterator nodes. The engine spawns
// a separate goroutine for each item in Items(), all going to output port 0.
type NodeResultFanOut interface {
	NodeResult
	// Items returns multiple data maps, each spawning a separate downstream branch.
	Items() []map[string]any
}

// ─── Noder Interface ───

// Noder is the interface that all node types must implement.
// Inspired by chore's Noder but simplified for AT's needs.
type Noder interface {
	// Type returns the node type name (e.g., "input", "llm_call").
	Type() string

	// Validate checks the node's configuration before execution.
	// Called during the validation phase. Return an error if the node
	// is misconfigured (e.g., missing required fields).
	Validate(ctx context.Context, reg *Registry) error

	// Run executes the node with the given input data.
	// The inputs map is keyed by input port name, with values coming from
	// upstream nodes. The run inputs are the original workflow trigger inputs.
	Run(ctx context.Context, reg *Registry, inputs map[string]any) (NodeResult, error)
}

// ─── Node Factory ───

// NodeFactory creates a Noder from a workflow node definition.
// Each node type registers a factory via RegisterNodeType.
type NodeFactory func(node service.WorkflowNode) (Noder, error)

// nodeFactories is the global registry of node type factories.
// Populated by init() functions in the nodes/ package.
var nodeFactories = make(map[string]NodeFactory)

// RegisterNodeType registers a node factory for a given type name.
// Called from init() functions in the nodes/ package.
func RegisterNodeType(typeName string, factory NodeFactory) {
	nodeFactories[typeName] = factory
}

// GetNodeFactory returns the factory for a given node type, or nil if not registered.
func GetNodeFactory(typeName string) NodeFactory {
	return nodeFactories[typeName]
}

// RegisteredNodeTypes returns all registered node type names.
func RegisteredNodeTypes() []string {
	types := make([]string, 0, len(nodeFactories))
	for t := range nodeFactories {
		types = append(types, t)
	}
	return types
}

// ─── Concrete Result Types ───

// result is the default NodeResult implementation.
type result struct {
	data map[string]any
}

func (r *result) Data() map[string]any { return r.data }

// NewResult creates a basic NodeResult with the given output data.
func NewResult(data map[string]any) NodeResult {
	return &result{data: data}
}

// selectionResult implements NodeResultSelection.
type selectionResult struct {
	data      map[string]any
	selection []int
}

func (r *selectionResult) Data() map[string]any { return r.data }
func (r *selectionResult) Selection() []int     { return r.selection }

// NewSelectionResult creates a NodeResult that routes to specific output ports.
func NewSelectionResult(data map[string]any, selection []int) NodeResultSelection {
	return &selectionResult{data: data, selection: selection}
}

// fanOutResult implements NodeResultFanOut.
type fanOutResult struct {
	data  map[string]any
	items []map[string]any
}

func (r *fanOutResult) Data() map[string]any    { return r.data }
func (r *fanOutResult) Items() []map[string]any { return r.items }

// NewFanOutResult creates a NodeResult that fans out to multiple downstream branches.
func NewFanOutResult(items []map[string]any) NodeResultFanOut {
	return &fanOutResult{data: map[string]any{}, items: items}
}

// ─── Registry ───

// Registry holds shared state and dependencies available to all nodes
// during execution. Similar to chore's registry.Registry.
type Registry struct {
	// ProviderLookup resolves LLM provider keys to provider instances.
	ProviderLookup ProviderLookup

	// RunInputs are the original inputs passed when triggering the workflow.
	RunInputs map[string]any

	// mu protects errors and outputs.
	mu sync.Mutex

	// errors collects non-fatal errors during execution.
	errors []error

	// outputs collects final output data from Output nodes.
	outputs map[string]any
}

// ProviderLookup returns a provider, its default model, and an error.
type ProviderLookup func(key string) (service.LLMProvider, string, error)

// NewRegistry creates a new execution registry.
func NewRegistry(lookup ProviderLookup, inputs map[string]any) *Registry {
	if inputs == nil {
		inputs = make(map[string]any)
	}
	return &Registry{
		ProviderLookup: lookup,
		RunInputs:      inputs,
		outputs:        make(map[string]any),
	}
}

// AddError records a non-fatal execution error.
func (r *Registry) AddError(err error) {
	r.mu.Lock()
	r.errors = append(r.errors, err)
	r.mu.Unlock()
}

// Errors returns all collected errors.
func (r *Registry) Errors() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]error{}, r.errors...)
}

// SetOutputs merges output data from an Output node.
func (r *Registry) SetOutputs(data map[string]any) {
	r.mu.Lock()
	for k, v := range data {
		r.outputs[k] = v
	}
	r.mu.Unlock()
}

// Outputs returns the collected final outputs.
func (r *Registry) Outputs() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]any, len(r.outputs))
	for k, v := range r.outputs {
		out[k] = v
	}
	return out
}
