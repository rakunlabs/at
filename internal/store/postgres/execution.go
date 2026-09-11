package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/doug-martin/goqu/v9"
	"github.com/doug-martin/goqu/v9/exp"

	"github.com/rakunlabs/at/internal/service"
)

var _ service.ExecutionStorer = (*Postgres)(nil)

func (p *Postgres) executionTable(name string) exp.IdentifierExpression {
	return goqu.T(strings.TrimSuffix(fmt.Sprint(p.tableTasks.GetTable()), "tasks") + name)
}

// GetExecutionPolicy is an internal authority lookup; callers must not expose
// it as an unscoped business resource API.
func (p *Postgres) GetExecutionPolicy(ctx context.Context, workspaceID string) (*service.ExecutionPolicy, error) {
	if workspaceID == "" {
		return nil, service.ErrExecutionDenied
	}
	var data []byte
	found, err := p.goqu.From(p.executionTable("execution_policies")).Select("data").Where(goqu.Ex{"workspace_id": workspaceID}).ScanValContext(ctx, &data)
	if err != nil {
		return nil, fmt.Errorf("get execution policy: %w", err)
	}
	if !found {
		return &service.ExecutionPolicy{WorkspaceID: workspaceID, Mode: service.ExecutionRestricted}, nil
	}
	var policy service.ExecutionPolicy
	if err := json.Unmarshal(data, &policy); err != nil {
		return nil, fmt.Errorf("decode execution policy: %w", err)
	}
	return &policy, nil
}

func (p *Postgres) SaveExecutionPolicy(ctx context.Context, requested service.ExecutionPolicy) error {
	policy, err := service.PrepareExecutionPolicyChange(ctx, requested)
	if err != nil {
		return fmt.Errorf("authorize execution policy: %w", err)
	}
	expected := policy.Version
	policy.Version++
	data, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode execution policy: %w", err)
	}
	row := goqu.Record{"workspace_id": policy.WorkspaceID, "version": policy.Version, "data": goqu.L("?::jsonb", string(data))}
	var rows int64
	if expected == 0 {
		result, err := p.goqu.Insert(p.executionTable("execution_policies")).Rows(row).OnConflict(goqu.DoNothing()).Executor().ExecContext(ctx)
		if err != nil {
			return fmt.Errorf("create execution policy: %w", err)
		}
		rows, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count execution policy writes: %w", err)
		}
	} else {
		result, err := p.goqu.Update(p.executionTable("execution_policies")).Set(row).Where(goqu.Ex{"workspace_id": policy.WorkspaceID, "version": expected}).Executor().ExecContext(ctx)
		if err != nil {
			return fmt.Errorf("update execution policy: %w", err)
		}
		rows, err = result.RowsAffected()
		if err != nil {
			return fmt.Errorf("count execution policy writes: %w", err)
		}
	}
	if rows != 1 {
		return fmt.Errorf("execution policy changed concurrently: %w", service.ErrExecutionDenied)
	}
	return nil
}

func (p *Postgres) CreateExecutionProvenance(ctx context.Context, value service.ExecutionProvenance) error {
	bound, _, ok := service.ExecutionFromContext(ctx)
	if !ok || bound != value {
		return service.ErrExecutionDenied
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode execution provenance: %w", err)
	}
	_, err = p.goqu.Insert(p.executionTable("execution_provenance")).Rows(goqu.Record{"run_id": value.RunID, "workspace_id": value.WorkspaceID, "data": goqu.L("?::jsonb", string(data))}).Executor().ExecContext(ctx)
	if err != nil {
		return fmt.Errorf("create immutable execution provenance: %w", err)
	}
	return nil
}

// GetExecutionProvenance is for trusted background entrypoint wiring only.
// Loading provenance never authenticates it: BindExecution revalidates it.
func (p *Postgres) GetExecutionProvenance(ctx context.Context, runID string) (*service.ExecutionProvenance, error) {
	var data []byte
	found, err := p.goqu.From(p.executionTable("execution_provenance")).Select("data").Where(goqu.Ex{"run_id": runID}).ScanValContext(ctx, &data)
	if err != nil {
		return nil, fmt.Errorf("get execution provenance: %w", err)
	}
	if !found {
		return nil, nil
	}
	var value service.ExecutionProvenance
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("decode execution provenance: %w", err)
	}
	return &value, nil
}
