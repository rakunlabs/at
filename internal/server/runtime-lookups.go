package server

import (
	"context"
	"fmt"

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

func nonSecretVariableLookup(ctx context.Context, store service.VariableStorer) workflow.VarLookup {
	if store == nil {
		return nil
	}
	return func(key string) (string, error) {
		v, err := store.GetVariableByKey(ctx, key)
		if err != nil {
			return "", err
		}
		if v == nil {
			return "", fmt.Errorf("variable %q not found", key)
		}
		if v.Secret {
			return "", fmt.Errorf("secret variable %q requires an approved variable reference", key)
		}
		return v.Value, nil
	}
}

func nonSecretVariableLister(ctx context.Context, store service.VariableStorer) workflow.VarLister {
	if store == nil {
		return nil
	}
	return func() (map[string]string, error) {
		vars, err := store.ListVariables(ctx, nil)
		if err != nil {
			return nil, err
		}
		values := make(map[string]string, len(vars.Data))
		for _, v := range vars.Data {
			if !v.Secret {
				values[v.Key] = v.Value
			}
		}
		return values, nil
	}
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
		if !v.Secret && service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "variables.read", ResourceID: v.ID}) == nil {
			values[v.Key] = v.Value
		}
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return nil, err
	}
	return values, nil
}
