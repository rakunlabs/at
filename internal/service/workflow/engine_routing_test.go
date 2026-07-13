package workflow

import "testing"

func TestGatherInputsSelectionRoutesWholePayload(t *testing.T) {
	e := &Engine{}
	states := map[string]*nodeState{
		"exec": {},
		"out": {
			inputs: map[string][]connection{
				"input": {{nodeID: "exec", port: "always"}},
			},
		},
	}
	payload := map[string]any{"stdout": "ok", "exit_code": 0}
	outputs := map[string]NodeResult{
		"exec": NewSelectionResult(payload, []string{"always", "true"}),
	}

	got := e.gatherInputs("out", states, outputs)
	input, ok := got["input"].(map[string]any)
	if !ok {
		t.Fatalf("input = %#v, want full result payload", got["input"])
	}
	if input["stdout"] != "ok" || input["exit_code"] != 0 {
		t.Fatalf("input = %#v, want stdout and exit_code", input)
	}
}

func TestGatherInputsLegacyOutputHandleUsesData(t *testing.T) {
	e := &Engine{}
	states := map[string]*nodeState{
		"input": {},
		"exec": {
			inputs: map[string][]connection{
				"data": {{nodeID: "input", port: "output"}},
			},
		},
	}
	payload := map[string]any{"scene": 1}
	outputs := map[string]NodeResult{
		"input": NewResult(map[string]any{"data": payload}),
	}

	got := e.gatherInputs("exec", states, outputs)
	data, ok := got["data"].(map[string]any)
	if !ok || data["scene"] != 1 {
		t.Fatalf("data = %#v, want legacy output handle to route data payload", got["data"])
	}
}
