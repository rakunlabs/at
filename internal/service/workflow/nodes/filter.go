package nodes

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type filterNode struct {
	ItemsPath  string          `json:"items_path"`
	Match      string          `json:"match"`
	Conditions []dataCondition `json:"conditions"`
}

func init() {
	workflow.RegisterNodeType("filter", newFilterNode)
	service.RegisterExecutionCapability("node", "filter", false)
}

func newFilterNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &filterNode{Match: "all"}
	if err := decodeDataConfig(node.Data, n); err != nil {
		return nil, err
	}
	return n, nil
}
func (*filterNode) Type() string { return "filter" }
func (*filterNode) Meta() workflow.NodeMeta {
	inputs, outputs := standardDataPorts()
	return workflow.NodeMeta{Type: "filter", Label: "Filter", Category: "processing", Description: "Keep matching items; emits an array, including [] when nothing matches", Inputs: inputs, Outputs: outputs, Fields: []workflow.FieldMeta{
		{Name: "items_path", Type: "string", Description: "JSON Pointer to items in data; empty selects data itself"},
		{Name: "match", Type: "string", Default: "all", Enum: []string{"all", "any"}},
		{Name: "conditions", Type: "array", Required: true, Description: "{path,operator:eq|neq|gt|gte|lt|lte|contains|exists|not_exists|is_empty,value_type,value:string}; each path is relative to an item"},
	}}
}
func (n *filterNode) Validate(context.Context, *workflow.Registry) error {
	if n.Match != "all" && n.Match != "any" {
		return fmt.Errorf("filter: match must be all or any")
	}
	if err := workflow.ValidateJSONPointer(n.ItemsPath); err != nil {
		return fmt.Errorf("filter: items_path: %w", err)
	}
	if len(n.Conditions) == 0 || len(n.Conditions) > 32 {
		return fmt.Errorf("filter: configure between 1 and 32 conditions")
	}
	for i, condition := range n.Conditions {
		if err := condition.validate(); err != nil {
			return fmt.Errorf("filter: condition %d: %w", i+1, err)
		}
	}
	return nil
}
func (n *filterNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	value, err := dataInput(inputs)
	if err != nil {
		return nil, err
	}
	value, err = workflow.ResolveJSONPointer(value, n.ItemsPath)
	if err != nil {
		return nil, fmt.Errorf("filter: items_path: %w", err)
	}
	items, err := dataItems(value, true)
	if err != nil {
		return nil, err
	}
	filtered := make([]any, 0)
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		keep := n.Match == "all"
		for i, condition := range n.Conditions {
			match, err := condition.matches(item)
			if err != nil {
				return nil, fmt.Errorf("filter: condition %d: %w", i+1, err)
			}
			if n.Match == "all" && !match {
				keep = false
				break
			}
			if n.Match == "any" && match {
				keep = true
				break
			}
		}
		if keep {
			filtered = append(filtered, item)
		}
	}
	return workflow.NewResult(map[string]any{"data": filtered}), nil
}
