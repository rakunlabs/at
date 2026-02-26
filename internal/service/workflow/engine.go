package workflow

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/rakunlabs/at/internal/service"
)

// RunResult is the output of a workflow execution.
type RunResult struct {
	Outputs map[string]any `json:"outputs"`
}

// Engine executes a workflow graph using a two-phase approach:
//   - Phase 1 (Validate): parse all nodes, validate configuration
//   - Phase 2 (Run): concurrent goroutine-per-branch execution with
//     return-type routing
type Engine struct {
	providerLookup   ProviderLookup
	skillLookup      SkillLookup
	varLookup        VarLookup
	varLister        VarLister
	nodeConfigLookup NodeConfigLookup
}

// NewEngine creates a new workflow execution engine.
func NewEngine(lookup ProviderLookup, skillLookup SkillLookup, varLookup VarLookup, varLister VarLister, nodeConfigLookup NodeConfigLookup) *Engine {
	return &Engine{providerLookup: lookup, skillLookup: skillLookup, varLookup: varLookup, varLister: varLister, nodeConfigLookup: nodeConfigLookup}
}

// ─── Execution State ───

// nodeState holds a parsed node and its connection info during execution.
type nodeState struct {
	noder   Noder
	node    service.WorkflowNode
	inputs  map[string][]connection // input port name → upstream connections
	outputs map[string][]connection // output port name → downstream connections
}

// connection represents one end of an edge between two nodes.
type connection struct {
	nodeID string
	port   string
}

// ─── Phase 1: Parse & Validate ───

// parseGraph builds nodeState map from workflow graph and validates all nodes.
func (e *Engine) parseGraph(ctx context.Context, graph service.WorkflowGraph, reg *Registry) (map[string]*nodeState, error) {
	states := make(map[string]*nodeState, len(graph.Nodes))

	// Create noders from graph nodes via factories.
	for _, n := range graph.Nodes {
		factory := GetNodeFactory(n.Type)
		if factory == nil {
			return nil, fmt.Errorf("node %q: unknown type %q", n.ID, n.Type)
		}

		noder, err := factory(n)
		if err != nil {
			return nil, fmt.Errorf("node %q: create failed: %w", n.ID, err)
		}

		states[n.ID] = &nodeState{
			noder:   noder,
			node:    n,
			inputs:  make(map[string][]connection),
			outputs: make(map[string][]connection),
		}
	}

	// Wire up connections from edges.
	for _, edge := range graph.Edges {
		srcState, ok := states[edge.Source]
		if !ok {
			return nil, fmt.Errorf("edge %q: source node %q not found", edge.ID, edge.Source)
		}
		tgtState, ok := states[edge.Target]
		if !ok {
			return nil, fmt.Errorf("edge %q: target node %q not found", edge.ID, edge.Target)
		}

		srcPort := edge.SourceHandle
		if srcPort == "" {
			srcPort = "output"
		}
		tgtPort := edge.TargetHandle
		if tgtPort == "" {
			tgtPort = "input"
		}

		srcState.outputs[srcPort] = append(srcState.outputs[srcPort], connection{
			nodeID: edge.Target,
			port:   tgtPort,
		})
		tgtState.inputs[tgtPort] = append(tgtState.inputs[tgtPort], connection{
			nodeID: edge.Source,
			port:   srcPort,
		})
	}

	// Validate all nodes.
	for id, st := range states {
		if err := st.noder.Validate(ctx, reg); err != nil {
			return nil, fmt.Errorf("node %q (%s): validation failed: %w", id, st.noder.Type(), err)
		}
	}

	return states, nil
}

// ─── Phase 2: Execute ───

// Run executes a workflow graph with the given inputs.
func (e *Engine) Run(ctx context.Context, graph service.WorkflowGraph, inputs map[string]any) (*RunResult, error) {
	if len(graph.Nodes) == 0 {
		return &RunResult{Outputs: map[string]any{}}, nil
	}

	reg := NewRegistry(e.providerLookup, e.skillLookup, e.varLookup, e.varLister, e.nodeConfigLookup, inputs)

	// Phase 1: Parse & Validate
	states, err := e.parseGraph(ctx, graph, reg)
	if err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	// Topological sort for execution order.
	order, err := topoSort(graph.Nodes, graph.Edges)
	if err != nil {
		return nil, fmt.Errorf("topological sort: %w", err)
	}

	// Phase 2: Execute nodes in topological order.
	// nodeOutputs stores the result from each node, keyed by node ID.
	nodeOutputs := make(map[string]NodeResult, len(graph.Nodes))

	// For concurrent fan-out, we use a WaitGroup to track all branches.
	var wg sync.WaitGroup
	var execMu sync.Mutex // protects nodeOutputs during concurrent writes
	var firstErr error

	// Execute sequentially for non-fan-out paths, fan-out spawns goroutines.
	for _, nodeID := range order {
		// Check for cancellation between node executions.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("workflow cancelled: %w", err)
		}

		st, ok := states[nodeID]
		if !ok {
			continue
		}

		// Gather inputs from upstream nodes.
		nodeInputs := e.gatherInputs(nodeID, states, nodeOutputs)

		// Run the node.
		result, err := st.noder.Run(ctx, reg, nodeInputs)
		if err != nil {
			if err == ErrStopBranch {
				continue
			}
			return nil, fmt.Errorf("node %q (%s): %w", nodeID, st.noder.Type(), err)
		}

		if result == nil {
			continue
		}

		// Store output.
		execMu.Lock()
		nodeOutputs[nodeID] = result
		execMu.Unlock()

		// Handle fan-out: if the result implements NodeResultFanOut,
		// we need to spawn goroutines for each item.
		if fanOut, ok := result.(NodeResultFanOut); ok {
			items := fanOut.Items()
			if len(items) == 0 {
				continue
			}

			// For each fan-out item, execute the downstream subgraph
			// in a separate goroutine.
			for _, item := range items {
				wg.Add(1)
				go func(data map[string]any) {
					defer wg.Done()
					if err := e.runFanOutBranch(ctx, nodeID, data, states, order, reg); err != nil {
						slog.Error("fan-out branch failed", "node", nodeID, "error", err)
						execMu.Lock()
						if firstErr == nil {
							firstErr = err
						}
						execMu.Unlock()
					}
				}(item)
			}
		}

		// Handle selection routing: if the result implements NodeResultSelection,
		// we need to mark which output ports are active. The gatherInputs function
		// will check this during downstream execution.
		// (Selection routing is handled naturally by the port-based wiring —
		// nodes with selection just have multiple named output ports like
		// "true"/"false", and the selection indices map to port names.)
	}

	// Wait for all fan-out branches.
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	// Collect outputs from Output nodes via the registry.
	outputs := reg.Outputs()

	// If no explicit Output node set outputs, collect from terminal nodes.
	if len(outputs) == 0 {
		for _, nodeID := range order {
			st := states[nodeID]
			if st == nil {
				continue
			}
			if isTerminal(nodeID, graph.Edges) {
				if r, ok := nodeOutputs[nodeID]; ok && r != nil {
					for k, v := range r.Data() {
						outputs[k] = v
					}
				}
			}
		}
	}

	return &RunResult{Outputs: outputs}, nil
}

// gatherInputs collects data from upstream nodes for a given node.
func (e *Engine) gatherInputs(nodeID string, states map[string]*nodeState, nodeOutputs map[string]NodeResult) map[string]any {
	st := states[nodeID]
	if st == nil {
		return make(map[string]any)
	}

	result := make(map[string]any)

	for tgtPort, conns := range st.inputs {
		for _, conn := range conns {
			upstream, ok := nodeOutputs[conn.nodeID]
			if !ok || upstream == nil {
				continue
			}

			upstreamData := upstream.Data()

			// Check if the upstream result has selection routing.
			if sel, ok := upstream.(NodeResultSelection); ok {
				// Find which output port index this connection uses.
				upstreamState := states[conn.nodeID]
				if upstreamState != nil {
					portActive := e.isPortActive(conn.port, upstreamState, sel.Selection())
					if !portActive {
						continue
					}
				}
			}

			// Map source port data to target port.
			if val, exists := upstreamData[conn.port]; exists {
				result[tgtPort] = val
			} else {
				// If the specific port doesn't exist in data, merge all data.
				for k, v := range upstreamData {
					result[k] = v
				}
			}
		}
	}

	return result
}

// isPortActive checks whether a specific output port is active given selection indices.
func (e *Engine) isPortActive(portName string, st *nodeState, selection []int) bool {
	// Build ordered list of output port names.
	portNames := e.getOutputPortNames(st)

	for _, idx := range selection {
		if idx >= 0 && idx < len(portNames) && portNames[idx] == portName {
			return true
		}
	}

	return false
}

// getOutputPortNames returns the output port names for a node in deterministic order.
func (e *Engine) getOutputPortNames(st *nodeState) []string {
	// Collect unique output port names.
	seen := make(map[string]bool)
	var names []string
	for port := range st.outputs {
		if !seen[port] {
			seen[port] = true
			names = append(names, port)
		}
	}

	// Sort for determinism — but typically node types define their ports
	// in a known order. For now, use alphabetical as fallback.
	// The node type should define port order via its factory.
	return names
}

// runFanOutBranch executes downstream nodes for a single fan-out item.
func (e *Engine) runFanOutBranch(ctx context.Context, sourceNodeID string, data map[string]any, states map[string]*nodeState, order []string, reg *Registry) error {
	// Find the position of sourceNodeID in order and execute everything after it
	// that is downstream.
	downstream := e.findDownstream(sourceNodeID, states)

	// Create a local outputs map for this branch.
	branchOutputs := make(map[string]NodeResult)
	branchOutputs[sourceNodeID] = NewResult(data)

	for _, nodeID := range order {
		if !downstream[nodeID] {
			continue
		}

		// Check for cancellation between node executions.
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("workflow cancelled: %w", err)
		}

		st := states[nodeID]
		if st == nil {
			continue
		}

		nodeInputs := e.gatherInputs(nodeID, states, branchOutputs)

		result, err := st.noder.Run(ctx, reg, nodeInputs)
		if err != nil {
			if err == ErrStopBranch {
				continue
			}
			return fmt.Errorf("node %q (%s): %w", nodeID, st.noder.Type(), err)
		}

		if result != nil {
			branchOutputs[nodeID] = result
		}
	}

	return nil
}

// findDownstream returns a set of all node IDs reachable from sourceNodeID.
func (e *Engine) findDownstream(sourceNodeID string, states map[string]*nodeState) map[string]bool {
	visited := make(map[string]bool)

	var visit func(id string)
	visit = func(id string) {
		st := states[id]
		if st == nil {
			return
		}
		for _, conns := range st.outputs {
			for _, conn := range conns {
				if !visited[conn.nodeID] {
					visited[conn.nodeID] = true
					visit(conn.nodeID)
				}
			}
		}
	}

	visit(sourceNodeID)
	return visited
}

// ─── Graph Utilities ───

// topoSort performs a topological sort using Kahn's algorithm.
func topoSort(nodes []service.WorkflowNode, edges []service.WorkflowEdge) ([]string, error) {
	inDegree := make(map[string]int, len(nodes))
	adjacency := make(map[string][]string, len(nodes))

	for _, n := range nodes {
		inDegree[n.ID] = 0
	}

	for _, e := range edges {
		adjacency[e.Source] = append(adjacency[e.Source], e.Target)
		inDegree[e.Target]++
	}

	var queue []string
	for _, n := range nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}

	var order []string
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		order = append(order, current)

		for _, neighbor := range adjacency[current] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	if len(order) != len(nodes) {
		return nil, fmt.Errorf("workflow graph contains a cycle")
	}

	return order, nil
}

// isTerminal returns true if the node has no outgoing edges.
func isTerminal(nodeID string, edges []service.WorkflowEdge) bool {
	for _, e := range edges {
		if e.Source == nodeID {
			return false
		}
	}
	return true
}
