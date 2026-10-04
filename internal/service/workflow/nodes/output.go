package nodes

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// outputNode collects final results into the registry.
// It has one input port ("input") and no output ports.
//
// Config (node.Data):
//
//	"response_mode": "json" (default) | "file" | "multipart" — how a
//	                 synchronous HTTP caller (webhook or run API with
//	                 ?sync=true) receives the result. Workflow outputs are
//	                 the same JSON map in every mode.
//	"file_path":     JSON Pointer into the gathered inputs selecting the
//	                 file(s) to send (e.g. "/input/file"). Empty: every file
//	                 reference found in the inputs.
//	"disposition":   "attachment" (default) | "inline" — file mode only.
//	"fields":        names of additional input ports (strings or {name}).
//	                 Each connected port becomes one top-level key of the
//	                 workflow outputs, so a caller (Workflow Call, the run
//	                 API) receives e.g. "text" and "file" separately.
type outputNode struct {
	mode        string
	filePath    string
	disposition string
	fields      []string
}

var outputFieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// reservedOutputFields are port names a Workflow Call node already uses for
// its own outputs, so a child field cannot shadow them there.
var reservedOutputFields = map[string]bool{"input": true, "output": true, "files": true}

// outputFieldNames reads the Output node's declared field names. Older
// graphs stored free-form display tags here, so a name that is not an
// identifier simply gets no port instead of failing saved workflows.
func outputFieldNames(raw any) []string {
	items, _ := raw.([]any)
	seen := map[string]bool{}
	var names []string
	for _, item := range items {
		var name string
		switch v := item.(type) {
		case string:
			name = v
		case map[string]any:
			name, _ = v["name"].(string)
		}
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		if !outputFieldName.MatchString(name) || reservedOutputFields[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == 32 {
			break
		}
	}
	return names
}

func init() {
	workflow.RegisterNodeType("output", newOutputNode)
}

func newOutputNode(node service.WorkflowNode) (workflow.Noder, error) {
	mode, _ := node.Data["response_mode"].(string)
	filePath, _ := node.Data["file_path"].(string)
	disposition, _ := node.Data["disposition"].(string)
	fields := outputFieldNames(node.Data["fields"])
	return &outputNode{
		mode:        strings.TrimSpace(mode),
		filePath:    strings.TrimSpace(filePath),
		disposition: strings.TrimSpace(disposition),
		fields:      fields,
	}, nil
}

func (n *outputNode) Type() string { return "output" }

func (n *outputNode) Meta() workflow.NodeMeta {
	inputs := []workflow.PortMeta{
		{Name: "input", Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: "Input", Position: "left"},
	}
	for _, field := range n.fields {
		inputs = append(inputs, workflow.PortMeta{Name: field, Type: workflow.PortTypeData, Accept: []workflow.PortType{workflow.PortTypeText}, Label: field, Position: "left"})
	}
	return workflow.NodeMeta{
		Type:        "output",
		Label:       "Output",
		Category:    "output",
		Description: "Collects final results into workflow outputs; optionally answers synchronous HTTP callers with a file or multipart body",
		Inputs:      inputs,
		Outputs:     []workflow.PortMeta{},
		Fields: []workflow.FieldMeta{
			{Name: "label", Type: "string", Required: true, Description: "Display name"},
			{Name: "fields", Type: "array", Description: "Named output fields; each adds an input port and becomes a top-level workflow output (e.g. text, file)"},
			{Name: "response_mode", Type: "string", Default: workflow.OutputResponseJSON, Enum: []string{workflow.OutputResponseJSON, workflow.OutputResponseFile, workflow.OutputResponseMultipart}, Description: "Synchronous HTTP response: JSON envelope, the file itself, or multipart/mixed with the JSON outputs plus every file"},
			{Name: "file_path", Type: "string", Description: "JSON Pointer to the file reference(s) to send, e.g. /input/file (default: all file references in the input)"},
			{Name: "disposition", Type: "string", Default: "attachment", Enum: []string{"attachment", "inline"}, Description: "Content-Disposition for file mode"},
		},
		Color: "red",
	}
}

func (n *outputNode) Validate(_ context.Context, _ *workflow.Registry) error {
	if n.mode != "" && !workflow.ValidOutputResponseMode(n.mode) {
		return fmt.Errorf("output: response_mode must be json, file or multipart")
	}
	if n.disposition != "" && n.disposition != "attachment" && n.disposition != "inline" {
		return fmt.Errorf("output: disposition must be attachment or inline")
	}
	if n.filePath != "" {
		if err := workflow.ValidateJSONPointer(n.filePath); err != nil {
			return fmt.Errorf("output: file_path: %w", err)
		}
	}
	return nil
}

// Run merges all incoming data into the registry's outputs and, in file or
// multipart mode, records which files the HTTP response carries. Files are
// checked here so a missing file fails the step instead of the HTTP reply.
func (n *outputNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	if n.mode == workflow.OutputResponseFile || n.mode == workflow.OutputResponseMultipart {
		files, err := n.responseFiles(ctx, inputs)
		if err != nil {
			return nil, err
		}
		if n.mode == workflow.OutputResponseFile && len(files) == 0 {
			return nil, fmt.Errorf("output: response_mode file needs a file reference in the input")
		}
		if n.mode == workflow.OutputResponseFile {
			files = files[:1]
		}
		disposition := n.disposition
		if disposition == "" {
			disposition = "attachment"
		}
		reg.SetResponse(workflow.NewOutputResponse(ctx, n.mode, disposition, files))
	}
	reg.SetOutputs(inputs)
	return workflow.NewResult(inputs), nil
}

func (n *outputNode) responseFiles(ctx context.Context, inputs map[string]any) ([]workflow.ResponseFile, error) {
	var source any
	if n.filePath != "" {
		value, err := workflow.ResolveJSONPointer(inputs, n.filePath)
		if err != nil {
			return nil, fmt.Errorf("output: file_path %q: %w", n.filePath, err)
		}
		source = value
	} else {
		refs := workflow.CollectFileRefs(inputs, workflow.FileRefLimit+1)
		items := make([]any, len(refs))
		for i := range refs {
			items[i] = refs[i]
		}
		source = items
	}

	runDir, _ := runWorkspaceDir(ctx)
	specs, err := parseAttachmentInput(runDir, source)
	if err != nil {
		return nil, fmt.Errorf("output: %w", err)
	}
	if len(specs) > workflow.FileRefLimit {
		return nil, fmt.Errorf("output: too many files: %d (maximum %d)", len(specs), workflow.FileRefLimit)
	}

	files := make([]workflow.ResponseFile, 0, len(specs))
	for _, spec := range specs {
		file := workflow.ResponseFile{
			WorkspacePath: spec.workspacePath,
			Name:          spec.name,
			ContentType:   spec.contentType,
			Content:       spec.inline,
			HasContent:    spec.hasInline,
		}
		if !spec.hasInline {
			if spec.workspacePath == "" {
				return nil, fmt.Errorf("output: file %q has no path", spec.name)
			}
			if err := statWorkspaceFile(ctx, spec.workspacePath); err != nil {
				return nil, fmt.Errorf("output: file %q: %w", spec.name, err)
			}
		}
		if file.Name == "" {
			file.Name = path.Base(file.WorkspacePath)
		}
		file.ContentType = attachmentContentType(file.ContentType, file.Name, headBytes(file.Content))
		files = append(files, file)
	}
	return files, nil
}

func statWorkspaceFile(ctx context.Context, workspacePath string) error {
	root, name, err := service.OpenExecutionRoot(ctx, workspacePath, false)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Stat(name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", workspacePath)
	}
	return nil
}

func headBytes(b []byte) []byte {
	if len(b) > 512 {
		return b[:512]
	}
	return b
}
