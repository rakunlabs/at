package server

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// Graphs written through the workflow_create / workflow_update tools come
// from a model, not from the editor. Two things went wrong with them in
// practice: every node arrived at {0,0} (or a tight pile), so the canvas
// showed one stack of cards; and edges named handles that do not exist, so
// they never rendered and the run silently ignored them. These helpers
// validate wiring before anything is stored and lay the graph out the way
// the editor's own guidance describes.

// Visual-only nodes are placed by the author and never moved.
var annotationNodeTypes = map[string]bool{"sticky_note": true, "group": true}

// Resource nodes feed an agent from below rather than sitting in the flow.
var resourceNodeTypes = map[string]bool{"skill_config": true, "mcp_config": true, "agent_config": true}

const (
	layoutColumnGap = 400.0
	layoutRowGap    = 300.0
	layoutCardW     = 300.0
	layoutCardH     = 220.0
	layoutResourceY = 300.0
)

// legacyHandleAliases are handle names saved graphs used before the current
// port names; the engine still accepts them.
var legacyHandleAliases = map[string]bool{"output": true, "input": true}

// workflowPorts returns the input/output handle names of a node type, or
// ok=false for types without metadata (triggers, annotations).
func workflowPorts(nodeType string, data map[string]any) (inputs, outputs []string, ok bool) {
	factory := workflow.GetNodeFactory(nodeType)
	if factory == nil {
		return nil, nil, false
	}
	if data == nil {
		data = map[string]any{}
	}
	noder, err := factory(service.WorkflowNode{ID: "__ports__", Type: nodeType, Data: data})
	if err != nil {
		noder, err = factory(service.WorkflowNode{ID: "__ports__", Type: nodeType, Data: map[string]any{}})
		if err != nil {
			return nil, nil, false
		}
	}
	mp, isMeta := noder.(workflow.NodeMetaProvider)
	if !isMeta {
		return nil, nil, false
	}
	meta := mp.Meta()
	for _, p := range meta.Inputs {
		inputs = append(inputs, p.Name)
	}
	for _, p := range meta.Outputs {
		outputs = append(outputs, p.Name)
	}
	if policy, _ := workflow.ParseNodeExecutionPolicy(data); policy.OnError == "error_output" {
		outputs = append(outputs, workflow.NodeFailurePort)
	}
	return inputs, outputs, true
}

// validateWorkflowGraphWiring rejects edges the editor could not draw and
// the engine would not follow, naming the valid handles so the caller can
// fix the call instead of guessing.
func validateWorkflowGraphWiring(graph service.WorkflowGraph) error {
	nodes := make(map[string]service.WorkflowNode, len(graph.Nodes))
	for _, n := range graph.Nodes {
		if n.ID == "" {
			return fmt.Errorf("every node needs a non-empty id")
		}
		if _, dup := nodes[n.ID]; dup {
			return fmt.Errorf("duplicate node id %q", n.ID)
		}
		nodes[n.ID] = n
	}
	var problems []string
	adjacency := map[string][]string{}
	for _, e := range graph.Edges {
		src, okSrc := nodes[e.Source]
		tgt, okTgt := nodes[e.Target]
		switch {
		case !okSrc:
			problems = append(problems, fmt.Sprintf("edge %q: unknown source node %q", e.ID, e.Source))
			continue
		case !okTgt:
			problems = append(problems, fmt.Sprintf("edge %q: unknown target node %q", e.ID, e.Target))
			continue
		case e.Source == e.Target:
			problems = append(problems, fmt.Sprintf("edge %q: a node cannot connect to itself", e.ID))
			continue
		}
		if annotationNodeTypes[src.Type] || annotationNodeTypes[tgt.Type] {
			problems = append(problems, fmt.Sprintf("edge %q: %s nodes are visual only and cannot be connected", e.ID, firstAnnotation(src, tgt)))
			continue
		}
		if _, outs, ok := workflowPorts(src.Type, src.Data); ok && e.SourceHandle != "" && !slices.Contains(outs, e.SourceHandle) && !legacyHandleAliases[e.SourceHandle] {
			problems = append(problems, fmt.Sprintf("edge %q: %s %q has no output %q (outputs: %s)", e.ID, src.Type, src.ID, e.SourceHandle, strings.Join(outs, ", ")))
		}
		if ins, _, ok := workflowPorts(tgt.Type, tgt.Data); ok && e.TargetHandle != "" && !slices.Contains(ins, e.TargetHandle) && !legacyHandleAliases[e.TargetHandle] {
			if len(ins) == 0 {
				problems = append(problems, fmt.Sprintf("edge %q: %s %q has no inputs", e.ID, tgt.Type, tgt.ID))
			} else {
				problems = append(problems, fmt.Sprintf("edge %q: %s %q has no input %q (inputs: %s)", e.ID, tgt.Type, tgt.ID, e.TargetHandle, strings.Join(ins, ", ")))
			}
		}
		adjacency[e.Source] = append(adjacency[e.Source], e.Target)
	}
	if cycle := findGraphCycle(graph.Nodes, adjacency); cycle != "" {
		problems = append(problems, "edges form a loop through "+cycle+"; workflows must flow forward (use a loop node for repetition)")
	}
	if len(problems) > 0 {
		if len(problems) > 12 {
			problems = append(problems[:12], fmt.Sprintf("… and %d more", len(problems)-12))
		}
		return fmt.Errorf("invalid workflow graph:\n- %s", strings.Join(problems, "\n- "))
	}
	return nil
}

func firstAnnotation(a, b service.WorkflowNode) string {
	if annotationNodeTypes[a.Type] {
		return a.Type
	}
	return b.Type
}

func findGraphCycle(nodes []service.WorkflowNode, adjacency map[string][]string) string {
	const (
		unvisited = iota
		active
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(string) string
	visit = func(id string) string {
		state[id] = active
		stack = append(stack, id)
		for _, next := range adjacency[id] {
			switch state[next] {
			case active:
				start := slices.Index(stack, next)
				return strings.Join(append(slices.Clone(stack[start:]), next), " → ")
			case unvisited:
				if c := visit(next); c != "" {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[id] = done
		return ""
	}
	for _, n := range nodes {
		if state[n.ID] == unvisited {
			if c := visit(n.ID); c != "" {
				return c
			}
		}
	}
	return ""
}

// graphNeedsLayout reports whether flow nodes were left at the origin or
// stacked on top of each other, which is how model-written graphs arrive.
func graphNeedsLayout(graph service.WorkflowGraph) bool {
	var flow []service.WorkflowNode
	for _, n := range graph.Nodes {
		if !annotationNodeTypes[n.Type] && n.ParentID == "" {
			flow = append(flow, n)
		}
	}
	if len(flow) < 2 {
		return false
	}
	for i := range flow {
		for j := i + 1; j < len(flow); j++ {
			if math.Abs(flow[i].Position.X-flow[j].Position.X) < layoutCardW-20 &&
				math.Abs(flow[i].Position.Y-flow[j].Position.Y) < layoutCardH-40 {
				return true
			}
		}
	}
	return false
}

// layoutWorkflowGraph places flow nodes left to right by dependency depth,
// stacks siblings vertically in their column, puts resource config nodes
// under the agent they feed, and moves annotations (when present) above
// the flow only if they overlap it. Positions are snapped to the editor's
// 20px grid.
func layoutWorkflowGraph(graph *service.WorkflowGraph) {
	index := map[string]int{}
	for i, n := range graph.Nodes {
		index[n.ID] = i
	}
	isFlow := func(n service.WorkflowNode) bool {
		return !annotationNodeTypes[n.Type] && !resourceNodeTypes[n.Type] && n.ParentID == ""
	}

	parents := map[string][]string{}
	children := map[string][]string{}
	resourceTarget := map[string]string{}
	for _, e := range graph.Edges {
		si, okS := index[e.Source]
		ti, okT := index[e.Target]
		if !okS || !okT {
			continue
		}
		src, tgt := graph.Nodes[si], graph.Nodes[ti]
		if resourceNodeTypes[src.Type] && isFlow(tgt) {
			if _, set := resourceTarget[src.ID]; !set {
				resourceTarget[src.ID] = tgt.ID
			}
			continue
		}
		if isFlow(src) && isFlow(tgt) {
			parents[tgt.ID] = append(parents[tgt.ID], src.ID)
			children[src.ID] = append(children[src.ID], tgt.ID)
		}
	}

	// Longest-path depth gives each step its own column after all of its
	// inputs. Cycles were rejected earlier; the visited guard keeps this safe.
	depth := map[string]int{}
	var depthOf func(string, map[string]bool) int
	depthOf = func(id string, seen map[string]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if seen[id] {
			return 0
		}
		seen[id] = true
		d := 0
		for _, p := range parents[id] {
			d = max(d, depthOf(p, seen)+1)
		}
		depth[id] = d
		return d
	}
	columns := map[int][]string{}
	maxDepth := 0
	for _, n := range graph.Nodes {
		if !isFlow(n) {
			continue
		}
		d := depthOf(n.ID, map[string]bool{})
		columns[d] = append(columns[d], n.ID)
		maxDepth = max(maxDepth, d)
	}

	// Order each column by the average row of its parents so branches stay
	// next to where they come from; original order breaks ties.
	row := map[string]float64{}
	for d := 0; d <= maxDepth; d++ {
		ids := columns[d]
		key := func(id string) float64 {
			ps := parents[id]
			if len(ps) == 0 {
				return float64(index[id])
			}
			sum := 0.0
			for _, p := range ps {
				sum += row[p]
			}
			return sum / float64(len(ps))
		}
		sort.SliceStable(ids, func(a, b int) bool { return key(ids[a]) < key(ids[b]) })
		for i, id := range ids {
			row[id] = float64(i) - float64(len(ids)-1)/2
		}
		columns[d] = ids
	}

	snap := func(v float64) float64 { return math.Round(v/20) * 20 }
	maxFlowY := 0.0
	for d := 0; d <= maxDepth; d++ {
		for _, id := range columns[d] {
			n := &graph.Nodes[index[id]]
			n.Position = service.WorkflowPos{X: snap(float64(d) * layoutColumnGap), Y: snap(row[id] * layoutRowGap)}
			maxFlowY = max(maxFlowY, n.Position.Y)
		}
	}

	// Resource nodes: a band under the whole flow, each in its agent's column
	// (spread sideways when an agent has several), so they never land on a
	// branch row. Orphans start a row of their own below that band.
	resourceBand := maxFlowY + layoutResourceY
	var bandX []float64
	orphans := 0
	usedResource := false
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if !resourceNodeTypes[n.Type] || n.ParentID != "" {
			continue
		}
		if target, ok := resourceTarget[n.ID]; ok {
			t := graph.Nodes[index[target]]
			x := t.Position.X
			for _, used := range bandX {
				if math.Abs(used-x) < layoutCardW {
					x = math.Max(x, used+layoutCardW)
				}
			}
			bandX = append(bandX, x)
			n.Position = service.WorkflowPos{X: snap(x), Y: snap(resourceBand)}
			usedResource = true
			continue
		}
		orphanY := resourceBand
		if usedResource {
			orphanY += layoutRowGap
		}
		n.Position = service.WorkflowPos{X: snap(float64(orphans) * layoutColumnGap), Y: snap(orphanY)}
		orphans++
	}

	placeAnnotationsClear(graph)
}

// placeAnnotationsClear keeps every note/group the author positioned, but
// moves one that sits on top of a step into a band above the flow, so a
// note never hides a card.
func placeAnnotationsClear(graph *service.WorkflowGraph) {
	type box struct{ x, y, w, h float64 }
	var cards []box
	minX, minY := math.Inf(1), math.Inf(1)
	for _, n := range graph.Nodes {
		if annotationNodeTypes[n.Type] || n.ParentID != "" {
			continue
		}
		cards = append(cards, box{n.Position.X, n.Position.Y, layoutCardW, layoutCardH})
		minX, minY = math.Min(minX, n.Position.X), math.Min(minY, n.Position.Y)
	}
	if len(cards) == 0 {
		return
	}
	overlaps := func(b box) bool {
		for _, c := range cards {
			if b.x < c.x+c.w && c.x < b.x+b.w && b.y < c.y+c.h && c.y < b.y+b.h {
				return true
			}
		}
		return false
	}
	nextX := minX
	for i := range graph.Nodes {
		n := &graph.Nodes[i]
		if n.Type != "sticky_note" || n.ParentID != "" {
			continue
		}
		w, h := 260.0, 160.0
		if n.Width != nil {
			w = *n.Width
		}
		if n.Height != nil {
			h = *n.Height
		}
		if !overlaps(box{n.Position.X, n.Position.Y, w, h}) {
			continue
		}
		n.Position = service.WorkflowPos{X: math.Round(nextX/20) * 20, Y: math.Round((minY-h-60)/20) * 20}
		nextX += w + 40
	}
}

// prepareAgentWorkflowGraph validates a model-written graph and lays it out
// when asked to, or when its nodes would otherwise pile up.
func prepareAgentWorkflowGraph(graph *service.WorkflowGraph, layout string) (bool, error) {
	if err := validateWorkflowGraphWiring(*graph); err != nil {
		return false, err
	}
	switch layout {
	case "", "auto_if_needed":
		if !graphNeedsLayout(*graph) {
			return false, nil
		}
	case "auto":
	case "keep":
		return false, nil
	default:
		return false, fmt.Errorf("layout must be auto, auto_if_needed or keep")
	}
	layoutWorkflowGraph(graph)
	return true, nil
}
