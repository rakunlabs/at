package server

import (
	"context"

	"github.com/rakunlabs/query"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

func runtimeWorkspaceQuery(ctx context.Context) (*query.Query, error) {
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return nil, err
	}
	p, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	return query.New().AddWhere(query.NewExpressionCmp(query.OperatorEq, "workspace_id", p.WorkspaceID).Expression()), nil
}

func (s *Server) runtimeHandlerLookups(ctx context.Context, skillID string) (workflow.VarLookup, workflow.VarLister, error) {
	lookup := func(key string) (string, error) {
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.read", ResourceID: key}); err != nil {
			return "", err
		}
		if s.variableStore == nil {
			return "", service.ErrExecutionDenied
		}
		value, err := s.variableStore.GetVariableByKey(ctx, key)
		if err != nil {
			return "", err
		}
		if value == nil {
			return "", service.ErrExecutionDenied
		}
		return value.Value, nil
	}
	lister := func() (map[string]string, error) { return s.runtimeVariableLister(ctx) }
	if agentID := agentIDFromContext(ctx); agentID != "" && s.agentStore != nil {
		if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: agentID}); err != nil {
			return nil, nil, err
		}
		agent, err := s.agentStore.GetAgent(ctx, agentID)
		if err != nil {
			return nil, nil, err
		}
		if agent == nil {
			return nil, nil, service.ErrExecutionDenied
		}
		var overrides map[string]string
		for _, ref := range agent.Config.Skills {
			if ref.ID == skillID {
				overrides = ref.Connections
			}
		}
		bindings := workflow.ResolveAgentConnectionBindings(ctx, s.connectionLookupFunc(), agent.Config.Connections, overrides)
		return workflow.WrapVarLookupWithConnectionsContext(ctx, lookup, bindings), workflow.WrapVarListerWithConnectionsContext(ctx, lister, bindings), nil
	}
	return lookup, lister, nil
}

func (s *Server) runtimeVariableLister(ctx context.Context) (map[string]string, error) {
	q, err := runtimeWorkspaceQuery(ctx)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if s.variableStore == nil {
		return values, nil
	}
	vars, err := s.variableStore.ListVariables(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, v := range vars.Data {
		if service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.read", ResourceID: v.ID}) == nil {
			values[v.Key] = v.Value
		}
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return nil, err
	}
	return values, nil
}
