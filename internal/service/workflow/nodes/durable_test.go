package nodes_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func durableGraph(url string) service.WorkflowGraph {
	return service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "i", Type: "input"},
		{ID: "before", Type: "http_request", Data: map[string]any{"url": url + "/before"}},
		{ID: "wait", Type: "wait", Data: map[string]any{"mode": "approval", "expires_seconds": 60}},
		{ID: "after", Type: "http_request", Data: map[string]any{"url": url + "/after"}},
		{ID: "out", Type: "output"},
	}, Edges: []service.WorkflowEdge{
		{Source: "i", SourceHandle: "data", Target: "before", TargetHandle: "data"},
		{Source: "before", SourceHandle: "always", Target: "wait", TargetHandle: "data"},
		{Source: "wait", SourceHandle: "data", Target: "after", TargetHandle: "data"},
		{Source: "after", SourceHandle: "always", Target: "out", TargetHandle: "input"},
	}}
}

func TestDurableWaitRestoresResultsWithoutRepeatingSideEffects(t *testing.T) {
	var before, after atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/before" {
			before.Add(1)
		} else {
			after.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	graph := durableGraph(upstream.URL)
	var checkpoint service.WorkflowCheckpoint
	status := ""
	save := func(value service.WorkflowCheckpoint, next string) error {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		checkpoint = service.WorkflowCheckpoint{}
		if err := json.Unmarshal(b, &checkpoint); err != nil {
			return err
		}
		status = next
		return nil
	}
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	if _, err := e.RunDurable(executiontest.Context(t), graph, map[string]any{"text": "original"}, []string{"i"}, checkpoint, false, save); err != nil {
		t.Fatal(err)
	}
	if status != "waiting" || checkpoint.Waiting == nil || before.Load() != 1 || after.Load() != 0 {
		t.Fatalf("not paused safely: %s %+v calls=%d/%d", status, checkpoint.Waiting, before.Load(), after.Load())
	}
	// A new engine has no in-memory state from the original worker.
	replica := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	if _, err := replica.RunDurable(executiontest.Context(t), graph, nil, []string{"i"}, checkpoint, false, save); err != nil {
		t.Fatal(err)
	}
	if after.Load() != 0 {
		t.Fatal("wait resumed without approval")
	}
	if _, err := replica.RunDurable(executiontest.Context(t), graph, nil, []string{"i"}, checkpoint, true, save); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || before.Load() != 1 || after.Load() != 1 {
		t.Fatalf("replayed side effects: %s %d/%d", status, before.Load(), after.Load())
	}
	if _, err := replica.RunDurable(executiontest.Context(t), graph, nil, []string{"i"}, checkpoint, true, save); err != nil {
		t.Fatal(err)
	}
	if before.Load() != 1 || after.Load() != 1 {
		t.Fatal("completed checkpoint replayed")
	}
}

func TestDurableCrashCheckpointBlocksUncertainStep(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = w.Write([]byte(`ok`)) }))
	defer upstream.Close()
	graph := durableGraph(upstream.URL)
	var persisted service.WorkflowCheckpoint
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	_, err := e.RunDurable(executiontest.Context(t), graph, nil, []string{"i"}, service.WorkflowCheckpoint{}, false, func(cp service.WorkflowCheckpoint, _ string) error {
		if _, done := cp.Nodes["before"]; done {
			return errors.New("simulated DB failure after external call")
		}
		b, _ := json.Marshal(cp)
		persisted = service.WorkflowCheckpoint{}
		return json.Unmarshal(b, &persisted)
	})
	if err == nil || persisted.InFlight != "before" || calls.Load() != 1 {
		t.Fatalf("missing intent checkpoint: %v %+v", err, persisted)
	}
	_, err = e.RunDurable(executiontest.Context(t), graph, nil, []string{"i"}, persisted, false, func(service.WorkflowCheckpoint, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "will not be replayed") || calls.Load() != 1 {
		t.Fatalf("uncertain step replayed: %v calls=%d", err, calls.Load())
	}
}

func TestOrdinaryRunsAndPartialTestsRefuseWaitBeforeEffects(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	graph := durableGraph(upstream.URL)
	e := workflow.NewEngineWithDependencies(workflow.Dependencies{})
	if _, err := e.Run(executiontest.Context(t), graph, nil, []string{"i"}, nil); err == nil {
		t.Fatal("ordinary engine silently lost a Wait")
	}
	if _, err := e.RunTest(executiontest.Context(t), graph, nil, []string{"i"}, workflow.TestRunOptions{TargetNodeID: "after"}); err == nil {
		t.Fatal("partial test crossed Wait")
	}
	if calls.Load() != 0 {
		t.Fatal("incompatible launch already performed side effects")
	}
	if _, err := e.RunTest(executiontest.Context(t), graph, nil, []string{"i"}, workflow.TestRunOptions{TargetNodeID: "before"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("partial test before Wait did not run")
	}
}

func TestDurableGraphRejectsAmbiguousCheckpointIdentities(t *testing.T) {
	for _, graph := range []service.WorkflowGraph{
		{Nodes: []service.WorkflowNode{{ID: "", Type: "input"}}},
		{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "i", Type: "wait"}}},
	} {
		if err := workflow.ValidateDurableGraph(graph, nil); err == nil {
			t.Fatal("ambiguous checkpoint node identity accepted")
		}
	}
}
