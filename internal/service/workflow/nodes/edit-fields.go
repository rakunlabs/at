package nodes

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type editField struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
	dataLiteral
}
type editFieldsNode struct {
	KeepInput bool        `json:"keep_input"`
	Fields    []editField `json:"fields"`
}

func init() {
	workflow.RegisterNodeType("edit_fields", newEditFieldsNode)
	service.RegisterExecutionCapability("node", "edit_fields", false)
}

func newEditFieldsNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &editFieldsNode{KeepInput: true}
	if err := decodeDataConfig(node.Data, n); err != nil {
		return nil, err
	}
	return n, nil
}
func (*editFieldsNode) Type() string { return "edit_fields" }
func (*editFieldsNode) Meta() workflow.NodeMeta {
	inputs, outputs := standardDataPorts()
	return workflow.NodeMeta{Type: "edit_fields", Label: "Edit Fields", Category: "processing", Description: "Set top-level fields on an object or each object in an array, using literals or JSON Pointer paths", Inputs: inputs, Outputs: outputs, Fields: []workflow.FieldMeta{
		{Name: "keep_input", Type: "boolean", Default: true, Description: "Keep fields not explicitly assigned"},
		{Name: "fields", Type: "array", Description: "Assignments: {name,source:value|path,path,value_type:string|number|boolean|null|json,value:string}. Paths refer to each original item."},
	}}
}
func (n *editFieldsNode) Validate(context.Context, *workflow.Registry) error {
	if len(n.Fields) > 64 {
		return fmt.Errorf("edit_fields: at most 64 assignments are supported")
	}
	seen := map[string]bool{}
	for i, field := range n.Fields {
		if strings.TrimSpace(field.Name) == "" || len(field.Name) > 256 || seen[field.Name] {
			return fmt.Errorf("edit_fields: assignment %d needs a unique, nonempty field name (up to 256 bytes)", i+1)
		}
		seen[field.Name] = true
		switch field.Source {
		case "path":
			if err := workflow.ValidateJSONPointer(field.Path); err != nil {
				return fmt.Errorf("edit_fields: %s: %w", field.Name, err)
			}
		case "", "value":
			if _, err := field.parse(); err != nil {
				return fmt.Errorf("edit_fields: %s: %w", field.Name, err)
			}
		default:
			return fmt.Errorf("edit_fields: unknown source %q", field.Source)
		}
	}
	return nil
}
func (n *editFieldsNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	value, err := dataInput(inputs)
	if err != nil {
		return nil, err
	}
	items, err := dataItems(value, true)
	if err != nil {
		return nil, err
	}
	result := make([]any, 0, len(items))
	for i, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		original, err := dataObject(item)
		if err != nil {
			return nil, fmt.Errorf("edit_fields: item %d: %w", i, err)
		}
		out := make(map[string]any)
		if n.KeepInput {
			for key, v := range original {
				out[key] = v
			}
		}
		for _, field := range n.Fields {
			var assigned any
			if field.Source == "path" {
				assigned, err = workflow.ResolveJSONPointer(original, field.Path)
			} else {
				assigned, err = field.parse()
			}
			if err != nil {
				return nil, fmt.Errorf("edit_fields: item %d field %q: %w", i, field.Name, err)
			}
			out[field.Name] = assigned
		}
		result = append(result, out)
	}
	kind := reflect.ValueOf(value).Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return workflow.NewResult(map[string]any{"data": result[0]}), nil
	}
	return workflow.NewResult(map[string]any{"data": result}), nil
}
