package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/loopgov"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func TestRuntimeLegacyRequiresExplicitInstallationAdmission(t *testing.T) {
	store := postgrestest.New(t, nil)
	s := &Server{store: store, loopGov: loopgov.New(loopgov.Config{WorkspaceRoot: t.TempDir()}, nil)}
	args := map[string]any{"command": "printf installation"}
	if _, err := s.dispatchBuiltinTool(t.Context(), "bash_execute", args); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("blank context inherited installation authority")
	}
	ctx := service.WithLegacyWorkspaceAccess(t.Context())
	result, err := s.dispatchBuiltinTool(ctx, "bash_execute", args)
	if err != nil || result != "installation" {
		t.Fatalf("explicit installation admission: %q %v", result, err)
	}
	policy, err := store.GetExecutionPolicy(t.Context(), "legacy-default")
	if err != nil || policy.Mode != service.ExecutionRestricted || policy.Version != 0 {
		t.Fatalf("legacy admission mutated workspace policy: %+v %v", policy, err)
	}
	ordinary := service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: "untrusted", WorkspaceID: "legacy-default", Role: "admin"})
	if _, err := s.bindLegacyRuntime(ordinary, "test"); !errors.Is(err, service.ErrExecutionDenied) {
		t.Fatal("workspace principal acquired installation fallback")
	}
	handler := s.runtimeRouteHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := service.ExecutionFromContext(r.Context()); !ok {
			t.Error("route lost explicit authority")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	unbound := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/files", nil)
	req.Header.Set("X-AT-Workspace-ID", "legacy-default")
	handler.ServeHTTP(unbound, req)
	if unbound.Code == http.StatusNoContent {
		t.Fatal("header forged installation admission")
	}
	admitted := httptest.NewRecorder()
	handler.ServeHTTP(admitted, req.WithContext(ctx))
	if admitted.Code != http.StatusNoContent {
		t.Fatalf("explicit parent admission failed: %d %s", admitted.Code, admitted.Body.String())
	}
}
