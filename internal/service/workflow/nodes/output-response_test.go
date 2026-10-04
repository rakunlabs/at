package nodes_test

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// A child workflow writes a file and returns it next to text through named
// Output fields; the parent's Workflow Call exposes both on separate ports,
// and the parent's Output answers the HTTP caller with the file itself.
func TestWorkflowCallReturnsTextAndFile(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	root := t.TempDir()
	pdf := []byte("%PDF-1.4 fake report")
	if err := os.MkdirAll(filepath.Join(root, "runs", "test-run", "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "runs", "test-run", "out", "report.pdf"), pdf, 0o644); err != nil {
		t.Fatal(err)
	}

	// The child returns "text" and "file" as separate named Output fields.
	child := &service.Workflow{ID: "child", Graph: service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			{ID: "in", Type: "input"},
			{ID: "text", Type: "edit_fields", Data: map[string]any{"keep_input": false, "fields": []any{
				map[string]any{"name": "v", "source": "value", "value_type": "string", "value": "quarterly report"},
			}}},
			{ID: "file", Type: "edit_fields", Data: map[string]any{"keep_input": false, "fields": []any{
				map[string]any{"name": "v", "source": "value", "value_type": "json", "value": `{"path":"out/report.pdf","workspace_path":"runs/test-run/out/report.pdf","name":"report.pdf","content_type":"application/pdf","size_bytes":20}`},
			}}},
			{ID: "out", Type: "output", Data: map[string]any{
				"fields":         []any{"text", map[string]any{"name": "file"}},
				"input_mappings": map[string]any{"text": "/text/v", "file": "/file/v"},
			}},
		},
		Edges: []service.WorkflowEdge{
			{Source: "in", SourceHandle: "data", Target: "text", TargetHandle: "data"},
			{Source: "in", SourceHandle: "data", Target: "file", TargetHandle: "data"},
			{Source: "text", SourceHandle: "data", Target: "out", TargetHandle: "text"},
			{Source: "file", SourceHandle: "data", Target: "out", TargetHandle: "file"},
		},
	}}

	deps := workflow.Dependencies{WorkflowLookup: func(_ context.Context, id string) (*service.Workflow, error) {
		if id == child.ID {
			return child, nil
		}
		return nil, nil
	}}
	graph := service.WorkflowGraph{
		Nodes: []service.WorkflowNode{
			{ID: "in", Type: "input"},
			{ID: "call", Type: "workflow_call", Data: map[string]any{"workflow_id": "child", "output_fields": []any{"text", "file"}}},
			{ID: "out", Type: "output", Data: map[string]any{"fields": []any{"text", "file"}, "response_mode": "file", "file_path": "/file"}},
		},
		Edges: []service.WorkflowEdge{
			{Source: "in", SourceHandle: "data", Target: "call", TargetHandle: "inputs"},
			{Source: "call", SourceHandle: "text", Target: "out", TargetHandle: "text"},
			{Source: "call", SourceHandle: "file", Target: "out", TargetHandle: "file"},
		},
	}

	outputCh := make(chan workflow.EarlyOutput, 1)
	result, err := workflow.NewEngineWithDependencies(deps).Run(executiontest.WithRoot(t, root), graph, map[string]any{}, []string{"in"}, outputCh)
	if err != nil {
		t.Fatal(err)
	}
	if result.Outputs["text"] != "quarterly report" {
		t.Fatalf("text output = %#v", result.Outputs)
	}
	if ref, ok := result.Outputs["file"].(map[string]any); !ok || ref["name"] != "report.pdf" {
		t.Fatalf("file output = %#v", result.Outputs["file"])
	}

	early := <-outputCh
	if early.Err != nil || early.Response == nil || early.Response.Mode != workflow.OutputResponseFile {
		t.Fatalf("early response = %#v", early)
	}
	files, err := early.Response.OpenFiles()
	if err != nil {
		t.Fatal(err)
	}
	defer files[0].Close()
	got, _ := io.ReadAll(files[0].Reader)
	if string(got) != string(pdf) || files[0].ContentType != "application/pdf" || files[0].Name != "report.pdf" {
		t.Fatalf("response file = %q %q %q", got, files[0].ContentType, files[0].Name)
	}
}

func TestOutputResponseModes(t *testing.T) {
	executiontest.RequireExecutionFileAccess(t)
	inline := map[string]any{"name": "note.txt", "content_base64": base64.StdEncoding.EncodeToString([]byte("hello"))}

	tests := []struct {
		name    string
		data    map[string]any
		input   map[string]any
		wantErr bool
		files   int
	}{
		{name: "multipart collects every file", data: map[string]any{"response_mode": "multipart"}, input: map[string]any{"a": inline, "b": []any{inline}}, files: 2},
		{name: "multipart without files", data: map[string]any{"response_mode": "multipart"}, input: map[string]any{"text": "x"}, files: 0},
		{name: "file mode needs a file", data: map[string]any{"response_mode": "file"}, input: map[string]any{"text": "x"}, wantErr: true},
		{name: "missing stored file fails the step", data: map[string]any{"response_mode": "file"}, input: map[string]any{"f": map[string]any{"path": "nope.bin", "name": "nope.bin", "content_type": "application/octet-stream"}}, wantErr: true},
		{name: "unknown mode", data: map[string]any{"response_mode": "zip"}, input: map[string]any{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
				{ID: "in", Type: "input"},
				{ID: "out", Type: "output", Data: tt.data},
			}, Edges: []service.WorkflowEdge{{Source: "in", SourceHandle: "data", Target: "out", TargetHandle: "input"}}}
			result, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(executiontest.WithRoot(t, t.TempDir()), graph, tt.input, []string{"in"}, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Response == nil || len(result.Response.Files) != tt.files {
				t.Fatalf("response = %#v", result.Response)
			}
		})
	}
}

// Old graphs stored free-form display tags in Output fields; they must keep
// validating and simply add no port.
func TestOutputLegacyFieldTags(t *testing.T) {
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "in", Type: "input"},
		{ID: "out", Type: "output", Data: map[string]any{"fields": []any{"Final answer", "", "input"}}},
	}, Edges: []service.WorkflowEdge{{Source: "in", SourceHandle: "data", Target: "out", TargetHandle: "input"}}}
	if _, err := workflow.NewEngineWithDependencies(workflow.Dependencies{}).Run(executiontest.Context(t), graph, map[string]any{"x": 1}, []string{"in"}, nil); err != nil {
		t.Fatal(err)
	}
}
