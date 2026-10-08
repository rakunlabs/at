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

	skills   *workflow.SkillRuntime
	builtins map[string]bool
	mcp      *agentMCPTools
	confirm  map[string]bool
}

func (k *developerToolkit) Close() {
	if k == nil {
		return
	}
	k.mcp.Close()
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
		builtins: map[string]bool{}, confirm: map[string]bool{},
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

	kit.skills, err = workflow.NewSkillRuntime(ctx, s.executionSkillLookup(ctx), agent.Config.Skills, nil, func(name string, lookupErr error) {
		slog.Warn("developer session: skill lookup failed", "skill", name, "error", lookupErr)
	})
	if err != nil {
		return nil, fmt.Errorf("skill runtime: %w", err)
	}

	kit.mcp = s.newAgentMCPTools("developer session", add)
	// The agent's connection bindings must be on the context while
	// connecting, for OAuth MCP upstreams that use the agent's account.
	kit.mcp.Connect(kit.agentContext(ctx, session), append(append([]string{}, agent.Config.MCPSets...), kit.skills.ToolSetNames()...), agent.Config.MCPs)

	// Built-in tools run on the AT host, not in the space container; the
	// system prompt says so, so the agent does not look for project files there.
	builtins := agentBuiltinTools(ctx, agent.Config.BuiltinTools, "developer session")
	if kit.skills.HasFork() {
		builtins = append(builtins, forkStatusBuiltinTools(ctx, func(name string) bool { return kit.builtins[name] || taken[name] })...)
	}
	for _, tool := range builtins {
		if add(tool) {
			kit.builtins[tool.Name] = true
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
	case kit.mcp.Owns(call.Name):
		return kit.mcp.Call(ctx, call.Name, call.Arguments, 0)
	case kit.builtins[call.Name]:
		if err := workflow.AuthorizeToolHandler(ctx, call.Name, "builtin", "", call.Name); err != nil {
			return "", err
		}
		return s.dispatchBuiltinTool(ctx, call.Name, call.Arguments)
	}
	return "", fmt.Errorf("unknown tool %q", call.Name)
}
