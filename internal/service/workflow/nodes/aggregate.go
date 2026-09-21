package nodes

import (
	"context"
	"fmt"
	"math"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type aggregateNode struct {
	Operation string `json:"operation"`
	ItemsPath string `json:"items_path"`
	FieldPath string `json:"field_path"`
}

func init() {
	workflow.RegisterNodeType("aggregate", newAggregateNode)
	service.RegisterExecutionCapability("node", "aggregate", false)
}

func newAggregateNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &aggregateNode{Operation: "collect"}
	if err := decodeDataConfig(node.Data, n); err != nil {
		return nil, err
	}
	return n, nil
}
func (*aggregateNode) Type() string { return "aggregate" }
func (*aggregateNode) Meta() workflow.NodeMeta {
	inputs, outputs := standardDataPorts()
	return workflow.NodeMeta{Type: "aggregate", Label: "Aggregate", Category: "processing", Description: "Summarize an array in this invocation as {value,count}; not a collector across Loop invocations", Inputs: inputs, Outputs: outputs, Fields: []workflow.FieldMeta{
		{Name: "operation", Type: "string", Default: "collect", Enum: []string{"collect", "count", "sum", "average", "min", "max"}},
		{Name: "items_path", Type: "string", Description: "JSON Pointer to an array inside data; empty selects data itself"},
		{Name: "field_path", Type: "string", Description: "JSON Pointer to a value in each item; empty selects the whole item; ignored for count"},
	}}
}
func (n *aggregateNode) Validate(context.Context, *workflow.Registry) error {
	switch n.Operation {
	case "collect", "count", "sum", "average", "min", "max":
	default:
		return fmt.Errorf("aggregate: unknown operation %q", n.Operation)
	}
	paths := []string{n.ItemsPath}
	if n.Operation != "count" {
		paths = append(paths, n.FieldPath)
	}
	for _, path := range paths {
		if err := workflow.ValidateJSONPointer(path); err != nil {
			return fmt.Errorf("aggregate: %w", err)
		}
	}
	return nil
}
func (n *aggregateNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	value, err := dataInput(inputs)
	if err != nil {
		return nil, err
	}
	value, err = workflow.ResolveJSONPointer(value, n.ItemsPath)
	if err != nil {
		return nil, fmt.Errorf("aggregate: items_path: %w", err)
	}
	items, err := dataItems(value, false)
	if err != nil {
		return nil, fmt.Errorf("aggregate: %w", err)
	}
	var result any
	collected := make([]any, 0, len(items))
	numeric := float64(0)
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if n.Operation == "count" {
			continue
		}
		field, err := workflow.ResolveJSONPointer(item, n.FieldPath)
		if err != nil {
			return nil, fmt.Errorf("aggregate: item %d field: %w", i, err)
		}
		if n.Operation == "collect" {
			collected = append(collected, field)
			continue
		}
		number, err := finiteNumber(field)
		if err != nil {
			return nil, fmt.Errorf("aggregate: item %d: %w", i, err)
		}
		switch n.Operation {
		case "sum", "average":
			numeric += number
		case "min":
			if i == 0 || number < numeric {
				numeric = number
			}
		case "max":
			if i == 0 || number > numeric {
				numeric = number
			}
		}
		if math.IsInf(numeric, 0) || math.IsNaN(numeric) {
			return nil, fmt.Errorf("aggregate: numeric result exceeds the supported range")
		}
	}
	switch n.Operation {
	case "collect":
		result = collected
	case "count":
		result = len(items)
	case "sum":
		result = numeric
	case "average":
		if len(items) > 0 {
			result = numeric / float64(len(items))
		}
	case "min", "max":
		if len(items) > 0 {
			result = numeric
		}
	}
	return workflow.NewResult(map[string]any{"data": map[string]any{"value": result, "count": len(items)}}), nil
}
