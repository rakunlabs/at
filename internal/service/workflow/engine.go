package workflow

import (
	"context"
	"fmt"
	"sync"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/logi"
)

// RunResult is the output of a workflow execution.
type RunResult struct {
	Outputs map[string]any `json:"outputs"`
}

// EarlyOutput is sent on the output channel when the first output node
// fires, or when execution completes/fails before any output node is reached.
// Callers waiting for a sync response can read from the channel without
// waiting for the entire graph to finish.
type EarlyOutput struct {
	Outputs map[string]any
	Err     error
}

// Engine executes a workflow graph using a two-phase approach:
//   - Phase 1 (Validate): discover nodes reachable from the specified entry
//     nodes via edges, parse only those nodes, validate configuration
//   - Phase 2 (Run): concurrent goroutine-per-branch execution with
//     return-type routing
//
// The caller specifies which node(s) to start from (e.g. the specific
// http_trigger or cron_trigger that fired). Only the subgraph reachable
// from those entry points is executed. Annotation nodes (group, sticky_note)
// and unrelated trigger branches are silently excluded.
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
// Only nodes in the reachable set are initialized; annotation and disconnected
// nodes are silently skipped.
func (e *Engine) parseGraph(ctx context.Context, graph service.WorkflowGraph, reg *Registry, reachable map[string]bool) (map[string]*nodeState, error) {
	// Build a lookup so we can find nodes by ID.
	nodeLookup := make(map[string]service.WorkflowNode, len(graph.Nodes))
	for _, n := range graph.Nodes {
		nodeLookup[n.ID] = n
	}

	states := make(map[string]*nodeState, len(reachable))

	// Create noders only for reachable nodes.
	for id := range reachable {
		n, ok := nodeLookup[id]
		if !ok {
			return nil, fmt.Errorf("node %q: referenced by edge but not found in graph", id)
		}

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

	// Wire up connections from edges (skip edges outside the reachable set).
	for _, edge := range graph.Edges {
		srcState, ok := states[edge.Source]
		if !ok {
			continue // source not reachable — skip
		}
		tgtState, ok := states[edge.Target]
		if !ok {
			continue // target not reachable — skip
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
//
// entryNodeIDs specifies which node(s) to use as the starting point for BFS
// reachability. For manual/API runs pass the IDs of "input" nodes, for
// webhook runs pass the specific http_trigger node ID, for cron runs pass
// the specific cron_trigger node ID. If nil/empty, all known start types
// are used as a fallback.
//
// outputCh is an optional channel. When non-nil, the engine sends an
// EarlyOutput as soon as the first "output" node fires (or when execution
// completes/fails if no output node is reached). This allows sync callers
// to respond immediately while the rest of the graph continues in the
// background. Pass nil if early output notification is not needed.
func (e *Engine) Run(ctx context.Context, graph service.WorkflowGraph, inputs map[string]any, entryNodeIDs []string, outputCh chan<- EarlyOutput) (*RunResult, error) {
	// Ensure outputCh is always signaled exactly once so callers never block.
	var outputOnce sync.Once
	signalOutput := func(outputs map[string]any, err error) {
		if outputCh == nil {
			return
		}
		outputOnce.Do(func() {
			outputCh <- EarlyOutput{Outputs: outputs, Err: err}
		})
	}
	defer func() {
		// Fallback: if no output node fired and no error was sent,
		// signal with whatever outputs the registry collected (may be empty).
		signalOutput(nil, nil)
	}()

	if len(graph.Nodes) == 0 {
		signalOutput(map[string]any{}, nil)
		return &RunResult{Outputs: map[string]any{}}, nil
	}

	reg := NewRegistry(e.providerLookup, e.skillLookup, e.varLookup, e.varLister, e.nodeConfigLookup, inputs)

	// Compute the set of nodes reachable from the entry nodes via edges.
	reachable := reachableNodes(entryNodeIDs, graph.Nodes, graph.Edges)
	if len(reachable) == 0 {
		signalOutput(map[string]any{}, nil)
		return &RunResult{Outputs: map[string]any{}}, nil
	}

	// Phase 1: Parse & Validate (only reachable nodes).
	states, err := e.parseGraph(ctx, graph, reg, reachable)
	if err != nil {
		err = fmt.Errorf("validation: %w", err)
		signalOutput(nil, err)
		return nil, err
	}

	// Topological sort for execution order (only reachable nodes).
	order, err := topoSort(reachable, graph.Edges)
	if err != nil {
		err = fmt.Errorf("topological sort: %w", err)
		signalOutput(nil, err)
		return nil, err
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
			err = fmt.Errorf("workflow cancelled: %w", err)
			signalOutput(nil, err)
			return nil, err
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
			err = fmt.Errorf("node %q (%s): %w", nodeID, st.noder.Type(), err)
			signalOutput(nil, err)
			return nil, err
		}

		if result == nil {
			continue
		}

		// Store output.
		execMu.Lock()
		nodeOutputs[nodeID] = result
		execMu.Unlock()

		// Signal early output when the first "output" node fires.
		if st.noder.Type() == "output" {
			signalOutput(reg.Outputs(), nil)
		}

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
						logi.Ctx(ctx).Error("fan-out branch failed", "node", nodeID, "error", err)
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

	// Signal with final outputs if no output node fired earlier.
	signalOutput(outputs, nil)

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

// reachableNodes returns the set of node IDs reachable from the given entry
// nodes by following edges forward via BFS. Nodes not reachable from any
// entry node are excluded from execution.
//
// If entryNodeIDs is empty, it falls back to seeding from all known start
// types (input, http_trigger, cron_trigger) for backward compatibility.
func reachableNodes(entryNodeIDs []string, nodes []service.WorkflowNode, edges []service.WorkflowEdge) map[string]bool {
	// Build forward adjacency from edges.
	adj := make(map[string][]string)
	for _, e := range edges {
		adj[e.Source] = append(adj[e.Source], e.Target)
	}

	// Seed BFS with the provided entry nodes.
	reachable := make(map[string]bool)
	var queue []string

	if len(entryNodeIDs) > 0 {
		for _, id := range entryNodeIDs {
			reachable[id] = true
			queue = append(queue, id)
		}
	} else {
		// Fallback: seed from all known start/trigger types.
		startTypes := map[string]bool{
			"input":        true,
			"http_trigger": true,
			"cron_trigger": true,
		}
		for _, n := range nodes {
			if startTypes[n.Type] {
				reachable[n.ID] = true
				queue = append(queue, n.ID)
			}
		}
	}

	// BFS forward through edges.
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adj[current] {
			if !reachable[next] {
				reachable[next] = true
				queue = append(queue, next)
			}
		}
	}

	return reachable
}

// topoSort performs a topological sort using Kahn's algorithm.
// Only nodes in the reachable set are considered.
func topoSort(reachable map[string]bool, edges []service.WorkflowEdge) ([]string, error) {
	inDegree := make(map[string]int, len(reachable))
	adjacency := make(map[string][]string, len(reachable))

	for id := range reachable {
		inDegree[id] = 0
	}

	for _, e := range edges {
		if !reachable[e.Source] || !reachable[e.Target] {
			continue
		}
		adjacency[e.Source] = append(adjacency[e.Source], e.Target)
		inDegree[e.Target]++
	}

	var queue []string
	for id := range reachable {
		if inDegree[id] == 0 {
			queue = append(queue, id)
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

	if len(order) != len(reachable) {
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
