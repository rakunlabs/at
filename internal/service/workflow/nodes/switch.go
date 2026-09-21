package nodes

import (
	"context"
	"fmt"
	"regexp"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type switchRule struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	dataCondition
}
type switchNode struct {
	MatchMode string       `json:"match_mode"`
	Rules     []switchRule `json:"rules"`
}

var switchPortID = regexp.MustCompile(`^case_[A-Za-z0-9_-]{1,59}$`)

func init() {
	workflow.RegisterNodeType("switch", newSwitchNode)
	service.RegisterExecutionCapability("node", "switch", false)
}

func newSwitchNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &switchNode{MatchMode: "first", Rules: []switchRule{{ID: "case_1", Label: "Case 1", dataCondition: dataCondition{Path: "/status", Operator: "eq", dataLiteral: dataLiteral{ValueType: "string", Value: "ready"}}}}}
	if err := decodeDataConfig(node.Data, n); err != nil {
		return nil, err
	}
	return n, nil
}
func (*switchNode) Type() string { return "switch" }
func (n *switchNode) Meta() workflow.NodeMeta {
	inputs, _ := standardDataPorts()
	outputs := make([]workflow.PortMeta, 0, len(n.Rules)+1)
	for _, rule := range n.Rules {
		outputs = append(outputs, workflow.PortMeta{Name: rule.ID, Type: workflow.PortTypeData, Label: rule.Label, Position: "right"})
	}
	outputs = append(outputs, workflow.PortMeta{Name: "fallback", Type: workflow.PortTypeData, Label: "Fallback", Position: "right"})
	return workflow.NodeMeta{Type: "switch", Label: "Switch", Category: "flow_control", Description: "Route the whole payload to first/all matching rules, or fallback. Case IDs are stable output handles.", Inputs: inputs, Outputs: outputs, Fields: []workflow.FieldMeta{
		{Name: "match_mode", Type: "string", Default: "first", Enum: []string{"first", "all"}},
		{Name: "rules", Type: "array", Required: true, Description: "1–16 rules: {id:case_<stable-id>,label,path,operator,value_type,value:string}. Paths refer to data. Each ID adds an output handle."},
	}}
}
func (n *switchNode) Validate(context.Context, *workflow.Registry) error {
	if n.MatchMode != "first" && n.MatchMode != "all" {
		return fmt.Errorf("switch: match_mode must be first or all")
	}
	if len(n.Rules) == 0 || len(n.Rules) > 16 {
		return fmt.Errorf("switch: configure between 1 and 16 rules")
	}
	seen := map[string]bool{}
	for _, rule := range n.Rules {
		if !switchPortID.MatchString(rule.ID) || seen[rule.ID] {
			return fmt.Errorf("switch: rule IDs must be unique case_<id> identifiers up to 64 characters")
		}
		seen[rule.ID] = true
		if err := rule.validate(); err != nil {
			return fmt.Errorf("switch: %s: %w", rule.ID, err)
		}
	}
	return nil
}
func (n *switchNode) Run(ctx context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	value, err := dataInput(inputs)
	if err != nil {
		return nil, err
	}
	data := make(map[string]any)
	var selected []string
	for _, rule := range n.Rules {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		matched, err := rule.matches(value)
		if err != nil {
			return nil, fmt.Errorf("switch: %s: %w", rule.ID, err)
		}
		if matched {
			data[rule.ID] = value
			selected = append(selected, rule.ID)
			if n.MatchMode == "first" {
				break
			}
		}
	}
	if len(selected) == 0 {
		selected = []string{"fallback"}
		data["fallback"] = value
	}
	return workflow.NewSelectionResult(data, selected), nil
}
