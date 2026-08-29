package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type mcpToolCall func(context.Context, map[string]any) (string, error)

type mcpToolRoute struct {
	id       int
	priority int
	source   string
	tool     service.Tool
	call     mcpToolCall
}

type mcpDiscoveredTool struct {
	tool service.Tool
	call mcpToolCall
}

type mcpLazySource struct {
	priority int
	source   string
	discover func(context.Context) ([]mcpDiscoveredTool, error)
	done     bool
}

// mcpClientLease makes client ownership explicit. HTTP clients are owned by
// the request runtime; stdio clients are borrowed from StdioProcessManager.
type mcpClientLease struct {
	client service.MCPClient
	owned  bool
}

type mcpRuntime struct {
	routes      map[string][]mcpToolRoute
	sources     []*mcpLazySource
	diagnostics []error
	owned       []service.MCPClient
	children    []*mcpRuntime
	nextOrder   int
	nextRouteID int
}

func newMCPRuntime() *mcpRuntime {
	return &mcpRuntime{routes: make(map[string][]mcpToolRoute)}
}

func (r *mcpRuntime) addTool(tool service.Tool, source string, call mcpToolCall) {
	r.addToolAt(r.reserveOrder(), tool, source, call)
}

func (r *mcpRuntime) reserveOrder() int {
	order := r.nextOrder
	r.nextOrder++
	return order
}

func (r *mcpRuntime) addToolAt(priority int, tool service.Tool, source string, call mcpToolCall) {
	route := mcpToolRoute{
		id:       r.nextRouteID,
		priority: priority,
		source:   source,
		tool:     tool,
		call:     call,
	}
	r.nextRouteID++
	r.routes[tool.Name] = append(r.routes[tool.Name], route)
	sort.SliceStable(r.routes[tool.Name], func(i, j int) bool {
		left, right := r.routes[tool.Name][i], r.routes[tool.Name][j]
		if left.priority == right.priority {
			return left.id < right.id
		}
		return left.priority < right.priority
	})
	if len(r.routes[tool.Name]) > 1 {
		slog.Warn("duplicate MCP tool route registered", "tool", tool.Name, "source", source)
	}
}

func (r *mcpRuntime) addSource(source string, discover func(context.Context) ([]mcpDiscoveredTool, error)) {
	r.sources = append(r.sources, &mcpLazySource{
		priority: r.reserveOrder(),
		source:   source,
		discover: discover,
	})
}

func (r *mcpRuntime) addClient(ctx context.Context, lease mcpClientLease, source string) {
	priority := r.reserveOrder()
	tools, err := r.discoverClient(ctx, lease, source)
	if err != nil {
		r.addDiagnostic(err)
		return
	}
	for _, tool := range tools {
		r.addToolAt(priority, tool.tool, source, tool.call)
	}
}

func (r *mcpRuntime) addClientSource(source string, acquire func(context.Context) (mcpClientLease, error)) {
	r.addSource(source, func(ctx context.Context) ([]mcpDiscoveredTool, error) {
		lease, err := acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("connect to %s: %w", source, err)
		}
		return r.discoverClient(ctx, lease, source)
	})
}

func (r *mcpRuntime) discoverClient(ctx context.Context, lease mcpClientLease, source string) ([]mcpDiscoveredTool, error) {
	if lease.owned {
		r.owned = append(r.owned, lease.client)
	}

	tools, err := lease.client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tools from %s: %w", source, err)
	}

	client := lease.client
	discovered := make([]mcpDiscoveredTool, 0, len(tools))
	for _, tool := range tools {
		tool := tool
		discovered = append(discovered, mcpDiscoveredTool{tool: tool, call: func(ctx context.Context, args map[string]any) (string, error) {
			result, err := client.CallTool(ctx, tool.Name, args)
			if err != nil {
				return "", fmt.Errorf("call %s tool %q: %w", source, tool.Name, err)
			}
			return result, nil
		}})
	}
	return discovered, nil
}

func (r *mcpRuntime) addDiagnostic(err error) {
	if err != nil {
		r.diagnostics = append(r.diagnostics, err)
		slog.Warn("MCP runtime discovery failed", "error", err)
	}
}

func (r *mcpRuntime) Tools() []service.Tool {
	var routes []mcpToolRoute
	for _, namedRoutes := range r.routes {
		routes = append(routes, namedRoutes...)
	}
	sort.SliceStable(routes, func(i, j int) bool {
		if routes[i].priority == routes[j].priority {
			return routes[i].id < routes[j].id
		}
		return routes[i].priority < routes[j].priority
	})

	tools := make([]service.Tool, 0, len(routes))
	seen := make(map[string]bool)
	for _, route := range routes {
		if seen[route.tool.Name] {
			continue
		}
		seen[route.tool.Name] = true
		tools = append(tools, route.tool)
	}
	return tools
}

func (r *mcpRuntime) ListTools(ctx context.Context) []service.Tool {
	for _, source := range r.sources {
		if ctx.Err() != nil {
			break
		}
		r.discoverSource(ctx, source)
	}
	return r.Tools()
}

func (r *mcpRuntime) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	attempted := make(map[int]bool)
	var callErrs []error
	if result, ok := r.callKnownRoutes(ctx, name, args, attempted, &callErrs); ok {
		return result, nil
	}

	for _, source := range r.sources {
		if ctx.Err() != nil {
			break
		}
		r.discoverSource(ctx, source)
		if result, ok := r.callKnownRoutes(ctx, name, args, attempted, &callErrs); ok {
			return result, nil
		}
	}

	if len(callErrs) == 0 && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if len(callErrs) > 0 {
		return "", fmt.Errorf("all MCP routes for tool %q failed: %w", name, errors.Join(callErrs...))
	}
	return "", &mcpToolNotFoundError{name: name, diagnostics: append([]error(nil), r.diagnostics...)}
}

func (r *mcpRuntime) callKnownRoutes(ctx context.Context, name string, args map[string]any, attempted map[int]bool, callErrs *[]error) (string, bool) {
	for _, route := range r.routes[name] {
		if attempted[route.id] {
			continue
		}
		attempted[route.id] = true
		result, err := route.call(ctx, args)
		if err == nil {
			return result, true
		}
		*callErrs = append(*callErrs, fmt.Errorf("%s: %w", route.source, err))
	}
	return "", false
}

func (r *mcpRuntime) discoverSource(ctx context.Context, source *mcpLazySource) {
	if source.done {
		return
	}
	source.done = true
	tools, err := source.discover(ctx)
	if err != nil {
		r.addDiagnostic(err)
		return
	}
	for _, tool := range tools {
		r.addToolAt(source.priority, tool.tool, source.source, tool.call)
	}
}

func (r *mcpRuntime) Close(ctx context.Context) error {
	var errs []error
	for _, child := range r.children {
		if err := child.Close(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	for _, client := range r.owned {
		if err := closeMCPClient(ctx, client); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func closeMCPClient(ctx context.Context, client service.MCPClient) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		if closer, ok := client.(service.MCPClientContextCloser); ok {
			done <- closer.CloseContext(ctx)
			return
		}
		done <- client.Close()
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

const mcpRuntimeCloseTimeout = time.Second

func closeMCPRuntime(parent context.Context, runtime *mcpRuntime) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), mcpRuntimeCloseTimeout)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		slog.Warn("close MCP runtime failed", "error", err)
	}
}

type mcpToolNotFoundError struct {
	name        string
	diagnostics []error
}

func (e *mcpToolNotFoundError) Error() string {
	if len(e.diagnostics) == 0 {
		return fmt.Sprintf("unknown tool: %s", e.name)
	}
	return fmt.Sprintf("unknown tool: %s; MCP discovery failed: %v", e.name, errors.Join(e.diagnostics...))
}

type mcpRuntimeBuilder struct {
	server          *Server
	acquireUpstream func(context.Context, service.MCPUpstream) (mcpClientLease, error)
	acquireURL      func(context.Context, string) (mcpClientLease, error)
}

func (s *Server) newMCPRuntimeBuilder() *mcpRuntimeBuilder {
	return &mcpRuntimeBuilder{
		server:          s,
		acquireUpstream: s.acquireMCPClient,
		acquireURL: func(ctx context.Context, url string) (mcpClientLease, error) {
			client, err := service.NewHTTPMCPClient(ctx, url)
			return mcpClientLease{client: client, owned: true}, err
		},
	}
}

// buildGateway preserves gateway dispatch precedence:
// HTTP > skill > upstream > referenced set > URL > builtin > workflow.
func (b *mcpRuntimeBuilder) buildGateway(ctx context.Context, srv *service.MCPServer) *mcpRuntime {
	runtime := newMCPRuntime()
	b.addHTTPTools(runtime, srv, true)
	b.addSkills(ctx, runtime, srv.Config)
	b.addUpstreams(runtime, srv.Config.MCPUpstreams)

	for _, setName := range srv.Servers {
		setName := setName
		source := fmt.Sprintf("MCP set %q", setName)
		runtime.addSource(source, func(ctx context.Context) ([]mcpDiscoveredTool, error) {
			child, err := b.buildSet(ctx, setName)
			if err != nil {
				return nil, err
			}
			runtime.children = append(runtime.children, child)
			tools := child.ListTools(ctx)
			discovered := make([]mcpDiscoveredTool, 0, len(tools))
			for _, tool := range tools {
				tool := tool
				discovered = append(discovered, mcpDiscoveredTool{tool: tool, call: func(ctx context.Context, args map[string]any) (string, error) {
					return child.CallTool(ctx, tool.Name, args)
				}})
			}
			return discovered, nil
		})
	}

	for _, url := range srv.URLs {
		url := url
		source := fmt.Sprintf("MCP URL %q", url)
		runtime.addClientSource(source, func(ctx context.Context) (mcpClientLease, error) {
			return b.acquireURL(ctx, url)
		})
	}

	b.addBuiltins(runtime, srv.Config)
	b.addWorkflows(ctx, runtime, srv.Config)
	return runtime
}

// buildSet preserves direct MCP-set dispatch precedence:
// skill > builtin > workflow > upstream > HTTP.
func (b *mcpRuntimeBuilder) buildSet(ctx context.Context, setName string) (*mcpRuntime, error) {
	srv, err := b.server.mcpSetToVirtualServer(ctx, setName)
	if err != nil {
		return nil, err
	}

	runtime := newMCPRuntime()
	b.addSkills(ctx, runtime, srv.Config)
	b.addBuiltins(runtime, srv.Config)
	b.addWorkflows(ctx, runtime, srv.Config)
	b.addUpstreams(runtime, srv.Config.MCPUpstreams)
	b.addHTTPTools(runtime, srv, false)
	return runtime, nil
}

func (b *mcpRuntimeBuilder) addHTTPTools(runtime *mcpRuntime, srv *service.MCPServer, gatewayResult bool) {
	for _, httpTool := range srv.Config.HTTPTools {
		httpTool := httpTool
		schema := httpTool.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tool := service.Tool{Name: httpTool.Name, Description: httpTool.Description, InputSchema: schema}
		runtime.addTool(tool, "HTTP tool", func(ctx context.Context, args map[string]any) (string, error) {
			if gatewayResult {
				return b.server.callGatewayMCPHTTPTool(ctx, httpTool, args)
			}
			return b.server.callHTTPToolInline(ctx, httpTool, args, srv)
		})
	}
}

func (b *mcpRuntimeBuilder) addSkills(ctx context.Context, runtime *mcpRuntime, config service.MCPServerConfig) {
	if b.server.skillStore == nil {
		return
	}
	for _, skillName := range config.EnabledSkills {
		skill, err := b.server.skillStore.GetSkillByName(ctx, skillName)
		if err != nil {
			runtime.addDiagnostic(fmt.Errorf("load skill %q: %w", skillName, err))
			continue
		}
		if skill == nil {
			continue
		}
		for _, skillTool := range skill.Tools {
			skillTool := skillTool
			definition := service.Tool{Name: skillTool.Name, Description: skillTool.Description, InputSchema: skillTool.InputSchema}
			runtime.addTool(definition, fmt.Sprintf("skill %q", skillName), func(ctx context.Context, args map[string]any) (string, error) {
				result, err := b.server.executeSkillTool(ctx, &skillTool, args)
				if err != nil {
					return "", fmt.Errorf("skill tool execution failed: %w", err)
				}
				return result, nil
			})
		}
	}
}

func (b *mcpRuntimeBuilder) addUpstreams(runtime *mcpRuntime, upstreams []service.MCPUpstream) {
	for _, upstream := range upstreams {
		upstream := upstream
		name := upstream.URL + upstream.Command
		source := fmt.Sprintf("MCP upstream %q", name)
		runtime.addClientSource(source, func(ctx context.Context) (mcpClientLease, error) {
			return b.acquireUpstream(ctx, upstream)
		})
	}
}

func (b *mcpRuntimeBuilder) addBuiltins(runtime *mcpRuntime, config service.MCPServerConfig) {
	for _, toolName := range config.EnabledBuiltinTools {
		if !isKnownBuiltinTool(toolName) {
			continue
		}
		for _, builtin := range builtinTools {
			if builtin.Name != toolName {
				continue
			}
			builtin := builtin
			runtime.addTool(service.Tool{Name: builtin.Name, Description: builtin.Description, InputSchema: builtin.InputSchema}, "builtin", func(ctx context.Context, args map[string]any) (string, error) {
				result, err := b.server.dispatchBuiltinTool(ctx, builtin.Name, args)
				if err != nil {
					return "", fmt.Errorf("builtin tool execution failed: %w", err)
				}
				return result, nil
			})
			break
		}
	}
}

func (b *mcpRuntimeBuilder) addWorkflows(ctx context.Context, runtime *mcpRuntime, config service.MCPServerConfig) {
	if b.server.workflowStore == nil {
		return
	}
	for _, workflowID := range config.WorkflowIDs {
		wf, err := b.server.workflowStore.GetWorkflow(ctx, workflowID)
		if err != nil {
			runtime.addDiagnostic(fmt.Errorf("load workflow %q: %w", workflowID, err))
			continue
		}
		if wf == nil {
			continue
		}
		tool := b.server.activeWorkflowToolDef(ctx, wf)
		runtime.addTool(tool, fmt.Sprintf("workflow %q", workflowID), func(ctx context.Context, args map[string]any) (string, error) {
			result, err := b.server.executeWorkflowTool(ctx, wf, args)
			if err != nil {
				return "", fmt.Errorf("workflow tool execution failed: %w", err)
			}
			return result, nil
		})
	}
}
