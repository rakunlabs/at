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

func TestExecutionMCPTrustedLocalEndpoints(t *testing.T) {
	ctx := allowAllExecution(t)
	var trusted []string
	SetTrustedLocalMCPLoader(func(context.Context) ([]string, error) { return trusted, nil })
	t.Cleanup(func() { SetTrustedLocalMCPLoader(nil) })
	if _, err := NewExecutionHTTPMCPClient(ctx, "http://127.0.0.1:8890/mcp"); !errors.Is(err, ErrExecutionDenied) || !strings.Contains(err.Error(), "Trusted local MCP") {
		t.Fatalf("loopback MCP admitted without operator allowlist: %v", err)
	}

	trusted = []string{" http://127.0.0.1:8890/mcp/ "}
	InvalidateTrustedLocalMCP()
	for _, refused := range []string{"http://127.0.0.1:8891/mcp", "http://localhost:8890/mcp", "http://127.0.0.1:8890/other"} {
		if _, err := NewExecutionHTTPMCPClient(ctx, refused); !errors.Is(err, ErrExecutionDenied) {
			t.Fatalf("%s: only the exact listed endpoint may be dialled: %v", refused, err)
		}
	}
	// The listed endpoint passes the loopback guard; the dial itself fails
	// here because nothing listens, which is a connection error, not a denial.
	if _, err := NewExecutionHTTPMCPClient(ctx, "http://127.0.0.1:8890/mcp"); errors.Is(err, ErrExecutionDenied) {
		t.Fatalf("listed local MCP still refused: %v", err)
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
