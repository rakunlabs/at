package server

import (
	"context"
	"fmt"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func (s *Server) newExecutionMCPClient(ctx context.Context, upstream service.MCPUpstream) (service.MCPClient, error) {
	// The legacy stdio manager pools children process-wide and starts them on
	// its own context. It cannot provide workspace provenance/cancellation.
	if upstream.Command != "" {
		return nil, fmt.Errorf("workspace-scoped stdio MCP runner: %w", service.ErrIsolatedWorkerUnsupported)
	}
	return service.NewExecutionHTTPMCPClient(ctx, upstream.URL, service.WithHeaders(upstream.Headers))
}

// This builder never invokes the legacy process-wide stdio manager, and checks
// ownership before resolving each referenced skill/workflow. A set ID is not
// authority to read every resource mentioned in its mutable configuration.
func (s *Server) buildExecutionMCPSet(ctx context.Context, setName string) (*mcpRuntime, error) {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: setName}); err != nil {
		return nil, err
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "handler", Name: "javascript"}); err != nil {
		return nil, err
	}
	srv, err := s.mcpSetToVirtualServer(ctx, setName)
	if err != nil {
		return nil, err
	}
	runtime := newMCPRuntime()
	for _, name := range srv.Config.EnabledSkills {
		if s.skillStore == nil {
			continue
		}
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: name}); err != nil {
			return nil, err
		}
		skill, err := s.skillStore.GetSkillByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if skill == nil {
			continue
		}
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: skill.ID}); err != nil {
			return nil, err
		}
		for _, tool := range skill.Tools {
			runtime.addTool(service.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema}, "scoped skill", func(ctx context.Context, args map[string]any) (string, error) {
				if err := workflow.AuthorizeToolHandler(ctx, tool.Name, tool.HandlerType, skill.ID, tool.Handler); err != nil {
					return "", err
				}
				lookup, lister, err := s.runtimeHandlerLookups(ctx, skill.ID)
				if err != nil {
					return "", err
				}
				if tool.HandlerType == "bash" {
					return workflow.ExecuteBashHandler(ctx, tool.Handler, args, lister, 60*time.Second)
				}
				return workflow.ExecuteJSHandlerContext(ctx, tool.Handler, args, lookup)
			})
		}
	}
	for _, id := range srv.Config.WorkflowIDs {
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: id}); err != nil {
			return nil, err
		}
	}
	b := &mcpRuntimeBuilder{server: s, acquireUpstream: func(ctx context.Context, u service.MCPUpstream) (mcpClientLease, error) {
		client, err := s.newExecutionMCPClient(ctx, u)
		return mcpClientLease{client: client, owned: true}, err
	}}
	b.addBuiltins(runtime, srv.Config)
	b.addWorkflows(ctx, runtime, srv.Config)
	b.addUpstreams(runtime, srv.Config.MCPUpstreams)
	// Custom HTTP templates still use unscoped credential expansion in the
	// legacy builder. They are intentionally not executable in this runtime.
	return runtime, nil
}

func (s *Server) listExecutionMCPSetTools(ctx context.Context, setName string) ([]service.Tool, error) {
	runtime, err := s.buildExecutionMCPSet(ctx, setName)
	if err != nil {
		return nil, err
	}
	defer closeMCPRuntime(ctx, runtime)
	var out []service.Tool
	for _, tool := range runtime.ListTools(ctx) {
		if workflow.AuthorizeMCPSetTool(ctx, setName, tool.Name) == nil {
			out = append(out, tool)
		}
	}
	return out, nil
}

func (s *Server) callExecutionMCPSetTool(ctx context.Context, setName, toolName string, args map[string]any) (string, error) {
	if err := workflow.AuthorizeMCPSetTool(ctx, setName, toolName); err != nil {
		return "", err
	}
	runtime, err := s.buildExecutionMCPSet(ctx, setName)
	if err != nil {
		return "", err
	}
	defer closeMCPRuntime(ctx, runtime)
	return runtime.CallTool(ctx, toolName, args)
}
