package workflow

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
)

// AuthorizeToolHandler classifies the resolved implementation, never a class
// supplied by the caller. A workflow/delegate indirection cannot bless bash.
func AuthorizeToolHandler(ctx context.Context, name, handlerType, skillID, target string) error {
	switch handlerType {
	case "builtin":
		return service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name})
	case "delegate", "agent":
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: target}); err != nil {
			return err
		}
		return service.CheckExecution(ctx, service.ExecutionAction{Kind: "delegate", Name: name, ResourceID: target})
	case "workflow":
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: target}); err != nil {
			return err
		}
		return service.CheckExecution(ctx, service.ExecutionAction{Kind: "delegate", Name: name, ResourceKind: "workflows", ResourceID: target})
	case "bash", "", "javascript", "js":
		// Inline code has no independently owned skill resource to authorize.
		// Until it has one, it is not an executable runtime capability.
		if skillID == "" {
			return service.CheckExecution(ctx, service.ExecutionAction{Kind: "inline_tool", Name: name})
		}
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: skillID}); err != nil {
			return err
		}
		return service.CheckExecution(ctx, service.ExecutionAction{Kind: "skill_tool", Name: name, ResourceID: skillID})
	default:
		return service.ErrExecutionDenied
	}
}

func AuthorizeMCPSetTool(ctx context.Context, setName, toolName string) error {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: setName}); err != nil {
		return err
	}
	return service.CheckExecution(ctx, service.ExecutionAction{Kind: "mcp_tool", Name: toolName, ResourceID: setName})
}
