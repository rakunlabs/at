package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/rytsh/mugo/templatex"

	"github.com/rakunlabs/at/internal/render"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// templateNode renders a Go text/template with the upstream data as context.
//
// Config (node.Data):
//
//	"template": string — the Go template text (required)
//
// Input ports:  "data" — upstream data used as template context
// Output ports: "text" — the rendered string
type templateNode struct {
	tmplText string
}

func init() {
	workflow.RegisterNodeType("template", newTemplateNode)
}

func newTemplateNode(node service.WorkflowNode) (workflow.Noder, error) {
	raw, ok := node.Data["template"]
	if !ok {
		return nil, fmt.Errorf("template: missing 'template' in node data")
	}

	tmplText, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("template: 'template' must be a string")
	}

	return &templateNode{tmplText: tmplText}, nil
}

func (n *templateNode) Type() string { return "template" }

func (n *templateNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if strings.TrimSpace(n.tmplText) == "" {
		return fmt.Errorf("template: template text is empty")
	}
	return nil
}

func (n *templateNode) Run(_ context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// If there is a single "data" key holding a map, use its contents
	// directly as the template context so users can write {{.field}}
	// instead of {{.data.field}}.
	ctx := any(inputs)
	if len(inputs) == 1 {
		if data, ok := inputs["data"]; ok {
			if m, ok := data.(map[string]any); ok {
				ctx = m
			}
		}
	}

	result, err := render.ExecuteWithData(n.tmplText, ctx, templatex.WithExecFuncMap(varFuncMap(reg)))
	if err != nil {
		return nil, fmt.Errorf("template: execute error: %w", err)
	}

	return workflow.NewResult(map[string]any{
		"text": string(result),
	}), nil
}
