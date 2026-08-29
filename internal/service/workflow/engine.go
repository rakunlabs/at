package workflow

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

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

// NodeEvent is emitted during workflow execution to provide real-time
// per-node progress updates. Used by the SSE streaming endpoint.
type NodeEvent struct {
	NodeID     string         `json:"node_id"`
	NodeType   string         `json:"node_type"`
	EventType  string         `json:"event_type"`     // "started", "completed", "error", "skipped"
	Data       map[string]any `json:"data,omitempty"` // output data (for completed)
	DurationMs int64          `json:"duration_ms,omitempty"`
	Error      string         `json:"error,omitempty"`
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
	dependencies *Dependencies

	// eventCh receives real-time node execution events when set.
	// The channel is optional; when nil, no events are emitted.
	// The caller is responsible for draining it.
	eventCh chan<- NodeEvent
}

// SetLoopGov installs the loop governor used by the agent_call node.
// May be called once at engine construction; nil disables governance
// (legacy behaviour).
func (e *Engine) SetLoopGov(gov LoopGovernor) {
	e.ensureDependencies().LoopGov = gov
}

// NewEngine creates a new workflow execution engine.
func NewEngine(lookup ProviderLookup, skillLookup SkillLookup, varLookup VarLookup, varLister VarLister, nodeConfigLookup NodeConfigLookup, workflowLookup WorkflowLookup, agentLookup AgentLookup, varSave VarSaveFunc, builtinDispatcher BuiltinToolDispatcher, builtinDefs []BuiltinToolDef, userPrefLookup UserPrefLookup, chatMessageCreator ChatMessageCreatorFunc, chatSessionLookup ChatSessionLookupFunc, recordUsage RecordUsageFunc, checkBudget CheckBudgetFunc, recordObservation RecordObservationFunc, goalAncestry GoalAncestryFunc, versionLookup VersionLookupFunc) *Engine {
	return NewEngineWithDependencies(Dependencies{
		ProviderLookup:        lookup,
		SkillLookup:           skillLookup,
		VarLookup:             varLookup,
		VarLister:             varLister,
		NodeConfigLookup:      nodeConfigLookup,
		WorkflowLookup:        workflowLookup,
		AgentLookup:           agentLookup,
		VarSave:               varSave,
		BuiltinToolDispatcher: builtinDispatcher,
		BuiltinToolDefs:       builtinDefs,
		UserPrefLookup:        userPrefLookup,
		ChatMessageCreator:    chatMessageCreator,
		ChatSessionLookup:     chatSessionLookup,
		RecordUsage:           recordUsage,
		CheckBudget:           checkBudget,
		RecordObservation:     recordObservation,
		GoalAncestry:          goalAncestry,
		VersionLookup:         versionLookup,
	})
}

// NewEngineWithDependencies creates an engine from one dependency object.
func NewEngineWithDependencies(deps Dependencies) *Engine {
	return &Engine{dependencies: &deps}
}

// NewChild creates an engine with a complete copy of the parent's dependencies.
func (e *Engine) NewChild() *Engine {
	return NewEngineWithDependencies(*e.ensureDependencies())
}

func (e *Engine) ensureDependencies() *Dependencies {
	if e.dependencies == nil {
		e.dependencies = &Dependencies{}
	}
	return e.dependencies
}

// SetConnectionLookup sets the callback used by agent_call nodes to resolve
// a named Connection by ID. When set, tool handlers inside the agentic loop
// resolve provider-scoped variable keys (e.g. "youtube_refresh_token") through
// the agent's bound connections before falling back to global variables.
// Optional — when nil, only global variables are used.
func (e *Engine) SetConnectionLookup(f ConnectionLookup) {
	e.ensureDependencies().ConnectionLookup = f
}

// SetWorkflowByNameLookup sets the callback used by agent_call nodes to
// resolve agent-attached workflows (AgentConfig.Workflows) by name.
// Optional — when nil, agents cannot attach workflows directly.
func (e *Engine) SetWorkflowByNameLookup(f WorkflowByNameLookupFunc) {
	e.ensureDependencies().WorkflowByNameLookup = f
}

// SetWorkflowExecutor sets the callback used by agent_call nodes to dispatch
// `wf_*` tool calls. Optional — when nil, workflow tool calls fail with a
// "no handler" error message.
func (e *Engine) SetWorkflowExecutor(f WorkflowExecutorFunc) {
	e.ensureDependencies().WorkflowExecutor = f
}

// SetEventChannel sets the channel for real-time node execution events.
// When set, the engine emits NodeEvent for each node start/complete/error.
// The caller must drain the channel; the engine does non-blocking sends
// and drops events if the channel is full.
func (e *Engine) SetEventChannel(ch chan<- NodeEvent) {
	e.eventCh = ch
}

// emitEvent sends a NodeEvent to the event channel if configured.
// Non-blocking: drops the event if the channel buffer is full.
func (e *Engine) emitEvent(ev NodeEvent) {
	if e.eventCh == nil {
		return
	}
	select {
	case e.eventCh <- ev:
	default:
		// Drop event if channel is full — non-blocking.
	}
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

// nodeRef formats a human-readable node reference for error messages.
// Example: `node #3 "llm_call_3" (llm_call)` or `node "llm_call_3" (llm_call)`.
func nodeRef(st *nodeState) string {
	if st.node.NodeNumber != nil {
		return fmt.Sprintf("node #%d %q (%s)", *st.node.NodeNumber, st.node.ID, st.noder.Type())
	}
	return fmt.Sprintf("node %q (%s)", st.node.ID, st.noder.Type())
}

// rawNodeRef formats a node reference from a raw WorkflowNode (before a Noder
// is created). Used during parseGraph when nodeState doesn't exist yet.
func rawNodeRef(n service.WorkflowNode) string {
	if n.NodeNumber != nil {
		return fmt.Sprintf("node #%d %q", *n.NodeNumber, n.ID)
	}
	return fmt.Sprintf("node %q", n.ID)
}

// nodeLogAttrs returns structured log attributes for a node.
func nodeLogAttrs(st *nodeState) []any {
	attrs := []any{
		slog.String("node_id", st.node.ID),
		slog.String("node_type", st.noder.Type()),
	}
	if st.node.NodeNumber != nil {
		attrs = append(attrs, slog.Int("node_number", *st.node.NodeNumber))
	}
	return attrs
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
			return nil, fmt.Errorf("%s: unknown type %q", rawNodeRef(n), n.Type)
		}

		noder, err := factory(n)
		if err != nil {
			return nil, fmt.Errorf("%s: create failed: %w", rawNodeRef(n), err)
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

	// Validate port type compatibility on all edges.
	// Build a port-meta lookup from nodes that implement NodeMetaProvider.
	type portKey struct {
		nodeID   string
		portName string
	}
	portTypes := make(map[portKey]PortMeta)
	for id, st := range states {
		mp, ok := st.noder.(NodeMetaProvider)
		if !ok {
			continue
		}
		meta := mp.Meta()
		for _, p := range meta.Inputs {
			portTypes[portKey{id, p.Name}] = p
		}
		for _, p := range meta.Outputs {
			portTypes[portKey{id, p.Name}] = p
		}
	}

	// Check each edge for port type compatibility.
	for _, edge := range graph.Edges {
		srcState, ok := states[edge.Source]
		if !ok {
			continue
		}
		tgtState, ok := states[edge.Target]
		if !ok {
			continue
		}

		srcPort := edge.SourceHandle
		if srcPort == "" {
			srcPort = "output"
		}
		tgtPort := edge.TargetHandle
		if tgtPort == "" {
			tgtPort = "input"
		}

		srcMeta, srcHasMeta := portTypes[portKey{edge.Source, srcPort}]
		tgtMeta, tgtHasMeta := portTypes[portKey{edge.Target, tgtPort}]

		// Only validate when both ends have declared metadata.
		if srcHasMeta && tgtHasMeta {
			if !PortsCompatible(srcMeta.Type, tgtMeta.Type, tgtMeta.Accept) {
				return nil, fmt.Errorf(
					"incompatible connection: %s port %q (%s) → %s port %q (%s)",
					nodeRef(srcState), srcPort, srcMeta.Type,
					nodeRef(tgtState), tgtPort, tgtMeta.Type,
				)
			}
		}
	}

	// Validate all nodes.
	for _, st := range states {
		if err := st.noder.Validate(ctx, reg); err != nil {
			return nil, fmt.Errorf("%s: validation failed: %w", nodeRef(st), err)
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

	reg := NewRegistryWithDependencies(e.ensureDependencies(), inputs)
	reg.engine = e

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

	// Phase 2: execute the non-fan-out graph in topological order. Fan-out
	// descendants are owned by their branch and never also run on this path.
	nodeOutputs := make(map[string]NodeResult, len(graph.Nodes))
	fanOutOwned := make(map[string]bool)
	var fanOuts []fanOutExecution

	for _, nodeID := range order {
		// Check for cancellation between node executions.
		if err := ctx.Err(); err != nil {
			err = fmt.Errorf("workflow cancelled: %w", err)
			signalOutput(nil, err)
			return nil, err
		}

		st, ok := states[nodeID]
		if !ok || fanOutOwned[nodeID] {
			continue
		}

		nodeInputs, active := e.gatherInputs(nodeID, states, nodeOutputs)
		if !active {
			e.emitSkipped(st)
			continue
		}

		result, stopped, err := e.executeNode(ctx, st, reg, nodeInputs, signalOutput)
		if err != nil {
			signalOutput(nil, err)
			return nil, err
		}
		if stopped || result == nil {
			continue
		}

		if fanOut, ok := result.(NodeResultFanOut); ok {
			items := fanOut.Items()
			if len(items) > 0 {
				fanOuts = append(fanOuts, fanOutExecution{sourceNodeID: nodeID, items: items})
				for downstreamID := range e.findDownstream(nodeID, states) {
					fanOutOwned[downstreamID] = true
				}
			}
			continue
		}

		nodeOutputs[nodeID] = result
	}

	for _, fanOut := range fanOuts {
		if err := e.runFanOut(ctx, fanOut, states, order, reg, nodeOutputs, signalOutput); err != nil {
			signalOutput(nil, err)
			return nil, err
		}
	}

	// Collect outputs from Output nodes via the registry.
	outputs := reg.Outputs()

	// Signal with final outputs if no output node fired earlier.
	signalOutput(outputs, nil)

	return &RunResult{Outputs: outputs}, nil
}

type fanOutExecution struct {
	sourceNodeID string
	items        []map[string]any
}

type outputSignal func(map[string]any, error)

// executeNode is the single execution path for main and fan-out nodes.
func (e *Engine) executeNode(ctx context.Context, st *nodeState, reg *Registry, inputs map[string]any, signalOutput outputSignal) (NodeResult, bool, error) {
	e.emitEvent(NodeEvent{
		NodeID:    st.node.ID,
		NodeType:  st.noder.Type(),
		EventType: "started",
	})
	logi.Ctx(ctx).Debug("node started", nodeLogAttrs(st)...)

	startTime := time.Now()
	result, err := st.noder.Run(ctx, reg, inputs)
	durationMs := time.Since(startTime).Milliseconds()
	if err != nil {
		if errors.Is(err, ErrStopBranch) {
			e.emitEvent(NodeEvent{
				NodeID:     st.node.ID,
				NodeType:   st.noder.Type(),
				EventType:  "skipped",
				DurationMs: durationMs,
			})
			return nil, true, nil
		}

		e.emitEvent(NodeEvent{
			NodeID:     st.node.ID,
			NodeType:   st.noder.Type(),
			EventType:  "error",
			Error:      err.Error(),
			DurationMs: durationMs,
		})
		return nil, false, fmt.Errorf("%s: %w", nodeRef(st), err)
	}
	logi.Ctx(ctx).Debug("node completed", nodeLogAttrs(st)...)

	completedEvent := NodeEvent{
		NodeID:     st.node.ID,
		NodeType:   st.noder.Type(),
		EventType:  "completed",
		DurationMs: durationMs,
	}
	if result != nil {
		completedEvent.Data = truncateOutputData(result.Data())
	}
	e.emitEvent(completedEvent)

	if st.noder.Type() == "output" {
		signalOutput(reg.Outputs(), nil)
	}

	return result, false, nil
}

func (e *Engine) emitSkipped(st *nodeState) {
	e.emitEvent(NodeEvent{
		NodeID:    st.node.ID,
		NodeType:  st.noder.Type(),
		EventType: "skipped",
	})
}

// gatherInputs collects upstream data and reports whether the node is active.
// Root nodes are active by definition; other nodes require at least one active
// incoming edge.
func (e *Engine) gatherInputs(nodeID string, states map[string]*nodeState, nodeOutputs map[string]NodeResult) (map[string]any, bool) {
	st := states[nodeID]
	if st == nil {
		return make(map[string]any), false
	}

	result := make(map[string]any)
	active := len(st.inputs) == 0

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
				active = true
				// Selection port names route the whole node result; they are not
				// required to also exist as keys in the result payload. Nodes such
				// as exec and conditional return fields like stdout/result while
				// selecting "always" or "true" as the active edge.
				if val, exists := upstreamData[conn.port]; exists {
					result[tgtPort] = val
				} else {
					result[tgtPort] = upstreamData
				}
				continue
			}

			active = true

			// Map source port data to target port.
			if val, exists := upstreamData[conn.port]; exists {
				result[tgtPort] = val
			} else if conn.port == "output" {
				// Legacy persisted graphs used the generic "output" handle for
				// input nodes. The current input node exposes that payload as
				// "data". Preserve those graphs without rewriting every version.
				if val, exists := upstreamData["data"]; exists {
					result[tgtPort] = val
				}
			}
			// If the specific port doesn't exist in data, skip it.
			// Strict port matching: we only use data from the exact port key.
		}
	}

	return result, active
}

// isPortActive checks whether a specific output port is active given selection port names.
func (e *Engine) isPortActive(portName string, _ *nodeState, selection []string) bool {
	for _, name := range selection {
		if name == portName {
			return true
		}
	}

	return false
}

// runFanOut executes each item concurrently, then merges branch outputs in item
// order. This preserves fan-out concurrency while making duplicate output keys
// deterministic: later items win, matching Registry.SetOutputs semantics.
func (e *Engine) runFanOut(ctx context.Context, fanOut fanOutExecution, states map[string]*nodeState, order []string, reg *Registry, baseOutputs map[string]NodeResult, signalOutput outputSignal) error {
	branchRegs := make([]*Registry, len(fanOut.items))
	errs := make([]error, len(fanOut.items))
	var wg sync.WaitGroup

	for i, item := range fanOut.items {
		branchRegs[i] = reg.newBranch()
		wg.Add(1)
		go func(index int, data map[string]any) {
			defer wg.Done()
			errs[index] = e.runFanOutBranch(ctx, fanOut.sourceNodeID, data, states, order, branchRegs[index], baseOutputs, signalOutput)
		}(i, item)
	}
	wg.Wait()

	for i, branchReg := range branchRegs {
		if errs[i] == nil {
			reg.SetOutputs(branchReg.Outputs())
		}
	}
	for i, err := range errs {
		if err != nil {
			st := states[fanOut.sourceNodeID]
			logi.Ctx(ctx).Error("fan-out branch failed", append(nodeLogAttrs(st), "item_index", i, "error", err)...)
			return err
		}
	}

	return nil
}

// runFanOutBranch executes downstream nodes for a single fan-out item.
func (e *Engine) runFanOutBranch(ctx context.Context, sourceNodeID string, data map[string]any, states map[string]*nodeState, order []string, reg *Registry, baseOutputs map[string]NodeResult, signalOutput outputSignal) error {
	downstream := e.findDownstream(sourceNodeID, states)
	branchOutputs := cloneNodeOutputs(baseOutputs)
	branchOutputs[sourceNodeID] = NewSelectionResult(data, outputPorts(states[sourceNodeID]))
	fanOutOwned := make(map[string]bool)
	var fanOuts []fanOutExecution

	for _, nodeID := range order {
		if !downstream[nodeID] || fanOutOwned[nodeID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("workflow cancelled: %w", err)
		}

		st := states[nodeID]
		if st == nil {
			continue
		}

		nodeInputs, active := e.gatherInputs(nodeID, states, branchOutputs)
		if !active {
			e.emitSkipped(st)
			continue
		}

		result, stopped, err := e.executeNode(ctx, st, reg, nodeInputs, signalOutput)
		if err != nil {
			return err
		}
		if stopped || result == nil {
			continue
		}

		if nested, ok := result.(NodeResultFanOut); ok {
			items := nested.Items()
			if len(items) > 0 {
				fanOuts = append(fanOuts, fanOutExecution{sourceNodeID: nodeID, items: items})
				for downstreamID := range e.findDownstream(nodeID, states) {
					fanOutOwned[downstreamID] = true
				}
			}
			continue
		}

		branchOutputs[nodeID] = result
	}

	for _, nested := range fanOuts {
		if err := e.runFanOut(ctx, nested, states, order, reg, branchOutputs, signalOutput); err != nil {
			return err
		}
	}

	return nil
}

func cloneNodeOutputs(outputs map[string]NodeResult) map[string]NodeResult {
	cloned := make(map[string]NodeResult, len(outputs))
	for nodeID, result := range outputs {
		cloned[nodeID] = result
	}
	return cloned
}

func outputPorts(st *nodeState) []string {
	if st == nil {
		return nil
	}
	ports := make([]string, 0, len(st.outputs))
	for port := range st.outputs {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	return ports
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
// nodes. It uses a two-phase BFS:
//
//  1. Forward BFS — walk edges source→target from entry nodes to discover
//     all downstream nodes.
//  2. Reverse BFS — walk edges target→source from every reachable node to
//     include upstream dependencies (e.g. resource config nodes like
//     skill_config and mcp_config that feed into agent_call via
//     bottom-handle edges but have no incoming edges themselves).
//
// This ensures that resource config nodes attached to reachable nodes are
// included in the execution set even though no entry node feeds into them.
//
// If entryNodeIDs is empty, it falls back to seeding from all known start
// types (input, http_trigger, cron_trigger) for backward compatibility.
func reachableNodes(entryNodeIDs []string, nodes []service.WorkflowNode, edges []service.WorkflowEdge) map[string]bool {
	// Build forward and reverse adjacency from edges.
	fwdAdj := make(map[string][]string)
	revAdj := make(map[string][]string)
	for _, e := range edges {
		fwdAdj[e.Source] = append(fwdAdj[e.Source], e.Target)
		revAdj[e.Target] = append(revAdj[e.Target], e.Source)
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
		// Fallback: seed from all input entry points.
		startTypes := map[string]bool{
			"input": true,
		}
		for _, n := range nodes {
			if startTypes[n.Type] {
				reachable[n.ID] = true
				queue = append(queue, n.ID)
			}
		}
	}

	// Phase 1: BFS forward through edges (source → target).
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range fwdAdj[current] {
			if !reachable[next] {
				reachable[next] = true
				queue = append(queue, next)
			}
		}
	}

	// Phase 2: BFS reverse through edges (target → source).
	// For every reachable node, also include its upstream dependencies.
	// This captures resource config nodes that connect into reachable nodes
	// but are not themselves downstream of any entry node.
	var revQueue []string
	for id := range reachable {
		revQueue = append(revQueue, id)
	}
	for len(revQueue) > 0 {
		current := revQueue[0]
		revQueue = revQueue[1:]
		for _, src := range revAdj[current] {
			if !reachable[src] {
				reachable[src] = true
				revQueue = append(revQueue, src)
			}
		}
	}

	return reachable
}

// truncateOutputData creates a shallow copy of node output data suitable
// for SSE streaming. Large string values are truncated and large slices
// are capped to keep event payloads reasonable.
func truncateOutputData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	const maxStringLen = 500
	const maxSliceLen = 5

	out := make(map[string]any, len(data))
	for k, v := range data {
		switch val := v.(type) {
		case string:
			if len(val) > maxStringLen {
				out[k] = val[:maxStringLen] + "..."
			} else {
				out[k] = val
			}
		case []any:
			if len(val) > maxSliceLen {
				out[k] = val[:maxSliceLen]
			} else {
				out[k] = val
			}
		default:
			out[k] = v
		}
	}
	return out
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
	for id := range adjacency {
		sort.Strings(adjacency[id])
	}

	var queue []string
	for id := range reachable {
		if inDegree[id] == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

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
		sort.Strings(queue)
	}

	if len(order) != len(reachable) {
		return nil, fmt.Errorf("workflow graph contains a cycle")
	}

	return order, nil
}
