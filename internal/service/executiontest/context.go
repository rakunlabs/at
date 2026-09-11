// Package executiontest provides explicit authority fixtures for runtime tests.
// Production admission must use live identity/policy resolvers instead.
package executiontest

import (
	"context"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func Context(t testing.TB) context.Context { t.Helper(); return WithRoot(t, t.TempDir()) }

func WithRoot(t testing.TB, root string) context.Context {
	t.Helper()
	p := service.AccessPrincipal{UserID: "test-user", WorkspaceID: "legacy-default", PlatformAdmin: true}
	ctx := service.WithAccessPrincipal(t.Context(), p)
	ctx, err := service.BindPlatformExecution(ctx, service.ExecutionProvenance{RunID: "test-run", UserID: p.UserID, WorkspaceID: p.WorkspaceID, Source: "explicit-test-fixture"}, root, func(context.Context, service.ExecutionProvenance, service.ExecutionAction) (service.ExecutionValidation, error) {
		return service.ExecutionValidation{Allowed: true, PlatformAdmin: true, Policy: service.ExecutionPolicy{WorkspaceID: p.WorkspaceID, Mode: service.ExecutionTrustedHost, GrantedBy: "test-platform-admin"}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
