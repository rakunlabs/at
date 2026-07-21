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

func TestWorkflowUpdateToolRequiresActivation(t *testing.T) {
	var updateDescription string
	activateDefined := false
	for _, tool := range builtinTools {
		switch tool.Name {
		case "workflow_update":
			updateDescription = tool.Description
		case "workflow_activate":
			activateDefined = true
		}
	}

	if !activateDefined {
		t.Fatal("workflow_activate tool is not defined")
	}
	if !strings.Contains(updateDescription, "activated by default") || !strings.Contains(updateDescription, "MUST call workflow_activate") {
		t.Fatalf("workflow_update does not require activation: %q", updateDescription)
	}
}

func TestWorkflowVersionArg(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    int
		wantErr bool
	}{
		{name: "JSON integer", value: float64(3), want: 3},
		{name: "Go integer", value: 2, want: 2},
		{name: "fraction", value: 1.5, wantErr: true},
		{name: "zero", value: float64(0), wantErr: true},
		{name: "string", value: "3", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := workflowVersionArg(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("workflowVersionArg() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("workflowVersionArg() = %d, want %d", got, tt.want)
			}
		})
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

func TestStripWorkflowInputEcho(t *testing.T) {
	inputs := map[string]any{
		"scenes":    []any{map[string]any{"image": "/w/scene_0.png", "audio": "/w/tts_0.mp3"}},
		"countdown": map[string]any{"enabled": true, "start_number": float64(5)},
		"work_dir":  "/w",
	}

	tests := []struct {
		name    string
		outputs map[string]any
		want    map[string]any
	}{
		{
			name: "exec node echo under handle.data is stripped, results kept",
			outputs: map[string]any{
				"input": map[string]any{
					"data":      copyMapForEchoTest(inputs),
					"exit_code": 0,
					"stdout":    `{"file":"final.mp4"}`,
				},
			},
			want: map[string]any{
				"input": map[string]any{
					"exit_code": 0,
					"stdout":    `{"file":"final.mp4"}`,
				},
			},
		},
		{
			name: "label-nested echo is stripped",
			outputs: map[string]any{
				"assemble_result": map[string]any{
					"input": map[string]any{
						"data":   copyMapForEchoTest(inputs),
						"stdout": "ok",
					},
				},
			},
			want: map[string]any{
				"assemble_result": map[string]any{
					"input": map[string]any{
						"stdout": "ok",
					},
				},
			},
		},
		{
			name: "derived or partial data is kept",
			outputs: map[string]any{
				"input": map[string]any{
					"data":   map[string]any{"work_dir": "/w"},
					"stdout": "ok",
				},
			},
			want: map[string]any{
				"input": map[string]any{
					"data":   map[string]any{"work_dir": "/w"},
					"stdout": "ok",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripWorkflowInputEcho(tt.outputs, inputs)
			if !reflect.DeepEqual(tt.outputs, tt.want) {
				t.Fatalf("stripWorkflowInputEcho() = %#v, want %#v", tt.outputs, tt.want)
			}
		})
	}
}

// copyMapForEchoTest deep-copies via JSON semantics-preserving shallow copy —
// the echo check uses reflect.DeepEqual, so an equal (not identical) map must
// also be stripped.
func copyMapForEchoTest(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
