package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

// developerToolkit is what one developer-session run executes with. Without an
// agent it is the built-in profile named by the session mode. With an agent it
// is that agent's system prompt, skills, MCP sets, built-in tools and
// confirmation list, layered on top of the container tools every session has.
// The session keeps its own provider/model, so switching models never changes
// what the agent may do.
type developerToolkit struct {
	agent   *service.Agent
	profile service.DeveloperAgentProfile

	systemPrompt  string
	tools         []service.Tool
	maxIterations int
	toolTimeout   int

	skills        *workflow.SkillRuntime
	builtins      map[string]bool
	mcpSetToolMap map[string]string
	mcpToolNames  map[string]bool
	mcpClients    []service.MCPClient
	confirm       map[string]bool
}

func (k *developerToolkit) Close() {
	if k == nil {
		return
	}
	for _, client := range k.mcpClients {
		client.Close()
	}
	k.mcpClients = nil
}

func isDeveloperContainerTool(name string) bool {
	for _, tool := range developerAgentTools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

// effect decides whether a call runs, asks first or is refused. Agent
// sessions follow the agent's own confirmation list; built-in profiles keep
// their ordered rules.
func (k *developerToolkit) effect(call service.ToolCall) string {
	if call.Name == "ask_user" {
		return "ask"
	}
	if k.agent == nil {
		return developerToolEffect(k.profile, call)
	}
	if k.confirm[call.Name] {
		return "ask"
	}
	return "allow"
}

// agentContext carries the identity and connection bindings the agent's own
// tools expect, exactly as the Sessions loop provides them.
func (k *developerToolkit) agentContext(ctx context.Context, session *service.DeveloperSession) context.Context {
	if k.agent == nil {
		return ctx
	}
	ctx = contextWithSessionUserID(ctx, session.OwnerUserID)
	ctx = contextWithAgentID(ctx, k.agent.ID)
	var overrides map[string]map[string]string
	if k.skills != nil {
		overrides = k.skills.ConnectionOverrides(k.agent.Config.Skills)
	}
	return workflow.ContextWithAgentConnections(ctx, k.agent.Config.Connections, overrides)
}

// buildDeveloperToolkit resolves the session's agent (if any). Every resource
// passes the same execution admission the Sessions loop applies; anything the
// caller may not use is skipped rather than failing the run, matching that loop.
func (s *Server) buildDeveloperToolkit(ctx context.Context, space *service.DeveloperSpace, session *service.DeveloperSession) (*developerToolkit, error) {
	if session.AgentID == "" {
		profile := developerProfile(space, session.Mode)
		return &developerToolkit{
			profile: profile, systemPrompt: profile.SystemPrompt, tools: developerAgentTools,
			maxIterations: profile.MaxIterations, toolTimeout: profile.ToolTimeoutSeconds,
		}, nil
	}
	if s.agentStore == nil {
		return nil, errors.New("agent store not configured")
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: session.AgentID}); err != nil {
		return nil, err
	}
	agent, err := s.agentStore.GetAgent(ctx, session.AgentID)
	if err != nil {
		return nil, fmt.Errorf("get agent: %w", err)
	}
	if agent == nil {
		return nil, errors.New("the agent this session runs as no longer exists; choose another agent")
	}

	kit := &developerToolkit{
		agent: agent, maxIterations: agent.Config.MaxIterations, toolTimeout: agent.Config.ToolTimeout,
		builtins: map[string]bool{}, mcpSetToolMap: map[string]string{}, mcpToolNames: map[string]bool{}, confirm: map[string]bool{},
	}
	for _, name := range agent.Config.ConfirmationRequiredTools {
		kit.confirm[name] = true
	}
	taken := map[string]bool{}
	for _, tool := range developerAgentTools {
		taken[tool.Name] = true
	}
	kit.tools = append(kit.tools, developerAgentTools...)
	// Container tools own their names: an agent tool with the same name would
	// otherwise shadow the one that works on the project.
	add := func(tool service.Tool) bool {
		if taken[tool.Name] {
			return false
		}
		taken[tool.Name] = true
		kit.tools = append(kit.tools, service.Tool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
		return true
	}

	var lookup workflow.SkillLookup
	if s.skillStore != nil {
		lookup = func(nameOrID string) (*service.Skill, error) {
			if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "skills.use", ResourceID: nameOrID}); err != nil {
				return nil, err
			}
			skill, err := s.skillStore.GetSkill(ctx, nameOrID)
			if err != nil || skill != nil {
				return skill, err
			}
			return s.skillStore.GetSkillByName(ctx, nameOrID)
		}
	}
	kit.skills, err = workflow.NewSkillRuntime(ctx, lookup, agent.Config.Skills, nil, func(name string, lookupErr error) {
		slog.Warn("developer session: skill lookup failed", "skill", name, "error", lookupErr)
	})
	if err != nil {
		return nil, fmt.Errorf("skill runtime: %w", err)
	}

	setNames := append(append([]string{}, agent.Config.MCPSets...), kit.skills.ToolSetNames()...)
	mcpURLs := append([]string{}, agent.Config.MCPs...)
	var upstreams []service.MCPUpstream
	if s.mcpSetStore != nil {
		for _, setName := range setNames {
			if service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: setName}) != nil {
				continue
			}
			set, err := s.mcpSetStore.GetMCPSetByName(ctx, setName)
			if err != nil || set == nil {
				slog.Warn("developer session: MCP set not found", "set", setName, "error", err)
				continue
			}
			for _, serverName := range set.Servers {
				mcpURLs = append(mcpURLs, fmt.Sprintf("http://127.0.0.1:%s%s/gateway/v1/mcp/%s", s.config.Port, s.config.BasePath, serverName))
			}
			mcpURLs = append(mcpURLs, set.URLs...)
			upstreams = append(upstreams, set.Config.MCPUpstreams...)
			if len(set.Config.InlineTools) > 0 || len(set.Config.HTTPTools) > 0 || len(set.Config.EnabledBuiltinTools) > 0 || len(set.Config.WorkflowIDs) > 0 {
				setTools, err := s.listExecutionMCPSetTools(ctx, setName)
				if err != nil {
					slog.Warn("developer session: failed to list MCP set tools", "set", setName, "error", err)
					continue
				}
				for _, tool := range setTools {
					if add(tool) {
						kit.mcpSetToolMap[tool.Name] = setName
					}
				}
			}
		}
	}
	addClient := func(client service.MCPClient, label string) {
		kit.mcpClients = append(kit.mcpClients, client)
		tools, err := client.ListTools(ctx)
		if err != nil {
			slog.Warn("developer session: failed to list MCP tools", "server", label, "error", err)
			return
		}
		for _, tool := range tools {
			if add(tool) {
				kit.mcpToolNames[tool.Name] = true
			}
		}
	}
	for _, url := range mcpURLs {
		client, err := service.NewExecutionHTTPMCPClient(ctx, url)
		if err != nil {
			slog.Warn("developer session: failed to connect to MCP server", "url", url, "error", err)
			continue
		}
		addClient(client, url)
	}
	for _, upstream := range upstreams {
		client, err := s.newExecutionMCPClient(ctx, upstream)
		if err != nil {
			slog.Warn("developer session: failed to connect to MCP upstream", "upstream", upstream.URL+upstream.Command, "error", err)
			continue
		}
		addClient(client, upstream.URL+upstream.Command)
	}

	// Built-in tools run on the AT host, not in the space container; the
	// system prompt says so, so the agent does not look for project files there.
	for _, name := range agent.Config.BuiltinTools {
		if name == "agent_run" && subagentDepthFromContext(ctx) >= maxSubagentDepth {
			continue
		}
		if service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name}) != nil || !isKnownBuiltinTool(name) {
			continue
		}
		bt, ok := builtinToolByName(name)
		if !ok {
			continue
		}
		if add(service.Tool{Name: bt.Name, Description: bt.Description, InputSchema: bt.InputSchema}) {
			kit.builtins[bt.Name] = true
		}
	}
	if kit.skills.HasFork() {
		for _, name := range []string{"agent_run_status", "agent_run_cancel"} {
			if kit.builtins[name] || service.CheckExecution(ctx, service.ExecutionAction{Kind: "tool", Name: name}) != nil {
				continue
			}
			if bt, ok := builtinToolByName(name); ok && add(service.Tool{Name: bt.Name, Description: bt.Description, InputSchema: bt.InputSchema}) {
				kit.builtins[bt.Name] = true
			}
		}
	}
	if kit.skills.HasSkills() {
		add(kit.skills.LoadSkillToolDef())
		if kit.skills.HasResources() {
			add(kit.skills.ReadSkillResourceToolDef())
		}
	}

	var prompt []string
	if agent.Config.SystemPrompt != "" {
		prompt = append(prompt, agent.Config.SystemPrompt)
	}
	if catalog := kit.skills.CatalogSystemPrompt(); catalog != "" {
		prompt = append(prompt, catalog)
	}
	if len(kit.builtins) > 0 {
		names := make([]string, 0, len(kit.builtins))
		for name := range kit.builtins {
			names = append(names, name)
		}
		slices.Sort(names)
		prompt = append(prompt, "These platform tools run on the AT server, not inside the project container, and cannot see the project files: "+strings.Join(names, ", ")+". Use the project tools (read_file, edit_file, run_command, ...) for anything in the project.")
	}
	kit.systemPrompt = strings.Join(prompt, "\n\n")
	return kit, nil
}

// execute runs one of the agent's own tools. Container tools never reach it.
func (s *Server) executeDeveloperAgentTool(ctx context.Context, kit *developerToolkit, call service.ToolCall) (string, error) {
	switch {
	case kit.skills != nil && call.Name == workflow.LoadSkillToolName:
		request, forked, err := kit.skills.ForkRequest(call.Arguments)
		if err != nil {
			return "", err
		}
		if forked {
			return s.dispatchBuiltinTool(contextWithSubagentSkill(ctx, request.Skill.ID), "agent_run", map[string]any{
				"agent": request.Agent, "task": request.Task, "context": request.Context, "background": request.Background,
			})
		}
		return kit.skills.HandleLoadSkill(call.Arguments)
	case kit.skills != nil && call.Name == workflow.ReadSkillResourceToolName:
		return kit.skills.HandleReadSkillResource(call.Arguments)
	case kit.mcpSetToolMap[call.Name] != "":
		setName := kit.mcpSetToolMap[call.Name]
		if err := workflow.AuthorizeMCPSetTool(ctx, setName, call.Name); err != nil {
			return "", err
		}
		return s.callExecutionMCPSetTool(ctx, setName, call.Name, call.Arguments)
	case kit.mcpToolNames[call.Name]:
		return callMCPToolFromClients(ctx, kit.mcpClients, call.Name, call.Arguments)
	case kit.builtins[call.Name]:
		if err := workflow.AuthorizeToolHandler(ctx, call.Name, "builtin", "", call.Name); err != nil {
			return "", err
		}
		return s.dispatchBuiltinTool(ctx, call.Name, call.Arguments)
	}
	return "", fmt.Errorf("unknown tool %q", call.Name)
}
