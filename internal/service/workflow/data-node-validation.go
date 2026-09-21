package workflow

import "fmt"

// Data nodes use one producer per named input. Older arbitrary multi-input
// nodes retain their historical behavior; these new nodes must not silently
// overwrite one producer with another or imply a global fan-out barrier.
func validateDataNodeInputs(states map[string]*nodeState) error {
	for _, st := range states {
		switch st.node.Type {
		case "edit_fields", "filter", "switch", "merge", "aggregate":
		default:
			continue
		}
		for port, sources := range st.inputs {
			allowed := port == "data"
			if st.node.Type == "merge" {
				allowed = port == "left" || port == "right"
			}
			if !allowed || len(sources) > 1 {
				return fmt.Errorf("%s: use one connection per declared input port; invalid or duplicate input %q", nodeRef(st), port)
			}
		}
		if st.node.Type != "merge" {
			continue
		}
		ancestors := func(start string) map[string]bool {
			seen := map[string]bool{start: true}
			queue := []string{start}
			for len(queue) > 0 {
				id := queue[0]
				queue = queue[1:]
				if current := states[id]; current != nil {
					for _, sources := range current.inputs {
						for _, source := range sources {
							if !seen[source.nodeID] {
								seen[source.nodeID] = true
								queue = append(queue, source.nodeID)
							}
						}
					}
				}
			}
			return seen
		}
		var loops []string
		for id := range ancestors(st.node.ID) {
			if node := states[id]; node != nil && node.node.Type == "loop" {
				loops = append(loops, id)
			}
		}
		for i, left := range loops {
			for _, right := range loops[i+1:] {
				if !ancestors(left)[right] && !ancestors(right)[left] {
					return fmt.Errorf("%s: independent Loop streams cannot be merged per invocation; merge arrays before fan-out", nodeRef(st))
				}
			}
		}
	}
	return nil
}
