package server

import (
	"reflect"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestWorkflowToolDefDescribesEntryInputKeys(t *testing.T) {
	wf := &service.Workflow{
		ID:   "wf-1",
		Name: "Video Toolkit",
		Graph: service.WorkflowGraph{Nodes: []service.WorkflowNode{
			{
				ID:   "input_assemble",
				Type: "input",
				Data: map[string]any{
					"label": "assemble_video",
					"fields": []any{
						map[string]any{"name": "scenes"},
						map[string]any{"name": "countdown"},
						map[string]any{"name": "polish"},
						map[string]any{"name": "work_dir"},
					},
				},
			},
		}},
	}

	tool := workflowToolDef(wf)
	if !strings.Contains(tool.Description, "assemble_video(scenes, countdown, polish, work_dir)") {
		t.Fatalf("tool description does not contain entry contract: %q", tool.Description)
	}
	inputs := tool.InputSchema["properties"].(map[string]any)["inputs"].(map[string]any)
	if !strings.Contains(inputs["description"].(string), "not a JSON string") {
		t.Fatalf("inputs description does not explain object contract: %#v", inputs)
	}
}

func TestWorkflowToolInputs(t *testing.T) {
	want := map[string]any{"audio": "audio_0.mp3", "work_dir": "/work"}
	tests := []struct {
		name    string
		value   any
		wantErr bool
	}{
		{name: "object", value: want},
		{name: "json encoded object", value: `{"audio":"audio_0.mp3","work_dir":"/work"}`},
		{name: "invalid json", value: `{"audio":`, wantErr: true},
		{name: "wrong type", value: []any{"audio_0.mp3"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workflowToolInputs(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("workflowToolInputs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, want) {
				t.Fatalf("workflowToolInputs() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestWorkflowOutputFailure(t *testing.T) {
	tests := []struct {
		name    string
		outputs map[string]any
		want    string
	}{
		{
			name: "successful output",
			outputs: map[string]any{
				"assemble_result": map[string]any{"input": map[string]any{"exit_code": 0, "stdout": `{"file":"final.mp4"}`}},
			},
		},
		{
			name: "nested error field",
			outputs: map[string]any{
				"assemble_result": map[string]any{"input": map[string]any{"error": "script failed"}},
			},
			want: "script failed",
		},
		{
			name: "nonzero exit",
			outputs: map[string]any{
				"assemble_result": map[string]any{"input": map[string]any{"exit_code": float64(2)}},
			},
			want: "workflow command exited with code 2",
		},
		{
			name: "json error in successful stdout",
			outputs: map[string]any{
				"assemble_result": map[string]any{"input": map[string]any{"exit_code": 0, "stdout": `{"error":"scenes array is required"}`}},
			},
			want: "scenes array is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflowOutputFailure(tt.outputs); got != tt.want {
				t.Fatalf("workflowOutputFailure() = %q, want %q", got, tt.want)
			}
		})
	}
}
