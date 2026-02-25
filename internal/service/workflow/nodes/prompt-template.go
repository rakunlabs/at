package nodes

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// promptTemplateNode renders a Go text/template with the upstream data as
// context. It converts simple {{variable}} mustache syntax to {{.variable}}
// so users don't need to know Go template syntax.
//
// Config (node.Data):
//
//	"template": string — the template text (required)
//
// Input ports:  "data" — upstream data used as template context
// Output ports: "text" — the rendered string
type promptTemplateNode struct {
	tmplText string
}

func init() {
	workflow.RegisterNodeType("prompt_template", newPromptTemplateNode)
}

func newPromptTemplateNode(node service.WorkflowNode) (workflow.Noder, error) {
	raw, ok := node.Data["template"]
	if !ok {
		return nil, fmt.Errorf("prompt_template: missing 'template' in node data")
	}

	tmplText, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("prompt_template: 'template' must be a string")
	}

	return &promptTemplateNode{tmplText: tmplText}, nil
}

func (n *promptTemplateNode) Type() string { return "prompt_template" }

func (n *promptTemplateNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if strings.TrimSpace(n.tmplText) == "" {
		return fmt.Errorf("prompt_template: template text is empty")
	}
	return nil
}

func (n *promptTemplateNode) Run(_ context.Context, _ *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Convert {{variable}} → {{.variable}} for convenience.
	converted := convertMustache(n.tmplText)

	tmpl, err := template.New("prompt").Parse(converted)
	if err != nil {
		return nil, fmt.Errorf("prompt_template: parse error: %w", err)
	}

	// If there is a single "data" key holding a map, use its contents
	// directly as the template context so users can write {{field}}
	// instead of {{.data.field}}.
	ctx := inputs
	if len(inputs) == 1 {
		if data, ok := inputs["data"]; ok {
			if m, ok := data.(map[string]any); ok {
				ctx = m
			}
		}
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("prompt_template: execute error: %w", err)
	}

	return workflow.NewResult(map[string]any{
		"text": buf.String(),
	}), nil
}

// convertMustache converts simple {{variable}} syntax to Go template
// {{.variable}}, but leaves already-dotted references unchanged.
func convertMustache(s string) string {
	var result []byte
	i := 0
	for i < len(s) {
		if i+2 < len(s) && s[i] == '{' && s[i+1] == '{' {
			end := -1
			for j := i + 2; j < len(s)-1; j++ {
				if s[j] == '}' && s[j+1] == '}' {
					end = j
					break
				}
			}
			if end >= 0 {
				inner := strings.TrimSpace(s[i+2 : end])
				if inner != "" && inner[0] != '.' && inner[0] != '$' &&
					!strings.HasPrefix(inner, "range") &&
					!strings.HasPrefix(inner, "if") &&
					!strings.HasPrefix(inner, "end") &&
					!strings.HasPrefix(inner, "else") &&
					!strings.HasPrefix(inner, "with") &&
					!strings.HasPrefix(inner, "block") &&
					!strings.HasPrefix(inner, "define") &&
					!strings.HasPrefix(inner, "template") {
					result = append(result, '{', '{', '.')
					result = append(result, []byte(inner)...)
					result = append(result, '}', '}')
					i = end + 2
					continue
				}
			}
		}
		result = append(result, s[i])
		i++
	}
	return string(result)
}
