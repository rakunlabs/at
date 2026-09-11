package workflow

import (
	"context"

	"github.com/rakunlabs/at/internal/service"
)

// ScopeDependencies creates a per-run copy. Never mutate shared dependencies
// when two workflows execute under different principals concurrently. Lookup
// adapters must resolve names in the bound workspace; the validator must check
// authoritative ownership (a requested ID is not proof of ownership).
func ScopeDependencies(ctx context.Context, base Dependencies) *Dependencies {
	d := base
	d.LoopGov = ScopeToolResults(ctx, base.LoopGov)
	check := func(name, id string) error {
		return service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: name, ResourceID: id})
	}
	lookupProvider := base.ProviderLookup
	if base.ScopedProviderLookup != nil {
		lookupProvider = func(key string) (service.LLMProvider, string, error) { return base.ScopedProviderLookup(ctx, key) }
	}
	if lookupProvider != nil {
		d.ProviderLookup = func(key string) (service.LLMProvider, string, error) {
			if err := check("providers.use", key); err != nil {
				return nil, "", err
			}
			p, model, err := lookupProvider(key)
			if err != nil {
				return nil, "", err
			}
			return service.ScopedExecutionProvider(p, key), model, nil
		}
	}
	if base.SkillLookup != nil {
		d.SkillLookup = func(key string) (*service.Skill, error) {
			if err := check("skills.use", key); err != nil {
				return nil, err
			}
			v, err := base.SkillLookup(key)
			if err == nil && v != nil {
				err = check("skills.use", v.ID)
			}
			if err != nil {
				return nil, err
			}
			return v, nil
		}
	}
	if base.VarLookup != nil {
		d.VarLookup = func(key string) (string, error) {
			if err := check("variables.read", key); err != nil {
				return "", err
			}
			return base.VarLookup(key)
		}
	}
	// An unscoped lister cannot safely enumerate cross-workspace secrets. The
	// workspace owner must supply a scoped lister explicitly via entrypoint wiring.
	if base.ScopedVarLister != nil {
		d.VarLister = func() (map[string]string, error) { return base.ScopedVarLister(ctx) }
	} else if base.VarLister != nil {
		d.VarLister = func() (map[string]string, error) { return nil, service.ErrExecutionDenied }
	}
	if base.NodeConfigLookup != nil {
		d.NodeConfigLookup = func(id string) (*service.NodeConfig, error) {
			if err := check("node_configs.use", id); err != nil {
				return nil, err
			}
			return base.NodeConfigLookup(id)
		}
	}
	if base.AgentLookup != nil {
		d.AgentLookup = func(c context.Context, id string) (*service.Agent, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: id}); err != nil {
				return nil, err
			}
			return base.AgentLookup(c, id)
		}
	}
	if base.ConnectionLookup != nil {
		d.ConnectionLookup = func(c context.Context, id string) (*service.Connection, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "connections.use", ResourceID: id}); err != nil {
				return nil, err
			}
			return base.ConnectionLookup(c, id)
		}
	}
	if base.WorkflowLookup != nil {
		d.WorkflowLookup = func(c context.Context, id string) (*service.Workflow, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: id}); err != nil {
				return nil, err
			}
			return base.WorkflowLookup(c, id)
		}
	}
	if base.WorkflowByNameLookup != nil {
		d.WorkflowByNameLookup = func(c context.Context, name string) (*service.Workflow, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: name}); err != nil {
				return nil, err
			}
			v, err := base.WorkflowByNameLookup(c, name)
			if err == nil && v != nil {
				err = service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: v.ID})
			}
			if err != nil {
				return nil, err
			}
			return v, nil
		}
	}
	if base.WorkflowExecutor != nil {
		d.WorkflowExecutor = func(c context.Context, wf *service.Workflow, args map[string]any) (string, error) {
			if wf == nil {
				return "", service.ErrExecutionDenied
			}
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: wf.ID}); err != nil {
				return "", err
			}
			return base.WorkflowExecutor(c, wf, args)
		}
	}
	if base.VersionLookup != nil {
		d.VersionLookup = func(c context.Context, id string) (*service.WorkflowGraph, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: id}); err != nil {
				return nil, err
			}
			return base.VersionLookup(c, id)
		}
	}
	if base.BuiltinToolDispatcher != nil {
		d.BuiltinToolDispatcher = func(c context.Context, name string, args map[string]any) (string, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "tool", Name: name}); err != nil {
				return "", err
			}
			return base.BuiltinToolDispatcher(c, name, args)
		}
	}
	if base.ChatSessionLookup != nil {
		d.ChatSessionLookup = func(c context.Context, id string) (*service.ChatSession, error) {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "chats.run", ResourceID: id}); err != nil {
				return nil, err
			}
			return base.ChatSessionLookup(c, id)
		}
	}
	if base.ChatMessageCreator != nil {
		d.ChatMessageCreator = func(c context.Context, id, role, content string) error {
			if err := service.CheckExecution(c, service.ExecutionAction{Kind: "resource", Name: "chats.run", ResourceID: id}); err != nil {
				return err
			}
			return base.ChatMessageCreator(c, id, role, content)
		}
	}
	if base.VarSave != nil {
		d.VarSave = func(context.Context, string, string) error { return service.ErrExecutionDenied }
	}
	return &d
}

type scopedToolGovernor struct {
	LoopGovernor
	truncate func(string, string, string) (string, bool)
}

func (g *scopedToolGovernor) TruncateToolResult(run, tool, body string) (string, bool) {
	return g.truncate(run, tool, body)
}

func ScopeToolResults(ctx context.Context, base LoopGovernor) LoopGovernor {
	if base == nil {
		return nil
	}
	if factory, ok := base.(interface {
		ScopedToolResults(context.Context) func(string, string, string) (string, bool)
	}); ok {
		return &scopedToolGovernor{LoopGovernor: base, truncate: factory.ScopedToolResults(ctx)}
	}
	return base
}
