package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/agentloop"
)

// executionSkillLookup resolves a skill by ID, then by name, after checking
// that the bound execution identity may use it. It is nil without a store.
func (s *Server) executionSkillLookup(ctx context.Context) func(string) (*service.Skill, error) {
	if s.skillStore == nil {
		return nil
	}
	return func(nameOrID string) (*service.Skill, error) {
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: nameOrID}); err != nil {
			return nil, err
		}
		return s.lookupSkill(ctx, nameOrID)
	}
}

// storeSkillLookup is executionSkillLookup without the admission check, for
// workflow registries whose nodes apply their own.
func (s *Server) storeSkillLookup(ctx context.Context) func(string) (*service.Skill, error) {
	if s.skillStore == nil {
		return nil
	}
	return func(nameOrID string) (*service.Skill, error) {
		return s.lookupSkill(ctx, nameOrID)
	}
}

func (s *Server) lookupSkill(ctx context.Context, nameOrID string) (*service.Skill, error) {
	skill, err := s.skillStore.GetSkill(ctx, nameOrID)
	if err != nil || skill != nil {
		return skill, err
	}
	return s.skillStore.GetSkillByName(ctx, nameOrID)
}

// agentMCPTools is the MCP surface of one agent run: server-side MCP-set tools
// dispatched in process, and tools served by connected MCP clients (gateway
// loopback for MCP servers, set URLs, legacy mcp_urls and set upstreams).
// Every loop builds it the same way so a tool an agent can use in Sessions is
// also usable through org delegation and Developer Spaces.
type agentMCPTools struct {
	server    *Server
	logPrefix string
	// accept may refuse a tool (for example on a name collision); refused
	// tools are neither advertised nor dispatched. Nil accepts everything.
	accept func(service.Tool) bool

	tools       []service.Tool
	setTools    map[string]string // tool name -> MCP set name
	clientTools map[string]bool
	clients     []service.MCPClient
}

func (s *Server) newAgentMCPTools(logPrefix string, accept func(service.Tool) bool) *agentMCPTools {
	return &agentMCPTools{
		server:      s,
		logPrefix:   logPrefix,
		accept:      accept,
		setTools:    map[string]string{},
		clientTools: map[string]bool{},
	}
}

// Connect resolves setNames (skipping sets the identity may not use) and
// connects every resulting MCP endpoint plus urls. Failures are logged and
// skipped, never fatal, so one unreachable server does not stop the run.
func (m *agentMCPTools) Connect(ctx context.Context, setNames, urls []string) {
	s := m.server
	urls = append([]string{}, urls...)
	var upstreams []service.MCPUpstream

	if s.mcpSetStore != nil {
		for _, setName := range setNames {
			if service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: setName}) != nil {
				continue
			}
			set, err := s.mcpSetStore.GetMCPSetByName(ctx, setName)
			if err != nil || set == nil {
				slog.Warn(m.logPrefix+": MCP set not found", "set", setName, "error", err)
				continue
			}
			for _, serverName := range set.Servers {
				urls = append(urls, fmt.Sprintf("http://127.0.0.1:%s%s/gateway/v1/mcp/%s", s.config.Port, s.config.BasePath, serverName))
			}
			urls = append(urls, set.URLs...)
			upstreams = append(upstreams, set.Config.MCPUpstreams...)

			if len(set.Config.InlineTools) > 0 || len(set.Config.HTTPTools) > 0 || len(set.Config.EnabledBuiltinTools) > 0 || len(set.Config.WorkflowIDs) > 0 {
				tools, err := s.listExecutionMCPSetTools(ctx, setName)
				if err != nil {
					slog.Warn(m.logPrefix+": failed to list MCP set tools", "set", setName, "error", err)
					continue
				}
				for _, tool := range tools {
					if m.add(tool) {
						m.setTools[tool.Name] = setName
					}
				}
			}
		}
	}

	for _, url := range urls {
		client, err := service.NewExecutionHTTPMCPClient(ctx, url)
		if err != nil {
			slog.Warn(m.logPrefix+": failed to connect to MCP server, skipping", "url", url, "error", err)
			continue
		}
		m.addClient(ctx, client, url)
	}
	for _, upstream := range upstreams {
		label := upstream.URL + upstream.Command
		client, err := s.newExecutionMCPClient(ctx, upstream)
		if err != nil {
			slog.Warn(m.logPrefix+": failed to connect to MCP upstream, skipping", "upstream", label, "error", err)
			continue
		}
		m.addClient(ctx, client, label)
	}
}

// AddSetTools advertises a set's server-side tools without connecting its
// endpoints. Paired MCP sets migrated from legacy skill tools carry only those.
func (m *agentMCPTools) AddSetTools(ctx context.Context, setNames []string) {
	for _, setName := range setNames {
		tools, err := m.server.listExecutionMCPSetTools(ctx, setName)
		if err != nil {
			slog.Warn(m.logPrefix+": failed to load migrated skill tool set", "set", setName, "error", err)
			continue
		}
		for _, tool := range tools {
			if m.add(tool) {
				m.setTools[tool.Name] = setName
			}
		}
	}
}

func (m *agentMCPTools) add(tool service.Tool) bool {
	if m.accept != nil && !m.accept(tool) {
		return false
	}
	m.tools = append(m.tools, tool)
	return true
}

func (m *agentMCPTools) addClient(ctx context.Context, client service.MCPClient, label string) {
	m.clients = append(m.clients, client)
	tools, err := client.ListTools(ctx)
	if err != nil {
		slog.Warn(m.logPrefix+": failed to list MCP tools, skipping", "server", label, "error", err)
		return
	}
	for _, tool := range tools {
		if m.add(tool) {
			m.clientTools[tool.Name] = true
		}
	}
}

// Tools returns the advertised tool definitions in discovery order.
func (m *agentMCPTools) Tools() []service.Tool { return m.tools }

// SetName reports the MCP set serving name in process, if any.
func (m *agentMCPTools) SetName(name string) (string, bool) {
	setName, ok := m.setTools[name]
	return setName, ok
}

// Owns reports whether name is dispatched by Call.
func (m *agentMCPTools) Owns(name string) bool {
	if m == nil {
		return false
	}
	return m.setTools[name] != "" || m.clientTools[name]
}

// Call dispatches an owned tool. MCP-set tools are bounded by timeout (zero
// leaves ctx as is) and re-check set/tool admission; client tools use ctx
// directly, since the MCP client applies its own transport limits.
func (m *agentMCPTools) Call(ctx context.Context, name string, args map[string]any, timeout time.Duration) (string, error) {
	if setName := m.setTools[name]; setName != "" {
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		return m.server.callExecutionMCPSetTool(ctx, setName, name, args)
	}
	if m.clientTools[name] {
		return agentloop.CallMCPTool(ctx, m.clients, name, args)
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// Close closes every connected MCP client.
func (m *agentMCPTools) Close() {
	if m == nil {
		return
	}
	for _, client := range m.clients {
		client.Close()
	}
	m.clients = nil
}

// agentBuiltinTools returns the definitions of the named built-in tools the
// bound identity may run. agent_run is withheld at the subagent depth limit,
// and unknown names are logged rather than advertised.
func agentBuiltinTools(ctx context.Context, names []string, logPrefix string) []service.Tool {
	var tools []service.Tool
	for _, name := range names {
		if name == "agent_run" && subagentDepthFromContext(ctx) >= maxSubagentDepth {
			continue
		}
		if service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name}) != nil {
			continue
		}
		if !isKnownBuiltinTool(name) {
			slog.Warn(logPrefix+": unknown builtin tool in agent config", "tool", name)
			continue
		}
		if bt, ok := builtinToolByName(name); ok {
			tools = append(tools, service.Tool{Name: bt.Name, Description: bt.Description, InputSchema: bt.InputSchema})
		}
	}
	return tools
}

// forkStatusBuiltinTools returns agent_run_status / agent_run_cancel for runs
// whose skills fork background subagents, skipping names already present.
func forkStatusBuiltinTools(ctx context.Context, present func(string) bool) []service.Tool {
	var tools []service.Tool
	for _, name := range []string{"agent_run_status", "agent_run_cancel"} {
		if present(name) || service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name}) != nil {
			continue
		}
		if bt, ok := builtinToolByName(name); ok {
			tools = append(tools, service.Tool{Name: bt.Name, Description: bt.Description, InputSchema: bt.InputSchema})
		}
	}
	return tools
}
