package server

import (
	"context"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// dispatchBuiltinTool preserves admission at one boundary for every executor.
func (s *Server) dispatchBuiltinTool(ctx context.Context, name string, args map[string]any) (string, error) {
	if builtinToolRegistryErr != nil {
		return "", fmt.Errorf("built-in tool registry unavailable: %w: %w", service.ErrExecutionDenied, builtinToolRegistryErr)
	}
	tool, ok := builtinToolRegistry.byName[name]
	if !ok {
		return "", fmt.Errorf("unknown built-in tool %q: %w", name, service.ErrExecutionDenied)
	}
	var bindErr error
	ctx, bindErr = s.bindRuntimePrincipal(ctx, "tool")
	if bindErr != nil {
		return "", bindErr
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name}); err != nil {
		return "", err
	}
	if tool.Class == builtinHostFiles {
		// Legacy file helpers still use host paths, not rooted workspace access.
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "file", Name: "files.host"}); err != nil {
			return "", err
		}
	}
	if err := s.checkBuiltinToolFeatures(ctx, name); err != nil {
		return "", err
	}
	return tool.Execute(s, ctx, args)
}
