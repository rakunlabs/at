package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestDurableWorkflowPostgresRestartFrozenGraphAndRevocation(t *testing.T) {
	f := newMachineFixture(t)
	f.s.workflowStore, f.s.workflowVersionStore = f.store, f.store
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	policy, err := f.store.GetExecutionPolicy(f.ctx, f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(`{"mode":"trusted_host","allow_all_nodes":true,"version":`+jsonNumber(policy.Version)+`}`)).WithContext(f.ctx)
	req.SetPathValue("workspace", f.workspace)
	w := httptest.NewRecorder()
	f.s.RuntimeExecutionPolicyAPI(w, req)
	if w.Code != 200 {
		t.Fatalf("configure policy: %d %s", w.Code, w.Body.String())
	}
	base := service.WithAccessPrincipal(t.Context(), actor)
	bound, err := f.s.bindRuntimePrincipal(base, "api")
	if err != nil {
		t.Fatal(err)
	}
	var before, after, unexpected atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/before":
			before.Add(1)
		case "/after":
			after.Add(1)
		default:
			unexpected.Add(1)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{
		{ID: "i", Type: "input"},
		{ID: "before", Type: "http_request", Data: map[string]any{"url": upstream.URL + "/before"}},
		{ID: "wait", Type: "wait", Data: map[string]any{"mode": "approval", "expires_seconds": 60, "prompt": "Review before publishing"}},
		{ID: "after", Type: "http_request", Data: map[string]any{"url": upstream.URL + "/after"}},
		{ID: "out", Type: "output"},
	}, Edges: []service.WorkflowEdge{
		{Source: "i", SourceHandle: "data", Target: "before", TargetHandle: "data"},
		{Source: "before", SourceHandle: "always", Target: "wait", TargetHandle: "data"},
		{Source: "wait", SourceHandle: "data", Target: "after", TargetHandle: "data"},
		{Source: "after", SourceHandle: "always", Target: "out", TargetHandle: "input"},
	}}
	wf, err := f.store.CreateWorkflow(base, service.Workflow{Name: "durable approval", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	launch := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/run-stream/"+wf.ID, strings.NewReader(`{"inputs":{"value":"snapshot"}}`)).WithContext(base)
	launch.SetPathValue("id", wf.ID)
	accepted := httptest.NewRecorder()
	f.s.RunWorkflowStreamAPI(accepted, launch)
	var event struct {
		EventType string `json:"event_type"`
		RunID     string `json:"run_id"`
	}
	if accepted.Code != 200 || json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(accepted.Body.String(), "data: "))), &event) != nil || event.EventType != "durable_started" {
		t.Fatalf("stream did not launch durably: %d %s", accepted.Code, accepted.Body.String())
	}
	job, err := f.store.GetWorkflowExecution(base, wf.ID, event.RunID)
	if err != nil || job == nil {
		t.Fatalf("queued record missing: %+v %v", job, err)
	}
	maintenance := service.WithExecutionMaintenance(t.Context())
	claimed, err := f.store.ClaimWorkflowExecution(maintenance, "first-process")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	f.s.runDurableWorkflow(maintenance, f.store, claimed, "first-process")
	waiting, err := f.store.GetWorkflowExecution(base, wf.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting" || before.Load() != 1 || after.Load() != 0 {
		t.Fatalf("first segment: %s %s calls=%d/%d", waiting.Status, waiting.Error, before.Load(), after.Load())
	}
	// Workflow edits must not replace the snapshot held by a waiting run.
	graph.Nodes[3].Data["url"] = upstream.URL + "/unexpected"
	if _, err := f.store.UpdateWorkflow(base, wf.ID, service.Workflow{Name: wf.Name, Graph: graph}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DecideWorkflowExecution(base, wf.ID, job.ID, waiting.Revision, "approve"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DecideWorkflowExecution(base, wf.ID, job.ID, waiting.Revision, "approve"); !errors.Is(err, service.ErrWorkflowExecutionConflict) {
		t.Fatal("approval executed twice")
	}
	// New server, no in-memory graph or run registry from the original worker.
	replica := &Server{ctx: t.Context(), store: f.store, workflowStore: f.store, workflowVersionStore: f.store, loopGov: f.s.loopGov}
	resumed, err := f.store.ClaimWorkflowExecution(maintenance, "replacement-process")
	if err != nil || resumed == nil {
		t.Fatalf("resume claim: %+v %v", resumed, err)
	}
	replica.runDurableWorkflow(maintenance, f.store, resumed, "replacement-process")
	finished, err := f.store.GetWorkflowExecution(base, wf.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "completed" || before.Load() != 1 || after.Load() != 1 || unexpected.Load() != 0 {
		t.Fatalf("restart result: %s %s calls=%d/%d/%d", finished.Status, finished.Error, before.Load(), after.Load(), unexpected.Load())
	}
	// Stored source identity must remain live; an approver cannot lend theirs.
	graph.Nodes[3].Data["url"] = upstream.URL + "/after"
	job, err = f.s.enqueueDurableWorkflow(bound, wf.ID, graph, nil, []string{"i"}, "api")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err = f.store.ClaimWorkflowExecution(maintenance, "before-revoke")
	if err != nil {
		t.Fatal(err)
	}
	f.s.runDurableWorkflow(maintenance, f.store, claimed, "before-revoke")
	waiting, err = f.store.GetWorkflowExecution(base, wf.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Status != "waiting" {
		t.Fatal(waiting.Error)
	}
	if err := f.store.DecideWorkflowExecution(base, wf.ID, job.ID, waiting.Revision, "approve"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.DeleteAuthSession(t.Context(), actor.SessionID); err != nil {
		t.Fatal(err)
	}
	resumed, err = f.store.ClaimWorkflowExecution(maintenance, "after-revoke")
	if err != nil {
		t.Fatal(err)
	}
	replica.runDurableWorkflow(maintenance, f.store, resumed, "after-revoke")
	user, err := f.store.GetAuthUserByID(t.Context(), actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.CreateAuthSession(t.Context(), service.AuthSession{Hash: "durable-new-session", UserID: actor.UserID, Version: user.SessionVersion, Transport: "web", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	reader, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, actor.UserID, "durable-new-session")
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := f.store.GetWorkflowExecution(service.WithAccessPrincipal(context.Background(), reader), wf.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != "blocked" || after.Load() != 1 {
		t.Fatalf("revoked identity resumed: %s %s calls=%d", blocked.Status, blocked.Error, after.Load())
	}
}

func jsonNumber(value int64) string { data, _ := json.Marshal(value); return string(data) }

func TestDurableWorkflowMachineTimerSurvivesBrowserLogout(t *testing.T) {
	f := newMachineFixture(t)
	f.s.workflowStore, f.s.workflowVersionStore = f.store, f.store
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	policy, err := f.store.GetExecutionPolicy(f.ctx, f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(`{"mode":"trusted_host","allow_all_nodes":true,"version":`+jsonNumber(policy.Version)+`}`)).WithContext(f.ctx)
	req.SetPathValue("workspace", f.workspace)
	w := httptest.NewRecorder()
	f.s.RuntimeExecutionPolicyAPI(w, req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	base := service.WithAccessPrincipal(t.Context(), actor)
	bound, err := f.s.bindRuntimePrincipal(base, "api")
	if err != nil {
		t.Fatal(err)
	}
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "i", Type: "input"}, {ID: "w", Type: "wait", Data: map[string]any{"seconds": 1}}, {ID: "o", Type: "output"}}, Edges: []service.WorkflowEdge{{Source: "i", SourceHandle: "data", Target: "w", TargetHandle: "data"}, {Source: "w", SourceHandle: "data", Target: "o", TargetHandle: "input"}}}
	wf, err := f.store.CreateWorkflow(base, service.Workflow{Name: "machine timer", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := f.store.CreateTrigger(base, service.Trigger{WorkflowID: wf.ID, TargetID: wf.ID, TargetType: "workflow", Type: "cron", Enabled: true, Config: map[string]any{"schedule": "* * * * *"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.saveRuntimeBinding(bound, "trigger", trigger.ID, false, f.member); err != nil {
		t.Fatal(err)
	}
	machine, err := f.s.ResumeRuntimeSubject(t.Context(), "trigger", trigger.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.s.enqueueDurableWorkflow(machine, wf.ID, graph, map[string]any{"message": "from trigger"}, []string{"i"}, "cron")
	if err != nil {
		t.Fatal(err)
	}
	if job.OwnerUserID != f.member {
		t.Fatal("machine run adopted creator instead of Run-as identity")
	}
	maintenance := service.WithExecutionMaintenance(t.Context())
	claimed, err := f.store.ClaimWorkflowExecution(maintenance, "timer-first", "cron")
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	f.s.runDurableWorkflow(maintenance, f.store, claimed, "timer-first")
	if err := f.store.DeleteAuthSession(t.Context(), actor.SessionID); err != nil {
		t.Fatal(err)
	}
	var resumed *service.WorkflowExecution
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		resumed, err = f.store.ClaimWorkflowExecution(maintenance, "timer-restarted", "cron")
		if err != nil {
			t.Fatal(err)
		}
		if resumed != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if resumed == nil {
		t.Fatal("timer never became ready")
	}
	replica := &Server{ctx: t.Context(), store: f.store, workflowStore: f.store, loopGov: f.s.loopGov}
	replica.runDurableWorkflow(maintenance, f.store, resumed, "timer-restarted")
	member, _, err := f.store.ResolveWorkspaceAccess(t.Context(), f.workspace, f.member, "")
	if err != nil {
		t.Fatal(err)
	}
	finished, err := f.store.GetWorkflowExecution(service.WithAccessPrincipal(t.Context(), member), wf.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "completed" {
		t.Fatalf("machine resumed with browser identity: %s %s", finished.Status, finished.Error)
	}
}
