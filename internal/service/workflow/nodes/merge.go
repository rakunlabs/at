package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type mergeNode struct {
	Mode     string `json:"mode"`
	JoinType string `json:"join_type"`
	LeftKey  string `json:"left_key"`
	RightKey string `json:"right_key"`
}

func init() {
	workflow.RegisterNodeType("merge", newMergeNode)
	service.RegisterExecutionCapability("node", "merge", false)
}

func newMergeNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &mergeNode{Mode: "append", JoinType: "inner", LeftKey: "/id", RightKey: "/id"}
	if err := decodeDataConfig(node.Data, n); err != nil {
		return nil, err
	}
	return n, nil
}
func (*mergeNode) Type() string { return "merge" }
func (*mergeNode) Meta() workflow.NodeMeta {
	return workflow.NodeMeta{Type: "merge", Label: "Merge", Category: "flow_control", Description: "Combine two inputs in this invocation. Append items or emit {left,right} pairs by position/key; not a Loop collector.", Inputs: []workflow.PortMeta{
		{Name: "left", Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "Left", Position: "left"},
		{Name: "right", Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "Right", Position: "left"},
	}, Outputs: []workflow.PortMeta{{Name: "data", Type: workflow.PortTypeData, Label: "Data", Position: "right"}}, Fields: []workflow.FieldMeta{
		{Name: "mode", Type: "string", Default: "append", Enum: []string{"append", "zip", "join"}},
		{Name: "join_type", Type: "string", Default: "inner", Enum: []string{"inner", "left", "outer"}},
		{Name: "left_key", Type: "string", Default: "/id", Description: "JSON Pointer relative to each left item"},
		{Name: "right_key", Type: "string", Default: "/id", Description: "JSON Pointer relative to each right item"},
	}}
}
func (n *mergeNode) Validate(context.Context, *workflow.Registry) error {
	if n.Mode != "append" && n.Mode != "zip" && n.Mode != "join" {
		return fmt.Errorf("merge: mode must be append, zip or join")
	}
	if n.JoinType != "inner" && n.JoinType != "left" && n.JoinType != "outer" {
		return fmt.Errorf("merge: join_type must be inner, left or outer")
	}
	if n.Mode != "join" {
		return nil
	}
	for _, path := range []string{n.LeftKey, n.RightKey} {
		if err := workflow.ValidateJSONPointer(path); err != nil {
			return fmt.Errorf("merge: join key: %w", err)
		}
	}
	return nil
}

func mergeKey(value any, path string) (string, bool, error) {
	key, err := workflow.ResolveJSONPointer(value, path)
	if err != nil || key == nil {
		return "", false, nil
	}
	if number, ok := numericValue(key); ok {
		return "number:" + number.RatString(), true, nil
	}
	switch key.(type) {
	case string, bool:
		encoded, _ := json.Marshal(key)
		return string(encoded), true, nil
	default:
		return "", false, fmt.Errorf("join key at %q must be a non-null scalar", path)
	}
}

func (n *mergeNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	var left, right []any
	var err error
	if value, ok := inputs["left"]; ok {
		left, err = dataItems(value, true)
		if err != nil {
			return nil, fmt.Errorf("merge: left: %w", err)
		}
	}
	if value, ok := inputs["right"]; ok {
		right, err = dataItems(value, true)
		if err != nil {
			return nil, fmt.Errorf("merge: right: %w", err)
		}
	}
	result := make([]any, 0)
	add := func(value any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(result) >= maxDataItems {
			return fmt.Errorf("merge: result exceeds %d items; reduce duplicate join keys or input size", maxDataItems)
		}
		result = append(result, value)
		return nil
	}
	if n.Mode == "append" {
		for _, items := range [][]any{left, right} {
			for _, item := range items {
				if err := add(item); err != nil {
					return nil, err
				}
			}
		}
	} else if n.Mode == "zip" {
		count := max(len(left), len(right))
		for i := 0; i < count; i++ {
			pair := map[string]any{"left": nil, "right": nil}
			if i < len(left) {
				pair["left"] = left[i]
			}
			if i < len(right) {
				pair["right"] = right[i]
			}
			if err := add(pair); err != nil {
				return nil, err
			}
		}
	} else {
		index := make(map[string][]int)
		used := make([]bool, len(right))
		for i, item := range right {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			key, present, err := mergeKey(item, n.RightKey)
			if err != nil {
				return nil, fmt.Errorf("merge: right item %d: %w", i, err)
			}
			if present {
				index[key] = append(index[key], i)
			}
		}
		for i, item := range left {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			key, present, err := mergeKey(item, n.LeftKey)
			if err != nil {
				return nil, fmt.Errorf("merge: left item %d: %w", i, err)
			}
			var matches []int
			if present {
				matches = index[key]
			}
			for _, j := range matches {
				used[j] = true
				if err := add(map[string]any{"left": item, "right": right[j]}); err != nil {
					return nil, err
				}
			}
			if len(matches) == 0 && n.JoinType != "inner" {
				if err := add(map[string]any{"left": item, "right": nil}); err != nil {
					return nil, err
				}
			}
		}
		if n.JoinType == "outer" {
			for i, item := range right {
				if !used[i] {
					if err := add(map[string]any{"left": nil, "right": item}); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return workflow.NewResult(map[string]any{"data": result}), nil
}
