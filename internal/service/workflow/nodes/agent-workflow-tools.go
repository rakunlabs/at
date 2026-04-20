package nodes

import (
	"regexp"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// reNonAlnumWF mirrors the server-side slug regex used for `wf_*` tool names,
// kept local so the nodes package has no server dependency.
var reNonAlnumWF = regexp.MustCompile(`[^a-z0-9]+`)

// workflowToolNameFor mirrors internal/server.workflowToolName — duplicated
// to avoid a package cycle (nodes cannot import server). Tool names MUST stay
// in sync with the server-side implementation so MCP-exposed and agent-attached
// workflows look identical to the LLM.
func workflowToolNameFor(wf *service.Workflow) string {
	name := strings.ToLower(strings.TrimSpace(wf.Name))
	name = reNonAlnumWF.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = wf.ID
	}
	return "wf_" + name
}

// buildWorkflowToolDef builds the LLM-facing tool definition for a workflow
// attached to an agent via AgentConfig.Workflows.
func buildWorkflowToolDef(wf *service.Workflow) service.Tool {
	desc := wf.Description
	if desc == "" {
		desc = "Run workflow: " + wf.Name
	}

	// Entry-point labels (from `input` nodes) help the LLM pick an entry.
	var entryLabels []string
	for _, n := range wf.Graph.Nodes {
		if n.Type != "input" {
			continue
		}
		label, _ := n.Data["label"].(string)
		if label == "" {
			label = n.ID
		}
		entryLabels = append(entryLabels, label)
	}
	if len(entryLabels) > 0 {
		desc += " | Available entries: " + strings.Join(entryLabels, ", ")
	}

	return service.Tool{
		Name:        workflowToolNameFor(wf),
		Description: desc,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"entry": map[string]any{
					"type":        "string",
					"description": "Name of the input node to enter (the label of the input node). If omitted, all input nodes are triggered.",
				},
				"inputs": map[string]any{
					"type":        "object",
					"description": "Key-value inputs to pass to the workflow",
				},
			},
		},
	}
}
