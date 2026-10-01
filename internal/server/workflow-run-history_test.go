package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/ada"

	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/nativeauth/nativeauthtest"
	"github.com/rakunlabs/at/internal/service"
)

// Background and API runs leave a history row with their outcome; the
// editor's live stream is not recorded (it is watched as it happens).
func TestWorkflowRunHistoryRecordsBackgroundRuns(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	policy, err := f.store.GetExecutionPolicy(f.ctx, f.workspace)
	if err != nil {
		t.Fatal(err)
	}
	policyReq := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(`{"mode":"trusted_host","allow_all_nodes":true,"version":`+jsonNumber(policy.Version)+`}`)).WithContext(f.ctx)
	policyReq.SetPathValue("workspace", f.workspace)
	policyW := httptest.NewRecorder()
	f.s.RuntimeExecutionPolicyAPI(policyW, policyReq)
	if policyW.Code != 200 {
		t.Fatalf("configure policy: %d %s", policyW.Code, policyW.Body.String())
	}
	if _, err := f.store.SetAuthUserPassword(t.Context(), actor.UserID, nativeauthtest.PasswordHash, nil); err != nil {
		t.Fatal(err)
	}
	a, err := nativeauth.New(nativeauthtest.Config(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	f.s.nativeAuth = a
	f.s.config.BasePath = "/at"
	f.s.workflowStore, f.s.workflowVersionStore = f.store, f.store
	mux := ada.New()
	a.Register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(f.s.workspaceBusinessAuthentication())
	api.POST("/v1/workflows", f.s.CreateWorkflowAPI)
	api.POST("/v1/workflows/run/{id}", f.s.RunWorkflowAPI)
	api.POST("/v1/workflows/run-stream/{id}", f.s.RunWorkflowStreamAPI)
	api.GET("/v1/workflows/{id}/runs", f.s.ListWorkflowRunsAPI)
	cookie := nativeauthtest.LoginCookie(t, mux, "machine-admin")
	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/at/api/v1"+path, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Origin", a.Origin())
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-AT-Workspace-ID", f.workspace)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	create := func(graph string) string {
		t.Helper()
		w := call(http.MethodPost, "/workflows", `{"name":"history-`+time.Now().Format("150405.000000")+`","graph":`+graph+`}`)
		var created service.Workflow
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil {
			t.Fatalf("create workflow: %d %s", w.Code, w.Body.String())
		}
		return created.ID
	}
	listRuns := func(id string) []service.WorkflowRun {
		t.Helper()
		w := call(http.MethodGet, "/workflows/"+id+"/runs", "")
		var body struct {
			Data []service.WorkflowRun `json:"data"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatalf("list runs: %d %s", w.Code, w.Body.String())
		}
		return body.Data
	}
	// waitTerminal polls because async runs finish on their own goroutine.
	waitTerminal := func(id string) service.WorkflowRun {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if runs := listRuns(id); len(runs) == 1 && runs[0].Status != service.WorkflowRunRunning {
				return runs[0]
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("run never finished: %+v", listRuns(id))
		return service.WorkflowRun{}
	}

	okGraph := `{"nodes":[{"id":"in","type":"input"},{"id":"out","type":"output"}],"edges":[{"source":"in","source_handle":"data","target":"out","target_handle":"input"}]}`
	failGraph := `{"nodes":[{"id":"in","type":"input"},{"id":"boom","type":"script","data":{"code":"return 1"}},{"id":"cond","type":"conditional","data":{"expression":"undefinedFunction()"}}],"edges":[{"source":"in","source_handle":"data","target":"boom","target_handle":"data"},{"source":"boom","source_handle":"always","target":"cond","target_handle":"data"}]}`

	t.Run("async success", func(t *testing.T) {
		id := create(okGraph)
		if w := call(http.MethodPost, "/workflows/run/"+id, `{"inputs":{"x":1}}`); w.Code != http.StatusAccepted {
			t.Fatalf("run: %d %s", w.Code, w.Body.String())
		}
		run := waitTerminal(id)
		if run.Status != service.WorkflowRunCompleted || run.Source != "api" || run.OwnerUserID != actor.UserID || run.FinishedAt == nil || run.Error != "" {
			t.Fatalf("run = %+v", run)
		}
	})

	t.Run("async failure names the node", func(t *testing.T) {
		id := create(failGraph)
		if w := call(http.MethodPost, "/workflows/run/"+id, `{"inputs":{}}`); w.Code != http.StatusAccepted {
			t.Fatalf("run: %d %s", w.Code, w.Body.String())
		}
		run := waitTerminal(id)
		if run.Status != service.WorkflowRunFailed || run.FailedNodeID != "cond" || run.FailedNodeType != "conditional" || !strings.Contains(run.Error, "undefinedFunction") {
			t.Fatalf("run = %+v", run)
		}
	})

	t.Run("editor stream is not recorded", func(t *testing.T) {
		id := create(okGraph)
		w := call(http.MethodPost, "/workflows/run-stream/"+id, `{"inputs":{}}`)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"done"`) {
			t.Fatalf("stream: %d %s", w.Code, w.Body.String())
		}
		if runs := listRuns(id); len(runs) != 0 {
			t.Fatalf("stream run recorded: %+v", runs)
		}
	})

	t.Run("status filter is validated", func(t *testing.T) {
		id := create(okGraph)
		if w := call(http.MethodGet, "/workflows/"+id+"/runs?status=nope", ""); w.Code != http.StatusBadRequest {
			t.Fatalf("bad status: %d %s", w.Code, w.Body.String())
		}
		if w := call(http.MethodGet, "/workflows/"+id+"/runs?limit=0", ""); w.Code != http.StatusBadRequest {
			t.Fatalf("bad limit: %d %s", w.Code, w.Body.String())
		}
	})
}
