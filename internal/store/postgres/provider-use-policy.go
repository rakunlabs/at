package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/doug-martin/goqu/v9"
	"github.com/worldline-go/types"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

func (p *Postgres) providerUsePolicy(ctx context.Context, reader workspaceReader, workspace string) (*service.ProviderUsePolicy, error) {
	var row struct {
		Config  string `db:"config"`
		Version int64  `db:"version"`
	}
	_, err := reader.From(p.workspaceTable("provider_use_policy")).Select("config", "version").Where(goqu.C("workspace_id").Eq(workspace)).ScanStructContext(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("read provider use policy: %w", err)
	}
	v := &service.ProviderUsePolicy{}
	if row.Config != "" {
		if err := json.Unmarshal([]byte(row.Config), v); err != nil {
			return nil, err
		}
	}
	v.Version = row.Version
	return v, nil
}

func (p *Postgres) GetProviderUsePolicy(ctx context.Context) (*service.ProviderUsePolicy, error) {
	a, err := p.businessPrincipal(ctx)
	if err != nil {
		return nil, err
	}
	return p.providerUsePolicy(ctx, p.goqu, a.WorkspaceID)
}

func (p *Postgres) SaveProviderUsePolicy(ctx context.Context, v service.ProviderUsePolicy) (*service.ProviderUsePolicy, error) {
	if err := v.Validate(); err != nil {
		return nil, err
	}
	tx, err := p.goqu.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	a, err := p.workspaceActor(ctx, tx, "workspace.write")
	if err != nil {
		return nil, err
	}
	current, err := p.providerUsePolicy(ctx, tx, a.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if v.Version != current.Version {
		return nil, service.ErrWorkspaceConflict
	}
	v.Version++
	data, _ := json.Marshal(v)
	_, err = tx.Insert(p.workspaceTable("provider_use_policy")).Rows(goqu.Record{"workspace_id": a.WorkspaceID, "config": types.RawJSON(data), "version": v.Version}).OnConflict(goqu.DoUpdate("workspace_id", goqu.Record{"config": types.RawJSON(data), "version": v.Version})).Executor().ExecContext(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.bumpWorkspacePolicy(ctx, tx, a.WorkspaceID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &v, nil
}

func (p *Postgres) checkPersonalProviderPolicy(ctx context.Context, reader workspaceReader, workspace string, cfg config.LLMConfig) error {
	policy, err := p.providerUsePolicy(ctx, reader, workspace)
	if err != nil {
		return err
	}
	if !policy.Admits(cfg) {
		return fmt.Errorf("workspace policy does not allow this personal provider endpoint: %w", service.ErrAccessDenied)
	}
	return nil
}
