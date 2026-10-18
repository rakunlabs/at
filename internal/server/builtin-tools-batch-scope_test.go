package server

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

// batch_execute must not reach built-ins the calling surface does not expose
// (an MCP server's enabled tools, an agent's tool list), even when the
// workspace execution policy would allow them.
func TestBatchExecuteStaysWithinToolScope(t *testing.T) {
	var revoked atomic.Bool
	ctx := runtimeTestContext(t, t.TempDir(), &revoked)
	s := &Server{}
	call := map[string]any{"tool_calls": []any{map[string]any{"name": "file_list", "arguments": map[string]any{}}}}

	tests := []struct {
		name    string
		allowed func(string) bool
		scoped  bool
		refused bool
	}{
		{"no scope fails closed", nil, false, true},
		{"only batch exposed", func(name string) bool { return name == "batch_execute" }, true, true},
		{"nested tool exposed", func(name string) bool { return name == "batch_execute" || name == "file_list" }, true, false},
		{"unrestricted surface", nil, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCtx := ctx
			if tt.scoped {
				callCtx = service.ContextWithBuiltinToolScope(ctx, tt.allowed)
			}
			result, err := s.dispatchBuiltinTool(callCtx, "batch_execute", call)
			if err != nil {
				t.Fatal(err)
			}
			refused := strings.Contains(result, "not enabled here")
			if refused != tt.refused {
				t.Fatalf("refused = %v, want %v: %s", refused, tt.refused, result)
			}
		})
	}
}
