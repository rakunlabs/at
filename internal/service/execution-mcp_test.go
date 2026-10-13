package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func allowAllExecution(t *testing.T) context.Context {
	t.Helper()
	ctx, err := BindExecution(t.Context(), ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "test"}, t.TempDir(),
		func(context.Context, ExecutionProvenance, ExecutionAction) (ExecutionValidation, error) {
			return ExecutionValidation{Allowed: true, Policy: ExecutionPolicy{WorkspaceID: "w", Mode: ExecutionTrustedHost, GrantedBy: "admin", AllowAllTools: true, AllowAllNodes: true}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

// Loopback endpoints are not special: with host execution granted, the URL an
// MCP set names is the URL that is dialled.
func TestExecutionMCPLoopbackNotRefused(t *testing.T) {
	ctx := allowAllExecution(t)
	for _, endpoint := range []string{"http://127.0.0.1:1/mcp", "http://localhost:1/mcp"} {
		if _, err := NewExecutionHTTPMCPClient(ctx, endpoint); errors.Is(err, ErrExecutionDenied) {
			t.Fatalf("%s: loopback MCP refused by policy: %v", endpoint, err)
		}
	}
}

func TestExecutionMCPRestrictedExplainsHostPermission(t *testing.T) {
	ctx, err := BindExecution(t.Context(), ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "chat"}, t.TempDir(),
		func(context.Context, ExecutionProvenance, ExecutionAction) (ExecutionValidation, error) {
			return ExecutionValidation{Allowed: true, Policy: ExecutionPolicy{WorkspaceID: "w", Mode: ExecutionRestricted, AllowAllTools: true}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	// Resource admission (and even Allow all tools) cannot lift Restricted mode.
	// Refusal must happen before any network request or OAuth token read.
	_, err = NewExecutionHTTPMCPClient(ctx, "https://gitlab.example/api/v4/mcp")
	if !errors.Is(err, ErrExecutionDenied) || !strings.Contains(err.Error(), "Trusted host") || !strings.Contains(err.Error(), "Settings → Execution") || !strings.Contains(err.Error(), "OAuth account does not grant") {
		t.Fatalf("restricted refusal is not actionable: %v", err)
	}
}

func TestExecutionMCPResourceDenialDistinctFromHostPolicy(t *testing.T) {
	ctx, err := BindExecution(t.Context(), ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "chat"}, t.TempDir(),
		func(_ context.Context, _ ExecutionProvenance, action ExecutionAction) (ExecutionValidation, error) {
			return ExecutionValidation{Allowed: action.Name != "mcp.use", Policy: ExecutionPolicy{WorkspaceID: "w", Mode: ExecutionTrustedHost, GrantedBy: "admin"}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewExecutionHTTPMCPClient(ctx, "https://gitlab.example/api/v4/mcp")
	if !errors.Is(err, ErrExecutionDenied) || !strings.Contains(err.Error(), "MCP set") || strings.Contains(err.Error(), "Trusted host") {
		t.Fatalf("resource refusal confused with host policy: %v", err)
	}
}
