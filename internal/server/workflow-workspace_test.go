package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/ada"
	"github.com/rakunlabs/ada/middleware/auth/password"

	"github.com/rakunlabs/at/internal/service"
)

func TestWorkflowHTTPWorkspaceIsolation(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	if _, err := f.store.SetAuthUserPassword(t.Context(), actor.UserID, password.Dummy, nil); err != nil {
		t.Fatal(err)
	}
	a, err := newNativeAuth(nativeTestConfig(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	f.s.nativeAuth = a
	f.s.config.BasePath = "/at"
	f.s.workflowStore, f.s.workflowVersionStore = f.store, f.store
	mux := ada.New()
	a.register(mux, "/at")
	api := mux.Group("/at/api")
	api.Use(f.s.workspaceBusinessAuthentication())
	api.GET("/v1/workflows", f.s.ListWorkflowsAPI)
	api.POST("/v1/workflows", f.s.CreateWorkflowAPI)
	api.GET("/v1/workflows/{id}", f.s.GetWorkflowAPI)
	api.PUT("/v1/workflows/{id}", f.s.UpdateWorkflowAPI)
	api.DELETE("/v1/workflows/{id}", f.s.DeleteWorkflowAPI)
	api.GET("/v1/workflows/{id}/versions", f.s.ListWorkflowVersionsAPI)
	api.POST("/v1/workflows/run/{id}", f.s.RunWorkflowAPI)
	api.POST("/v1/workflows/run-stream/{id}", f.s.RunWorkflowStreamAPI)
	api.GET("/v1/runs", f.s.ListActiveRunsAPI)
	api.POST("/v1/runs/{id}/cancel", f.s.CancelRunAPI)
	cookie := nativeLoginCookie(t, mux, "machine-admin")
	other, err := f.store.CreateWorkspace(service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{UserID: actor.UserID}), "other", actor.UserID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, workspace, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/at/api/v1"+path, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Origin", a.cfg.Origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-AT-Workspace-ID", workspace)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	ids := map[string]string{}
	for _, workspace := range []string{f.workspace, other.ID} {
		w := call(http.MethodPost, "/workflows", workspace, `{"name":"same-name","graph":{"nodes":[],"edges":[]}}`)
		var created service.Workflow
		if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.WorkspaceID != workspace {
			t.Fatalf("create workflow: %d %s", w.Code, w.Body.String())
		}
		ids[workspace] = created.ID
		w = call(http.MethodGet, "/workflows", workspace, "")
		var listed service.ListResult[service.Workflow]
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed.Data) != 1 || listed.Data[0].ID != created.ID {
			t.Fatalf("list workflow: %d %s", w.Code, w.Body.String())
		}
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/workflows/" + ids[other.ID]},
		{http.MethodPut, "/workflows/" + ids[other.ID]},
		{http.MethodDelete, "/workflows/" + ids[other.ID]},
		{http.MethodGet, "/workflows/" + ids[other.ID] + "/versions"},
		{http.MethodPost, "/workflows/run/" + ids[other.ID]},
		{http.MethodPost, "/workflows/run-stream/" + ids[other.ID]},
	} {
		w := call(route.method, route.path, f.workspace, `{}`)
		if w.Code != 404 && w.Code != 403 {
			t.Fatalf("cross-workspace %s %s: %d %s", route.method, route.path, w.Code, w.Body.String())
		}
	}
	for _, workspace := range []string{f.workspace, other.ID} {
		principal, _, err := f.store.ResolveWorkspaceAccess(t.Context(), workspace, actor.UserID, "")
		if err != nil {
			t.Fatal(err)
		}
		id, ctx, cleanup := f.s.registerRun(service.WithAccessPrincipal(t.Context(), principal), ids[workspace], "test")
		t.Cleanup(cleanup)
		if workspace == other.ID {
			w := call(http.MethodPost, fmt.Sprintf("/runs/%s/cancel", id), f.workspace, "")
			if w.Code != 404 || ctx.Err() != nil {
				t.Fatalf("foreign run cancelled: %d %s", w.Code, w.Body.String())
			}
		}
	}
	w := call(http.MethodGet, "/runs", f.workspace, "")
	var runs activeRunsResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &runs) != nil || len(runs.Runs) != 1 || runs.Runs[0].WorkflowID != ids[f.workspace] {
		t.Fatalf("run list leaked workspace: %d %s", w.Code, w.Body.String())
	}
}
